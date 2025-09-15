package generator

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"simulator/internal/config"
	"simulator/internal/csvreader"
	"simulator/internal/httpclient"
	"simulator/internal/metrics"
	"simulator/logger"
)

// Generator manages the EPS-based ticket generation and API testing
type Generator struct {
	config     *config.Config
	csvReader  *csvreader.CSVReader
	httpClient *httpclient.HTTPClient
	metrics    *metrics.Metrics
	logger     logger.Logger

	// Ticket ID tracking for get/update operations with tenant association
	createdTickets map[string][]string // tenantID -> []ticketID
	ticketIDsMu    sync.RWMutex

	// Searchable field values tracking for realistic search operations
	searchableValues map[string]map[string][]string // fieldName -> tenantID -> []values
	searchValuesMu   sync.RWMutex

	// Random number generator
	rand *rand.Rand
}

// New creates a new generator instance
func New(cfg *config.Config, csvReader *csvreader.CSVReader, httpClient *httpclient.HTTPClient, metricsCollector *metrics.Metrics) *Generator {
	// Configure HTTP client
	httpClient.SetBaseURL(cfg.APIBaseURL)
	httpClient.SetTenantIDs(cfg.TenantIDs)

	return &Generator{
		config:           cfg,
		csvReader:        csvReader,
		httpClient:       httpClient,
		metrics:          metricsCollector,
		logger:           logger.NewLogger("simulator", "simulator"),
		createdTickets:   make(map[string][]string),
		searchableValues: make(map[string]map[string][]string),
		rand:             rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Start begins the EPS-based generation process
func (g *Generator) Start(ctx context.Context) error {
	g.logger.Info("Starting ticket generator...")

	var wg sync.WaitGroup

	// Start create ticket generator
	if g.config.Operations.Create.Enabled && g.config.EPS.Create > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.runCreateGenerator(ctx)
		}()
	}

	// Start search generator
	if g.config.Operations.Search.Enabled && g.config.EPS.Search > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.runSearchGenerator(ctx)
		}()
	}

	// Start get generator
	if g.config.Operations.Get.Enabled && g.config.EPS.Get > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.runGetGenerator(ctx)
		}()
	}

	// Start update generator
	if g.config.Operations.Update.Enabled && g.config.EPS.Update > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.runUpdateGenerator(ctx)
		}()
	}

	g.logger.Info(fmt.Sprintf("Generator started with EPS rates - Create: %.2f, Search: %.2f, Get: %.2f, Update: %.2f",
		g.config.EPS.Create, g.config.EPS.Search, g.config.EPS.Get, g.config.EPS.Update))

	wg.Wait()
	g.logger.Info("All generators stopped")
	return nil
}

// runCreateGenerator runs the ticket creation generator
func (g *Generator) runCreateGenerator(ctx context.Context) {
	g.logger.Info(fmt.Sprintf("Starting create generator with EPS: %.2f", g.config.EPS.Create))

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Create generator stopped")
			return
		case <-ticker.C:

			for i := 0; i < int(g.config.EPS.Create); i++ {

				g.createTicket(ctx)
			}
		}
	}
}

// runSearchGenerator runs the search generator
func (g *Generator) runSearchGenerator(ctx context.Context) {
	g.logger.Info(fmt.Sprintf("Starting search generator with EPS: %.2f", g.config.EPS.Search))

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Search generator stopped")
			return
		case <-ticker.C:

			for i := 0; i < int(g.config.EPS.Search); i++ {

				g.searchTickets(ctx)
			}
		}
	}
}

// runGetGenerator runs the get ticket generator
func (g *Generator) runGetGenerator(ctx context.Context) {
	g.logger.Info(fmt.Sprintf("Starting get generator with EPS: %.2f", g.config.EPS.Get))

	interval := time.Duration(float64(time.Second) / g.config.EPS.Get)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Get generator stopped")
			return
		case <-ticker.C:
			g.getTicket(ctx)
		}
	}
}

// runUpdateGenerator runs the update ticket generator
func (g *Generator) runUpdateGenerator(ctx context.Context) {
	g.logger.Info(fmt.Sprintf("Starting update generator with EPS: %.2f", g.config.EPS.Update))

	interval := time.Duration(float64(time.Second) / g.config.EPS.Update)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Update generator stopped")
			return
		case <-ticker.C:
			g.updateTicket(ctx)
		}
	}
}

// createTicket creates a single ticket
func (g *Generator) createTicket(ctx context.Context) {
	startTime := time.Now()

	// Get random ticket data from CSV
	ticketData, err := g.csvReader.GetRandomTicket()
	if err != nil {
		g.logger.Error(fmt.Sprintf("Failed to get random ticket data: %v", err))
		g.metrics.RecordCreateRequest(time.Since(startTime), false)
		return
	}

	// Apply field filtering and transformations
	processedData := g.processCreateData(ticketData.Fields)

	// Make API request with tenant tracking
	response, tenantID, err := g.httpClient.CreateTicketWithTenant(ctx, processedData)
	duration := time.Since(startTime)

	success := err == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300
	g.metrics.RecordCreateRequest(duration, success)

	if err != nil {
		g.logger.Error(fmt.Sprintf("Create ticket failed: %v", err))
		return
	}

	if !success {
		g.logger.Error(fmt.Sprintf("Create ticket failed with status %d: %s", response.StatusCode, response.Error))
		return
	}

	// Extract ticket ID from response and store it with tenant association
	if ticketID := g.extractTicketID(response.Body); ticketID != "" {
		g.addCreatedTicketID(tenantID, ticketID)

		// Collect searchable field values for future search operations
		g.collectSearchableValues(tenantID, processedData)

		// Extract database latency from response
		dbLatency := g.extractDatabaseLatency(response.Body)
		g.logger.Info(fmt.Sprintf("Created ticket %s for tenant %s (total: %.2fms, db: %.2fms)",
			ticketID, tenantID, float64(duration.Nanoseconds())/1000000, dbLatency))
	} else {
		dbLatency := g.extractDatabaseLatency(response.Body)
		g.logger.Info(fmt.Sprintf("Created ticket for tenant %s (total: %.2fms, db: %.2fms) - ID not found in response",
			tenantID, float64(duration.Nanoseconds())/1000000, dbLatency))
	}
}

// searchTickets performs a search operation
func (g *Generator) searchTickets(ctx context.Context) {
	startTime := time.Now()

	// Generate search request with tenant awareness
	searchRequest, tenantID := g.generateSearchRequest()

	// Make API request with correct tenant
	var response *httpclient.APIResponse
	var err error

	if tenantID != "" {
		response, err = g.httpClient.SearchTicketsWithTenant(ctx, searchRequest, tenantID)
	} else {
		// Fallback to random tenant if no specific tenant found
		response, err = g.httpClient.SearchTickets(ctx, searchRequest)
	}

	duration := time.Since(startTime)

	success := err == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300
	g.metrics.RecordSearchRequest(duration, success)

	if err != nil {
		g.logger.Error(fmt.Sprintf("Search tickets failed: %v", err))
		return
	}

	if !success {
		g.logger.Error(fmt.Sprintf("Search tickets failed with status %d: %s", response.StatusCode, response.Error))
		return
	}

	// Count results and extract database latency
	resultCount := g.countSearchResults(response.Body)
	dbLatency := g.extractDatabaseLatency(response.Body)

	if tenantID != "" {
		g.logger.Info(fmt.Sprintf("Search completed for tenant %s (total: %.2fms, db: %.2fms) - %d results",
			tenantID, float64(duration.Nanoseconds())/1000000, dbLatency, resultCount))
	} else {
		g.logger.Info(fmt.Sprintf("Search completed (total: %.2fms, db: %.2fms) - %d results",
			float64(duration.Nanoseconds())/1000000, dbLatency, resultCount))
	}
}

// getTicket retrieves a specific ticket
func (g *Generator) getTicket(ctx context.Context) {
	startTime := time.Now()

	// Get ticket ID and tenant to retrieve
	ticketID, tenantID := g.getRandomTicketID()
	if ticketID == "" {
		g.logger.Warn("No ticket IDs available for get operation")
		g.metrics.RecordGetRequest(time.Since(startTime), false)
		return
	}

	// Make API request with correct tenant
	response, err := g.httpClient.GetTicketWithTenant(ctx, ticketID, tenantID, g.config.Operations.Get.ProjectedFields)
	duration := time.Since(startTime)

	success := err == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300
	g.metrics.RecordGetRequest(duration, success)

	if err != nil {
		g.logger.Error(fmt.Sprintf("Get ticket %s from tenant %s failed: %v", ticketID, tenantID, err))
		return
	}

	if !success {
		g.logger.Error(fmt.Sprintf("Get ticket %s from tenant %s failed with status %d: %s", ticketID, tenantID, response.StatusCode, response.Error))
		return
	}

	// Extract database latency from response
	dbLatency := g.extractDatabaseLatency(response.Body)
	g.logger.Info(fmt.Sprintf("Retrieved ticket %s from tenant %s (total: %.2fms, db: %.2fms)",
		ticketID, tenantID, float64(duration.Nanoseconds())/1000000, dbLatency))
}

// updateTicket updates a specific ticket
func (g *Generator) updateTicket(ctx context.Context) {
	startTime := time.Now()

	// Get ticket ID and tenant to update
	ticketID, tenantID := g.getRandomTicketID()
	if ticketID == "" {
		g.logger.Warn("No ticket IDs available for update operation")
		g.metrics.RecordUpdateRequest(time.Since(startTime), false)
		return
	}

	// Generate update data
	updateData := g.generateUpdateData()

	// Make API request with correct tenant
	response, err := g.httpClient.UpdateTicketWithTenant(ctx, ticketID, tenantID, updateData)
	duration := time.Since(startTime)

	success := err == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300
	g.metrics.RecordUpdateRequest(duration, success)

	if err != nil {
		g.logger.Error(fmt.Sprintf("Update ticket %s in tenant %s failed: %v", ticketID, tenantID, err))
		return
	}

	if !success {
		g.logger.Error(fmt.Sprintf("Update ticket %s in tenant %s failed with status %d: %s", ticketID, tenantID, response.StatusCode, response.Error))
		return
	}

	// Extract database latency from response
	dbLatency := g.extractDatabaseLatency(response.Body)
	g.logger.Info(fmt.Sprintf("Updated ticket %s in tenant %s (total: %.2fms, db: %.2fms)",
		ticketID, tenantID, float64(duration.Nanoseconds())/1000000, dbLatency))
}

// Helper methods

// processCreateData processes ticket data for creation
func (g *Generator) processCreateData(fields map[string]interface{}) map[string]interface{} {
	processed := make(map[string]interface{})

	// Copy fields excluding the ones in exclude list
	for key, value := range fields {
		excluded := false
		for _, excludeField := range g.config.Operations.Create.ExcludeFields {
			if key == excludeField {
				excluded = true
				break
			}
		}

		if !excluded {
			// Replace empty values with dummy values to avoid DynamoDB GSI errors
			cleanedValue := g.cleanFieldValue(key, value)
			processed[key] = cleanedValue
		}
	}

	// Add 20 additional custom fields from the business scenarios
	additionalFields := g.generateAdditionalBusinessFields()
	for key, value := range additionalFields {
		processed[key] = value
	}

	// Add default values for required fields if missing
	for _, requiredField := range g.config.Operations.Create.RequiredFields {
		if _, exists := processed[requiredField]; !exists {
			processed[requiredField] = g.config.Operations.Create.DefaultFieldValue
		}
	}

	return processed
}

// cleanFieldValue ensures no empty string values are sent for GSI-indexed fields
func (g *Generator) cleanFieldValue(fieldName string, value interface{}) interface{} {
	// Convert value to string to check if it's empty
	strValue, isString := value.(string)

	// If it's an empty string, replace with appropriate dummy value
	if isString && strValue == "" {
		return g.getDummyValueForField(fieldName)
	}

	// For other types, check for nil or zero values
	if value == nil {
		return g.getDummyValueForField(fieldName)
	}

	return value
}

// getDummyValueForField returns appropriate dummy values for different field types
func (g *Generator) getDummyValueForField(fieldName string) interface{} {
	// Determine field type based on field name patterns
	fieldNameLower := strings.ToLower(fieldName)

	// ID fields - use 0 as dummy value
	if strings.HasSuffix(fieldNameLower, "id") {
		return int64(0)
	}

	// Time fields - use current timestamp or 0
	if strings.Contains(fieldNameLower, "time") ||
		strings.Contains(fieldNameLower, "due") ||
		strings.Contains(fieldNameLower, "date") {
		return int64(0)
	}

	// Duration fields - use 0
	if strings.Contains(fieldNameLower, "duration") {
		return int64(0)
	}

	// Level fields - use 0
	if strings.Contains(fieldNameLower, "level") {
		return int64(0)
	}

	// Boolean-like fields
	if strings.Contains(fieldNameLower, "removed") ||
		strings.Contains(fieldNameLower, "spam") ||
		strings.Contains(fieldNameLower, "vip") ||
		strings.Contains(fieldNameLower, "violated") ||
		strings.Contains(fieldNameLower, "reopened") ||
		strings.Contains(fieldNameLower, "migrated") {
		return false
	}

	// Text fields - use meaningful dummy values
	switch fieldNameLower {
	case "name":
		return "DUMMY-TICKET-" + g.generateRandomString(6)
	case "subject":
		return "Dummy Subject - " + g.generateRandomString(8)
	case "description":
		return "Dummy description for testing purposes"
	case "oobtype":
		return "DUMMY"
	case "callfrom":
		return "SIMULATOR"
	case "emailreadconfigemail":
		return "dummy@simulator.test"
	default:
		// For unknown string fields, use a generic dummy value
		if strings.Contains(fieldNameLower, "email") {
			return "dummy@simulator.test"
		}
		if strings.Contains(fieldNameLower, "config") {
			return "DUMMY_CONFIG"
		}
		// Default to "DUMMY" for string fields, 0 for numeric fields
		return "DUMMY"
	}
}

// generateRandomString generates a random alphanumeric string of specified length
func (g *Generator) generateRandomString(length int) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[g.rand.Intn(len(charset))]
	}
	return string(result)
}

// generateAdditionalBusinessFields generates 20 additional fields from business scenarios
func (g *Generator) generateAdditionalBusinessFields() map[string]interface{} {
	// Define all possible business fields with their data generators
	allPossibleFields := map[string]func() interface{}{
		// Customer fields
		"customer_id": func() interface{} {
			customerIDs := []string{"CUST-456789", "CUST-123456", "CUST-789012", "CUST-345678", "CUST-567890", "CUST-234567"}
			return customerIDs[g.rand.Intn(len(customerIDs))]
		},
		"name": func() interface{} {
			names := []string{"Sarah Johnson", "Michael Chen", "Robert Wilson", "Jane Elizabeth Doe", "David Smith", "Emily Brown", "Alex Garcia", "Maria Rodriguez"}
			return names[g.rand.Intn(len(names))]
		},
		"email": func() interface{} {
			emails := []string{"sarah.j@gmail.com", "michael.c@company.com", "robert.w@hospital.org", "jane.doe@company.com", "david.smith@tech.com", "emily.brown@startup.io"}
			return emails[g.rand.Intn(len(emails))]
		},
		"phone": func() interface{} {
			phones := []string{"+1-555-0123", "+1-555-0456", "+1-555-0789", "+1-555-0234", "+1-555-0567"}
			return phones[g.rand.Intn(len(phones))]
		},
		"loyalty_status": func() interface{} {
			statuses := []string{"gold", "silver", "bronze", "platinum", "diamond"}
			return statuses[g.rand.Intn(len(statuses))]
		},
		"lifetime_value": func() interface{} {
			return g.rand.Intn(50000) + 5000 // 5000-55000
		},
		"account_type": func() interface{} {
			types := []string{"premium", "standard", "basic", "enterprise", "trial"}
			return types[g.rand.Intn(len(types))]
		},
		"subscription_tier": func() interface{} {
			tiers := []string{"free", "pro", "business", "enterprise", "custom"}
			return tiers[g.rand.Intn(len(tiers))]
		},

		// Order/Financial fields
		"order_id": func() interface{} {
			orderIDs := []string{"ORD-2025-789456", "ORD-2025-123789", "ORD-2025-456123", "ORD-2025-789012", "ORD-2025-345678"}
			return orderIDs[g.rand.Intn(len(orderIDs))]
		},
		"total_amount": func() interface{} {
			return float64(g.rand.Intn(500000))/100 + 10.0 // 10.00-5010.00
		},
		"currency": func() interface{} {
			currencies := []string{"USD", "EUR", "GBP", "CAD", "JPY", "AUD"}
			return currencies[g.rand.Intn(len(currencies))]
		},
		"payment_method": func() interface{} {
			methods := []string{"credit_card", "debit_card", "paypal", "bank_transfer", "cryptocurrency", "wire_transfer"}
			return methods[g.rand.Intn(len(methods))]
		},
		"invoice_number": func() interface{} {
			invoices := []string{"INV-2025-001", "INV-2025-002", "INV-2025-003", "INV-2025-004"}
			return invoices[g.rand.Intn(len(invoices))]
		},
		"tax_amount": func() interface{} {
			return float64(g.rand.Intn(10000))/100 + 1.0 // 1.00-101.00
		},
		"discount_applied": func() interface{} {
			return float64(g.rand.Intn(5000)) / 100 // 0.00-50.00
		},

		// Infrastructure/Alert fields
		"alert_id": func() interface{} {
			alertIDs := []string{"ALT-2025-001234", "ALT-2025-005678", "ALT-2025-009012", "ALT-2025-003456", "ALT-2025-007890"}
			return alertIDs[g.rand.Intn(len(alertIDs))]
		},
		"severity": func() interface{} {
			severities := []string{"critical", "high", "medium", "low", "info"}
			return severities[g.rand.Intn(len(severities))]
		},
		"hostname": func() interface{} {
			hostnames := []string{"db-prod-01", "web-prod-02", "api-prod-03", "cache-prod-04", "worker-prod-05", "lb-prod-06"}
			return hostnames[g.rand.Intn(len(hostnames))]
		},
		"ip_address": func() interface{} {
			ips := []string{"192.168.1.100", "10.0.0.50", "172.16.0.25", "192.168.100.200"}
			return ips[g.rand.Intn(len(ips))]
		},
		"region": func() interface{} {
			regions := []string{"us-east-1", "us-west-2", "eu-west-1", "ap-southeast-1", "eu-central-1", "ap-northeast-1"}
			return regions[g.rand.Intn(len(regions))]
		},
		"environment": func() interface{} {
			environments := []string{"production", "staging", "development", "testing", "qa", "demo"}
			return environments[g.rand.Intn(len(environments))]
		},
		"cpu_usage_percent": func() interface{} {
			return float64(g.rand.Intn(10000)) / 100 // 0.00-100.00
		},
		"memory_usage_percent": func() interface{} {
			return float64(g.rand.Intn(10000)) / 100 // 0.00-100.00
		},
		"disk_usage_percent": func() interface{} {
			return float64(g.rand.Intn(10000)) / 100 // 0.00-100.00
		},
		"affected_users_estimate": func() interface{} {
			return g.rand.Intn(100000) + 1000 // 1000-101000
		},
		"service_name": func() interface{} {
			services := []string{"user-service", "payment-service", "notification-service", "auth-service", "analytics-service"}
			return services[g.rand.Intn(len(services))]
		},
		"error_code": func() interface{} {
			codes := []string{"E001", "E002", "E003", "E404", "E500", "E503"}
			return codes[g.rand.Intn(len(codes))]
		},

		// Manufacturing/Quality fields
		"product_sku": func() interface{} {
			skus := []string{"WIDGET-X500", "GADGET-A200", "DEVICE-Z100", "COMPONENT-B300", "TOOL-Y400", "PART-C150"}
			return skus[g.rand.Intn(len(skus))]
		},
		"facility": func() interface{} {
			facilities := []string{"Factory-Detroit", "Plant-Austin", "Facility-Phoenix", "Manufacturing-Denver", "Assembly-Portland", "Production-Seattle"}
			return facilities[g.rand.Intn(len(facilities))]
		},
		"quantity_produced": func() interface{} {
			return g.rand.Intn(50000) + 1000 // 1000-51000
		},
		"failure_rate_percent": func() interface{} {
			return g.rand.Intn(50) + 1 // 1-50
		},
		"batch_number": func() interface{} {
			batches := []string{"BATCH-2025-001", "BATCH-2025-002", "BATCH-2025-003", "BATCH-2025-004"}
			return batches[g.rand.Intn(len(batches))]
		},
		"quality_score": func() interface{} {
			return float64(g.rand.Intn(10000)) / 100 // 0.00-100.00
		},
		"production_line": func() interface{} {
			lines := []string{"LINE-A", "LINE-B", "LINE-C", "LINE-D", "LINE-E"}
			return lines[g.rand.Intn(len(lines))]
		},
		"shift": func() interface{} {
			shifts := []string{"morning", "afternoon", "night", "weekend"}
			return shifts[g.rand.Intn(len(shifts))]
		},
		"operator_id": func() interface{} {
			operators := []string{"OP-001", "OP-002", "OP-003", "OP-004", "OP-005"}
			return operators[g.rand.Intn(len(operators))]
		},
		"maintenance_type": func() interface{} {
			types := []string{"preventive", "corrective", "emergency", "scheduled", "predictive"}
			return types[g.rand.Intn(len(types))]
		},

		// Additional Business fields
		"project_id": func() interface{} {
			projects := []string{"PROJ-2025-100", "PROJ-2025-200", "PROJ-2025-300", "PROJ-2025-400"}
			return projects[g.rand.Intn(len(projects))]
		},
		"contract_number": func() interface{} {
			contracts := []string{"CONTRACT-2025-A", "CONTRACT-2025-B", "CONTRACT-2025-C", "CONTRACT-2025-D"}
			return contracts[g.rand.Intn(len(contracts))]
		},
		"vendor_name": func() interface{} {
			vendors := []string{"TechCorp", "InnovateInc", "GlobalSolutions", "SmartSystems", "DataWorks"}
			return vendors[g.rand.Intn(len(vendors))]
		},
		"budget_allocated": func() interface{} {
			return float64(g.rand.Intn(1000000))/100 + 1000.0 // 1000.00-11000.00
		},
		"risk_level": func() interface{} {
			levels := []string{"low", "medium", "high", "critical", "negligible"}
			return levels[g.rand.Intn(len(levels))]
		},
		"compliance_status": func() interface{} {
			statuses := []string{"compliant", "non-compliant", "under-review", "pending-approval"}
			return statuses[g.rand.Intn(len(statuses))]
		},
		"asset_tag": func() interface{} {
			tags := []string{"ASSET-001", "ASSET-002", "ASSET-003", "ASSET-004", "ASSET-005"}
			return tags[g.rand.Intn(len(tags))]
		},
		"location_code": func() interface{} {
			codes := []string{"NYC-001", "LAX-002", "CHI-003", "MIA-004", "SEA-005"}
			return codes[g.rand.Intn(len(codes))]
		},
	}

	// Get all field names and randomly select 20
	allFieldNames := make([]string, 0, len(allPossibleFields))
	for fieldName := range allPossibleFields {
		allFieldNames = append(allFieldNames, fieldName)
	}

	// Shuffle and select 20 fields
	g.rand.Shuffle(len(allFieldNames), func(i, j int) {
		allFieldNames[i], allFieldNames[j] = allFieldNames[j], allFieldNames[i]
	})

	selectedFields := allFieldNames[:20] // Take first 20 after shuffle

	// Generate values for selected fields
	fields := make(map[string]interface{})
	for _, fieldName := range selectedFields {
		if generator, exists := allPossibleFields[fieldName]; exists {
			fields[fieldName] = generator()
		}
	}

	return fields
}

// getAllPossibleBusinessFieldNames returns all possible business field names
func (g *Generator) getAllPossibleBusinessFieldNames() []string {
	return []string{
		// Customer fields
		"customer_id", "name", "email", "phone", "loyalty_status", "lifetime_value", "account_type", "subscription_tier",
		// Order/Financial fields
		"order_id", "total_amount", "currency", "payment_method", "invoice_number", "tax_amount", "discount_applied",
		// Infrastructure/Alert fields
		"alert_id", "severity", "hostname", "ip_address", "region", "environment", "cpu_usage_percent",
		"memory_usage_percent", "disk_usage_percent", "affected_users_estimate", "service_name", "error_code",
		// Manufacturing/Quality fields
		"product_sku", "facility", "quantity_produced", "failure_rate_percent", "batch_number", "quality_score",
		"production_line", "shift", "operator_id", "maintenance_type",
		// Additional Business fields
		"project_id", "contract_number", "vendor_name", "budget_allocated", "risk_level", "compliance_status",
		"asset_tag", "location_code",
	}
}

// generateSearchRequest generates a random search request using collected values from the 19 GSI fields only
func (g *Generator) generateSearchRequest() (httpclient.SearchRequest, string) {
	// Generate random search conditions using collected values from inserted tickets (19 GSI fields only)
	conditions, tenantID := g.generateRandomSearchConditions()

	// Generate random projection fields
	projectedFields := g.generateRandomProjectionFields()

	return httpclient.SearchRequest{
		Conditions:      conditions,
		ProjectedFields: projectedFields,
	}, tenantID
}

// generateRandomSearchConditions generates 1-3 random search conditions using CSV values from the indexed fields only
func (g *Generator) generateRandomSearchConditions() ([]httpclient.SearchCondition, string) {
	// Get the indexed fields (these are the ONLY fields we can search on efficiently)
	businessCriticalFields := g.getBusinessCriticalFields()

	// Check which of these indexed fields have values available in CSV data
	availableFields := g.getAvailableSearchableFieldsFromCSV(businessCriticalFields)

	if len(availableFields) == 0 {
		//g.logger.Warn("No searchable values available for the supported indexed fields in CSV data.")
		//g.logger.Debug(fmt.Sprintf("Supported indexed fields: %v", businessCriticalFields))
		return []httpclient.SearchCondition{}, ""
	}

	// Generate 1-3 random conditions using CSV values from the indexed fields only
	numConditions := g.rand.Intn(5) + 1
	var conditions []httpclient.SearchCondition
	usedFields := make(map[string]bool)

	// Select a random tenant ID from configured tenants for the search
	var searchTenantID string
	if tenantIDs := g.httpClient.GetTenantIDs(); len(tenantIDs) > 0 {
		tenantIndex := g.rand.Intn(len(tenantIDs))
		searchTenantID = tenantIDs[tenantIndex]
	} else {
		searchTenantID = "default-tenant"
	}

	for i := 0; i < numConditions && len(usedFields) < len(availableFields); i++ {
		// Select random field that hasn't been used (only from the indexed fields that have CSV values)
		var selectedField string
		attempts := 0
		for attempts < 10 {
			field := availableFields[g.rand.Intn(len(availableFields))]
			if !usedFields[field] {
				selectedField = field
				usedFields[field] = true
				break
			}
			attempts++
		}

		if selectedField == "" {
			continue
		}

		// Generate condition for this field using CSV values
		condition := g.generateConditionForField(selectedField)
		if condition != nil {
			conditions = append(conditions, *condition)
		}
	}

	return conditions, searchTenantID
}

// selectRandomCondition selects a random search condition based on weights
func (g *Generator) selectRandomCondition(conditions []config.SearchCondition) config.SearchCondition {
	if len(conditions) == 1 {
		return conditions[0]
	}

	// Calculate total weight
	totalWeight := 0.0
	for _, condition := range conditions {
		totalWeight += condition.Weight
	}

	// Select random value
	randomValue := g.rand.Float64() * totalWeight

	// Find the condition
	currentWeight := 0.0
	for _, condition := range conditions {
		currentWeight += condition.Weight
		if randomValue <= currentWeight {
			return condition
		}
	}

	// Fallback to first condition
	return conditions[0]
}

// randomizeSearchValue randomizes a search value based on CSV data
func (g *Generator) randomizeSearchValue(condition config.SearchCondition) interface{} {
	// Try to get random value from CSV for this field
	if randomValue, err := g.csvReader.GetRandomFieldValue(condition.Field); err == nil {
		return randomValue
	}

	// Fallback to original value
	return condition.Value
}

// generateUpdateData generates random update data
func (g *Generator) generateUpdateData() map[string]interface{} {
	updateData := make(map[string]interface{})

	for _, field := range g.config.Operations.Update.UpdateFields {
		if template, exists := g.config.Operations.Update.UpdateTemplates[field]; exists {
			var value interface{}
			if g.config.Operations.Update.RandomizeValues {
				value = g.selectRandomTemplateValue(template)
			} else {
				value = template
			}

			// Clean the value to avoid empty strings for GSI fields
			cleanedValue := g.cleanFieldValue(field, value)
			updateData[field] = cleanedValue
		}
	}

	// Add subset of business fields to update operations (randomly select some fields to update)
	// Get all possible business field names and randomly select 3-8 for this update
	allBusinessFields := g.getAllPossibleBusinessFieldNames()
	numFieldsToUpdate := 3 + g.rand.Intn(6) // 3-8 fields

	// Shuffle and select fields for update
	g.rand.Shuffle(len(allBusinessFields), func(i, j int) {
		allBusinessFields[i], allBusinessFields[j] = allBusinessFields[j], allBusinessFields[i]
	})

	selectedFields := allBusinessFields[:numFieldsToUpdate]

	// Generate fresh business field values for the selected fields
	businessFields := g.generateAdditionalBusinessFields()

	// Add selected fields to update data if they were generated
	for _, fieldName := range selectedFields {
		if value, exists := businessFields[fieldName]; exists {
			updateData[fieldName] = value
		}
	}

	return updateData
}

// selectRandomTemplateValue selects a random value from a template
func (g *Generator) selectRandomTemplateValue(template interface{}) interface{} {
	switch t := template.(type) {
	case []string:
		if len(t) > 0 {
			return t[g.rand.Intn(len(t))]
		}
	case []int:
		if len(t) > 0 {
			return t[g.rand.Intn(len(t))]
		}
	case []interface{}:
		if len(t) > 0 {
			return t[g.rand.Intn(len(t))]
		}
	}
	return template
}

// extractTicketID extracts ticket ID from API response
func (g *Generator) extractTicketID(responseBody map[string]interface{}) string {
	// Try different possible field names for ticket ID
	possibleFields := []string{"id", "ticket_id", "ticketId", "Id"}

	for _, field := range possibleFields {
		if value, exists := responseBody[field]; exists {
			if strValue, ok := value.(string); ok {
				return strValue
			}
		}
	}

	// Try to extract from nested data
	if data, exists := responseBody["data"]; exists {
		if dataMap, ok := data.(map[string]interface{}); ok {
			for _, field := range possibleFields {
				if value, exists := dataMap[field]; exists {
					if strValue, ok := value.(string); ok {
						return strValue
					}
				}
			}
		}
	}

	return ""
}

// addCreatedTicketID adds a ticket ID to the list of created tickets for a specific tenant
func (g *Generator) addCreatedTicketID(tenantID, ticketID string) {
	g.ticketIDsMu.Lock()
	defer g.ticketIDsMu.Unlock()

	// Initialize tenant slice if it doesn't exist
	if g.createdTickets[tenantID] == nil {
		g.createdTickets[tenantID] = make([]string, 0)
	}

	g.createdTickets[tenantID] = append(g.createdTickets[tenantID], ticketID)

	// Keep only the last 1000 ticket IDs per tenant to prevent memory growth
	if len(g.createdTickets[tenantID]) > 1000 {
		g.createdTickets[tenantID] = g.createdTickets[tenantID][len(g.createdTickets[tenantID])-1000:]
	}
}

// getRandomTicketID returns a random ticket ID and its associated tenant for get/update operations
func (g *Generator) getRandomTicketID() (string, string) {
	g.ticketIDsMu.RLock()
	defer g.ticketIDsMu.RUnlock()

	// Use created ticket IDs if available and configured
	if g.config.Operations.Get.UseCreatedIDs {
		// Collect all ticket IDs from all tenants
		var allTickets []struct {
			tenantID string
			ticketID string
		}

		for tenantID, ticketIDs := range g.createdTickets {
			for _, ticketID := range ticketIDs {
				allTickets = append(allTickets, struct {
					tenantID string
					ticketID string
				}{tenantID, ticketID})
			}
		}

		if len(allTickets) > 0 {
			randomIndex := g.rand.Intn(len(allTickets))
			selected := allTickets[randomIndex]
			return selected.ticketID, selected.tenantID
		}
	}

	// Fallback to configured random ticket IDs (use random tenant)
	if len(g.config.Operations.Get.RandomTicketIDs) > 0 {
		randomIndex := g.rand.Intn(len(g.config.Operations.Get.RandomTicketIDs))
		ticketID := g.config.Operations.Get.RandomTicketIDs[randomIndex]

		// Use a random tenant from the configured list
		tenantID := "default-tenant"
		if len(g.httpClient.GetTenantIDs()) > 0 {
			tenantIndex := g.rand.Intn(len(g.httpClient.GetTenantIDs()))
			tenantID = g.httpClient.GetTenantIDs()[tenantIndex]
		}

		return ticketID, tenantID
	}

	return "", ""
}

// countSearchResults counts the number of results in a search response
func (g *Generator) countSearchResults(responseBody map[string]interface{}) int {
	// Try to find results in different possible locations
	if results, exists := responseBody["result"]; exists {
		if resultsArray, ok := results.([]interface{}); ok {
			return len(resultsArray)
		}
	}

	return 0
}

// getSearchableFieldsFromCSV returns searchable fields that exist in the CSV data
func (g *Generator) getSearchableFieldsFromCSV() []string {
	csvHeaders := g.csvReader.GetHeaders()
	headerSet := make(map[string]bool)
	for _, header := range csvHeaders {
		headerSet[header] = true
	}

	var availableFields []string
	// Get all searchable fields from config
	allSearchableFields := []string{
		"id", "createdbyid", "name", "oobtype", "removed", "removedbyid",
		"approvalstatus", "approvaltype", "categoryid", "departmentid",
		"dueby", "duetimemanuallyupdated", "firstresponsetime", "groupid",
		"impactid", "lastclosedtime", "lastopenedtime", "lastresolvedtime",
		"lastviolationtime", "locationid", "olddueby", "oldresponsedue",
		"priorityid", "reopened", "requesterid", "resolutionduelevel",
		"resolutionescalationtime", "responsedue", "responseduelevel",
		"responsedueviolated", "responseescalationtime", "slaviolated",
		"statuschangedtime", "statusid", "subject", "supportlevel",
		"technicianid", "templateid", "totalonholdduration",
		"totalresolutiontime", "totalslapausetime", "totalworkingtime",
		"urgencyid", "violatedslaid", "callfrom", "emailreadconfigemail",
		"emailreadconfigid", "purchaserequest", "requesttype",
		"servicecatalogid", "sourceid", "spam", "viprequest",
		"groupchangedtime", "lastolaviolationtime", "oladueby",
		"olaviolated", "oldoladueby", "askfeedbackdate", "firstfeedbackdate",
		"oladuelevel", "olaescalationtime", "suggestedcategoryid",
		"suggestedgroupid", "companyid", "closedby", "resolvedby",
		"vendorid", "lastucviolationtime", "olducdueby", "totaluconholdduration",
		"totalucpausetime", "totalucworkingtime", "ucdueby", "ucduelevel",
		"ucescalationtime", "ucviolated", "violateducid",
		"totalucresolutiontime", "transitionmodelid", "migrated",
		"mergedrequest", "messengerconfigid", "lastapproveddate",
	}

	for _, field := range allSearchableFields {
		if headerSet[field] {
			availableFields = append(availableFields, field)
		}
	}

	return availableFields
}

// generateConditionForField generates a search condition for a specific field using CSV data
func (g *Generator) generateConditionForField(field string) *httpclient.SearchCondition {
	// Check if this is a business field we added
	businessFields := g.getAllPossibleBusinessFieldNames()

	isBusinessField := false
	for _, bf := range businessFields {
		if bf == field {
			isBusinessField = true
			break
		}
	}

	var cleanedValue interface{}

	if isBusinessField {
		// Generate business field values directly
		businessFieldsMap := g.generateAdditionalBusinessFields()
		if value, exists := businessFieldsMap[field]; exists {
			cleanedValue = value
			// For string values in business fields, format with quotes as requested
			if strVal, ok := value.(string); ok {
				cleanedValue = fmt.Sprintf("\"%s\"", strVal)
			}
		} else {
			return nil
		}
	} else {
		// Get random value from CSV for this field
		value, err := g.csvReader.GetRandomFieldValue(field)
		if err != nil {
			return nil
		}

		// Clean the value to avoid empty strings for GSI fields
		cleanedValue = g.cleanFieldValue(field, value)

		// Skip if the cleaned value is still problematic
		if cleanedValue == nil {
			return nil
		}

		// For string values, skip if they're still empty after cleaning
		if strVal, ok := cleanedValue.(string); ok && strVal == "" {
			return nil
		}
	}

	// Determine operator based on field type and value
	operator := g.selectOperatorForValue(cleanedValue)

	return &httpclient.SearchCondition{
		Field:    field,
		Operator: operator,
		Value:    cleanedValue,
	}
}

// selectOperatorForValue selects an appropriate operator based on the value type
// Only uses operators supported by the 19 GSI fields: eq, ne, lt, lte, gt, gte
func (g *Generator) selectOperatorForValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		if v == "" {
			return "eq"
		}
		// For strings, only use operators supported by GSI fields (no "contains" or "begins_with")
		operators := []string{"eq", "ne"}
		return operators[g.rand.Intn(len(operators))]
	case int64, int, float64:
		// For numbers, use comparison operators supported by GSI fields
		operators := []string{"eq", "ne", "gt", "lt", "gte", "lte"}
		return operators[g.rand.Intn(len(operators))]
	case bool:
		// For booleans, only use equality operators
		operators := []string{"eq", "ne"}
		return operators[g.rand.Intn(len(operators))]
	default:
		return "eq"
	}
}

// generateRandomProjectionFields generates random projection fields from CSV headers
func (g *Generator) generateRandomProjectionFields() []string {
	csvHeaders := g.csvReader.GetHeaders()
	if len(csvHeaders) == 0 {
		return g.config.Operations.Search.ProjectedFields
	}

	// Generate 3-8 random projection fields
	numFields := g.rand.Intn(6) + 3 // 3 to 8 fields
	if numFields > len(csvHeaders) {
		numFields = len(csvHeaders)
	}

	// Always include ticket ID if available
	var projectedFields []string
	idFields := []string{"id", "ticket_id", "ticketId"}
	for _, idField := range idFields {
		for _, header := range csvHeaders {
			if header == idField {
				projectedFields = append(projectedFields, header)
				numFields-- // Reduce count since we added ID
				break
			}
		}
		if len(projectedFields) > 0 {
			break
		}
	}

	// Add random fields
	usedFields := make(map[string]bool)
	for _, field := range projectedFields {
		usedFields[field] = true
	}

	for len(projectedFields) < numFields && len(usedFields) < len(csvHeaders) {
		randomField := csvHeaders[g.rand.Intn(len(csvHeaders))]
		if !usedFields[randomField] {
			projectedFields = append(projectedFields, randomField)
			usedFields[randomField] = true
		}
	}

	return projectedFields
}

// extractDatabaseLatency extracts the database_latency_ms from API response
func (g *Generator) extractDatabaseLatency(responseBody map[string]interface{}) float64 {
	if responseBody == nil {
		return 0
	}

	// Try to extract database_latency_ms from response
	if latency, exists := responseBody["database_latency_ms"]; exists {
		switch v := latency.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case string:
			if parsed, err := strconv.ParseFloat(v, 64); err == nil {
				return parsed
			}
		}
	}

	return 0
}

// getMapKeys returns the keys of a map[string]interface{} as a slice of strings
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// collectSearchableValues collects values from searchable fields during ticket creation
// Only collects values for the supported indexed fields to prevent unsupported field searches
func (g *Generator) collectSearchableValues(tenantID string, ticketData map[string]interface{}) {
	g.searchValuesMu.Lock()
	defer g.searchValuesMu.Unlock()

	// Use the same fields as defined in getBusinessCriticalFields() for consistency
	searchableFields := g.getBusinessCriticalFields()

	// Extract fields from ticket data - check if wrapped in "fields" or direct
	var fields map[string]interface{}
	if fieldsWrapper, ok := ticketData["fields"].(map[string]interface{}); ok {
		// Data is wrapped in "fields" object
		fields = fieldsWrapper
		//g.logger.Debug(fmt.Sprintf("Using wrapped fields for tenant %s. Available fields: %v", tenantID, getMapKeys(fields)))
	} else {
		// Data is directly at top level (CSV format)
		fields = ticketData
	}

	// Collect values for each searchable field (all supported indexed fields)
	collectedCount := 0
	skippedCount := 0

	for _, fieldName := range searchableFields {
		if value, exists := fields[fieldName]; exists && value != nil {
			// Convert value to string for consistent storage
			valueStr := fmt.Sprintf("%v", value)
			if valueStr == "" || valueStr == "<nil>" {
				continue
			}
			collectedCount++

			// Initialize field map if it doesn't exist
			if g.searchableValues[fieldName] == nil {
				g.searchableValues[fieldName] = make(map[string][]string)
			}

			// Initialize tenant slice if it doesn't exist
			if g.searchableValues[fieldName][tenantID] == nil {
				g.searchableValues[fieldName][tenantID] = make([]string, 0)
			}

			// Add value if it's not already present (avoid duplicates)
			values := g.searchableValues[fieldName][tenantID]
			found := false
			for _, existingValue := range values {
				if existingValue == valueStr {
					found = true
					break
				}
			}

			if !found {
				g.searchableValues[fieldName][tenantID] = append(values, valueStr)

				// Keep only the last 100 unique values per field per tenant to prevent memory growth
				if len(g.searchableValues[fieldName][tenantID]) > 100 {
					g.searchableValues[fieldName][tenantID] = g.searchableValues[fieldName][tenantID][len(g.searchableValues[fieldName][tenantID])-100:]
				}
			}
		} else {
			// Field exists in CSV but not in our supported list
			if _, exists := fields[fieldName]; !exists {
				skippedCount++
			}
		}
	}

	// Log collection summary
	if collectedCount > 0 {
		/*g.logger.Debug(fmt.Sprintf("Collected searchable values for tenant %s: %d fields collected, %d fields skipped (only indexed fields supported)",
		tenantID, collectedCount, skippedCount))*/
	}
}

// getRandomSearchableValue returns a random value for a searchable field from collected data
func (g *Generator) getRandomSearchableValue(fieldName string) (string, string) {
	g.searchValuesMu.RLock()
	defer g.searchValuesMu.RUnlock()

	// Check if we have collected values for this field
	fieldValues, exists := g.searchableValues[fieldName]
	if !exists || len(fieldValues) == 0 {
		return "", ""
	}

	// Collect all values from all tenants for this field
	var allValues []struct {
		tenantID string
		value    string
	}

	for tenantID, values := range fieldValues {
		for _, value := range values {
			allValues = append(allValues, struct {
				tenantID string
				value    string
			}{tenantID, value})
		}
	}

	if len(allValues) == 0 {
		return "", ""
	}

	// Return random value with its tenant
	randomIndex := g.rand.Intn(len(allValues))
	selected := allValues[randomIndex]
	return selected.value, selected.tenantID
}

// isSupportedSearchField checks if a field is in the updated supported indexed fields
func (g *Generator) isSupportedSearchField(field string) bool {
	supportedFields := map[string]bool{
		// User and assignment fields
		"updatedbyid":  true,
		"createdbyid":  true,
		"removedbyid":  true,
		"requesterid":  true,
		"technicianid": true,
		"closedby":     true,
		"resolvedby":   true,

		// Organizational and categorization fields
		"departmentid":        true,
		"groupid":             true,
		"impactid":            true,
		"locationid":          true,
		"priorityid":          true,
		"resolutionduelevel":  true,
		"responseduelevel":    true,
		"statusid":            true,
		"templateid":          true,
		"urgencyid":           true,
		"violatedslaid":       true,
		"emailreadconfigid":   true,
		"requesttype":         true,
		"servicecatalogid":    true,
		"sourceid":            true,
		"oladuelevel":         true,
		"suggestedcategoryid": true,
		"suggestedgroupid":    true,
		"companyid":           true,
		"vendorid":            true,
		"violateducid":        true,
		"transitionmodelid":   true,
		"messengerconfigid":   true,

		// Timestamp fields
		"updatedtime":            true,
		"createdtime":            true,
		"removedtime":            true,
		"lastclosedtime":         true,
		"lastopenedtime":         true,
		"lastresolvedtime":       true,
		"lastviolationtime":      true,
		"olddueby":               true,
		"oldresponsedue":         true,
		"responsedue":            true,
		"responseescalationtime": true,
		"statuschangedtime":      true,
		"groupchangedtime":       true,
		"oladueby":               true,
		"olaescalationtime":      true,
		"askfeedbackdate":        true,
		"firstfeedbackdate":      true,
		"lastucviolationtime":    true,
		"lastapproveddate":       true,
	}
	return supportedFields[field]
}

// getBusinessCriticalFields returns the updated searchable fields (matching PostgreSQL indexed columns)
func (g *Generator) getBusinessCriticalFields() []string {
	return []string{
		// User and assignment fields
		"updatedbyid", "createdbyid", "removedbyid", "requesterid", "technicianid",
		"closedby", "resolvedby",

		// Organizational and categorization fields
		"departmentid", "groupid", "impactid", "locationid", "priorityid",
		"resolutionduelevel", "responseduelevel", "statusid", "templateid",
		"urgencyid", "violatedslaid", "emailreadconfigid", "requesttype",
		"servicecatalogid", "sourceid", "oladuelevel", "suggestedcategoryid",
		"suggestedgroupid", "companyid", "vendorid", "violateducid",
		"transitionmodelid", "messengerconfigid",

		// Timestamp fields
		"updatedtime", "createdtime", "removedtime", "lastclosedtime",
		"lastopenedtime", "lastresolvedtime", "lastviolationtime", "olddueby",
		"oldresponsedue", "responsedue", "responseescalationtime",
		"statuschangedtime", "groupchangedtime", "oladueby", "olaescalationtime",
		"askfeedbackdate", "firstfeedbackdate", "lastucviolationtime",
		"lastapproveddate",

		// Business scenario fields (expanded list for better search coverage)
		"customer_id", "name", "email", "phone", "loyalty_status", "account_type",
		"order_id", "total_amount", "currency", "payment_method", "invoice_number",
		"alert_id", "severity", "hostname", "ip_address", "region", "environment",
		"service_name", "error_code", "product_sku", "facility", "batch_number",
		"production_line", "shift", "operator_id", "maintenance_type", "project_id",
		"contract_number", "vendor_name", "risk_level", "compliance_status", "asset_tag", "location_code",
	}
}

// getAvailableSearchableFields returns fields that have collected values available for searching
// Only returns fields that are in the updated supported indexed fields
func (g *Generator) getAvailableSearchableFields(businessFields []string) []string {
	g.searchValuesMu.RLock()
	defer g.searchValuesMu.RUnlock()

	// Define the updated supported fields (must match PostgreSQL indexed columns)
	supportedFields := map[string]bool{
		// User and assignment fields
		"updatedbyid":  true,
		"createdbyid":  true,
		"removedbyid":  true,
		"requesterid":  true,
		"technicianid": true,
		"closedby":     true,
		"resolvedby":   true,

		// Organizational and categorization fields
		"departmentid":        true,
		"groupid":             true,
		"impactid":            true,
		"locationid":          true,
		"priorityid":          true,
		"resolutionduelevel":  true,
		"responseduelevel":    true,
		"statusid":            true,
		"templateid":          true,
		"urgencyid":           true,
		"violatedslaid":       true,
		"emailreadconfigid":   true,
		"requesttype":         true,
		"servicecatalogid":    true,
		"sourceid":            true,
		"oladuelevel":         true,
		"suggestedcategoryid": true,
		"suggestedgroupid":    true,
		"companyid":           true,
		"vendorid":            true,
		"violateducid":        true,
		"transitionmodelid":   true,
		"messengerconfigid":   true,

		// Timestamp fields
		"updatedtime":            true,
		"createdtime":            true,
		"removedtime":            true,
		"lastclosedtime":         true,
		"lastopenedtime":         true,
		"lastresolvedtime":       true,
		"lastviolationtime":      true,
		"olddueby":               true,
		"oldresponsedue":         true,
		"responsedue":            true,
		"responseescalationtime": true,
		"statuschangedtime":      true,
		"groupchangedtime":       true,
		"oladueby":               true,
		"olaescalationtime":      true,
		"askfeedbackdate":        true,
		"firstfeedbackdate":      true,
		"lastucviolationtime":    true,
		"lastapproveddate":       true,
	}

	var availableFields []string
	for _, field := range businessFields {
		// Only consider fields that are in the supported list
		if !supportedFields[field] {
			//g.logger.Warn(fmt.Sprintf("Field '%s' is not in the supported indexed fields, skipping", field))
			continue
		}

		if fieldValues, exists := g.searchableValues[field]; exists && len(fieldValues) > 0 {
			// Check if any tenant has values for this field
			hasValues := false
			for _, tenantValues := range fieldValues {
				if len(tenantValues) > 0 {
					hasValues = true
					break
				}
			}
			if hasValues {
				availableFields = append(availableFields, field)
			}
		}
	}

	return availableFields
}

// getAvailableSearchableFieldsFromCSV returns fields that have values available in CSV data
// Only returns fields that are in the supported indexed fields and have non-empty values in CSV
func (g *Generator) getAvailableSearchableFieldsFromCSV(businessFields []string) []string {
	// Get supported fields map for quick lookup
	supportedFields := map[string]bool{
		// User and assignment fields
		"updatedbyid":  true,
		"createdbyid":  true,
		"removedbyid":  true,
		"requesterid":  true,
		"technicianid": true,
		"closedby":     true,
		"resolvedby":   true,

		// Organizational and categorization fields
		"departmentid":        true,
		"groupid":             true,
		"impactid":            true,
		"locationid":          true,
		"priorityid":          true,
		"resolutionduelevel":  true,
		"responseduelevel":    true,
		"statusid":            true,
		"templateid":          true,
		"urgencyid":           true,
		"violatedslaid":       true,
		"emailreadconfigid":   true,
		"requesttype":         true,
		"servicecatalogid":    true,
		"sourceid":            true,
		"oladuelevel":         true,
		"suggestedcategoryid": true,
		"suggestedgroupid":    true,
		"companyid":           true,
		"vendorid":            true,
		"violateducid":        true,
		"transitionmodelid":   true,
		"messengerconfigid":   true,

		// Timestamp fields
		"updatedtime":            true,
		"createdtime":            true,
		"removedtime":            true,
		"lastclosedtime":         true,
		"lastopenedtime":         true,
		"lastresolvedtime":       true,
		"lastviolationtime":      true,
		"olddueby":               true,
		"oldresponsedue":         true,
		"responsedue":            true,
		"responseescalationtime": true,
		"statuschangedtime":      true,
		"groupchangedtime":       true,
		"oladueby":               true,
		"olaescalationtime":      true,
		"askfeedbackdate":        true,
		"firstfeedbackdate":      true,
		"lastucviolationtime":    true,
		"lastapproveddate":       true,

		// Business scenario fields (expanded list)
		"customer_id":             true,
		"name":                    true,
		"email":                   true,
		"phone":                   true,
		"loyalty_status":          true,
		"lifetime_value":          true,
		"account_type":            true,
		"subscription_tier":       true,
		"order_id":                true,
		"total_amount":            true,
		"currency":                true,
		"payment_method":          true,
		"invoice_number":          true,
		"tax_amount":              true,
		"discount_applied":        true,
		"alert_id":                true,
		"severity":                true,
		"hostname":                true,
		"ip_address":              true,
		"region":                  true,
		"environment":             true,
		"cpu_usage_percent":       true,
		"memory_usage_percent":    true,
		"disk_usage_percent":      true,
		"affected_users_estimate": true,
		"service_name":            true,
		"error_code":              true,
		"product_sku":             true,
		"facility":                true,
		"quantity_produced":       true,
		"failure_rate_percent":    true,
		"batch_number":            true,
		"quality_score":           true,
		"production_line":         true,
		"shift":                   true,
		"operator_id":             true,
		"maintenance_type":        true,
		"project_id":              true,
		"contract_number":         true,
		"vendor_name":             true,
		"budget_allocated":        true,
		"risk_level":              true,
		"compliance_status":       true,
		"asset_tag":               true,
		"location_code":           true,
	}

	var availableFields []string
	businessFieldsList := []string{
		"customer_id", "name", "email", "phone", "loyalty_status", "lifetime_value", "account_type", "subscription_tier",
		"order_id", "total_amount", "currency", "payment_method", "invoice_number", "tax_amount", "discount_applied",
		"alert_id", "severity", "hostname", "ip_address", "region", "environment", "cpu_usage_percent",
		"memory_usage_percent", "disk_usage_percent", "affected_users_estimate", "service_name", "error_code",
		"product_sku", "facility", "quantity_produced", "failure_rate_percent", "batch_number", "quality_score",
		"production_line", "shift", "operator_id", "maintenance_type", "project_id", "contract_number",
		"vendor_name", "budget_allocated", "risk_level", "compliance_status", "asset_tag", "location_code",
	}

	for _, field := range businessFields {
		// Only consider fields that are in the supported list
		if !supportedFields[field] {
			continue
		}

		// Check if this is a business field (always available)
		isBusinessField := false
		for _, bf := range businessFieldsList {
			if bf == field {
				isBusinessField = true
				break
			}
		}

		if isBusinessField {
			// Business fields are always available for search
			availableFields = append(availableFields, field)
		} else {
			// Check if field has values in CSV data
			if values := g.csvReader.GetFieldValues(field); len(values) > 0 {
				// Check if field has non-empty values
				hasNonEmptyValues := false
				for _, value := range values {
					if value != nil {
						valueStr := fmt.Sprintf("%v", value)
						if valueStr != "" && valueStr != "<nil>" {
							hasNonEmptyValues = true
							break
						}
					}
				}
				if hasNonEmptyValues {
					availableFields = append(availableFields, field)
				}
			}
		}
	}

	return availableFields
}

// generateConditionFromCollectedValues generates a search condition using collected values
func (g *Generator) generateConditionFromCollectedValues(field string) (*httpclient.SearchCondition, string) {
	// Validate that the field is in the supported indexed fields
	if !g.isSupportedSearchField(field) {
		//g.logger.Warn(fmt.Sprintf("Attempted to generate condition for unsupported field '%s', skipping", field))
		return nil, ""
	}

	// Get random value from collected data
	value, tenantID := g.getRandomSearchableValue(field)
	if value == "" {
		return nil, ""
	}

	// Clean the value to avoid empty strings for GSI fields
	cleanedValue := g.cleanFieldValue(field, value)

	// Skip if the cleaned value is still problematic
	if cleanedValue == nil {
		return nil, ""
	}

	// For string values, skip if they're still empty after cleaning
	if strVal, ok := cleanedValue.(string); ok && strVal == "" {
		return nil, ""
	}

	// Determine operator based on field type and value
	operator := g.selectOperatorForValue(cleanedValue)

	/*g.logger.Debug(fmt.Sprintf("Generated search condition: field=%s, operator=%s, value=%v, tenant=%s",
	field, operator, cleanedValue, tenantID))*/

	return &httpclient.SearchCondition{
		Field:    field,
		Operator: operator,
		Value:    cleanedValue,
	}, tenantID
}
