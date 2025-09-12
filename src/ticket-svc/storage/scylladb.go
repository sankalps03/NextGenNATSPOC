package storage

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gocql/gocql"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// ScyllaDBStorage implements tenant-aware ticket storage using ScyllaDB
// Each tenant gets its own table for complete data isolation
type ScyllaDBStorage struct {
	session       *gocql.Session
	keyspace      string
	baseTableName string
	tenantTables  sync.Map     // tenant -> tableName mapping for performance
	tableMutex    sync.RWMutex // synchronizes table creation operations
}

// NewScyllaDBStorage creates a new tenant-aware ScyllaDB storage instance
func NewScyllaDBStorage(ctx context.Context, hosts []string, keyspace, baseTableName string) (*ScyllaDBStorage, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no ScyllaDB hosts provided")
	}

	log.Printf("Attempting to connect to ScyllaDB hosts: %v", hosts)

	// Helper function to create cluster configuration
	createCluster := func(withKeyspace string) *gocql.ClusterConfig {
		cluster := gocql.NewCluster(hosts...)
		cluster.Consistency = gocql.Quorum
		cluster.Timeout = 30 * time.Second
		cluster.ConnectTimeout = 15 * time.Second
		cluster.NumConns = 2
		cluster.DisableInitialHostLookup = false

		// Enable token-aware host selection for shard awareness
		// Create a new policy instance for each cluster to avoid sharing
		fallback := gocql.RoundRobinHostPolicy()
		cluster.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(fallback)

		// Add retry policy
		cluster.RetryPolicy = &gocql.SimpleRetryPolicy{NumRetries: 3}

		if withKeyspace != "" {
			cluster.Keyspace = withKeyspace
		}

		return cluster
	}

	// First, try to connect without specifying keyspace to test basic connectivity
	log.Printf("Testing basic connectivity to ScyllaDB...")
	testCluster := createCluster("")
	testSession, err := testCluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ScyllaDB hosts %v: %w\n\nTroubleshooting:\n1. Ensure ScyllaDB is running: docker run --name scylla -p 9042:9042 -d scylladb/scylla:6.1.2 --overprovisioned 1 --smp 1\n2. Wait 30-60 seconds for ScyllaDB to start\n3. Check connectivity: nc -z localhost 9042\n4. Verify hosts configuration: %v", hosts, err, hosts)
	}
	testSession.Close()
	log.Printf("✓ Basic connectivity to ScyllaDB established")

	// Now try to connect with the keyspace
	cluster := createCluster(keyspace)
	session, err := cluster.CreateSession()
	if err != nil {
		// If keyspace doesn't exist, try to create it first
		log.Printf("Failed to connect to keyspace '%s', attempting to create it...", keyspace)

		// Create a new cluster for keyspace creation
		tempCluster := createCluster("")
		tempSession, tempErr := tempCluster.CreateSession()
		if tempErr != nil {
			return nil, fmt.Errorf("failed to create temporary session for keyspace creation: %w", tempErr)
		}

		// Create keyspace
		createKeyspaceQuery := fmt.Sprintf(`
			CREATE KEYSPACE IF NOT EXISTS %s
			WITH REPLICATION = {
				'class': 'SimpleStrategy',
				'replication_factor': 1
			}`, keyspace)

		if keyspaceErr := tempSession.Query(createKeyspaceQuery).Exec(); keyspaceErr != nil {
			tempSession.Close()
			return nil, fmt.Errorf("failed to create keyspace '%s': %w", keyspace, keyspaceErr)
		}
		tempSession.Close()

		log.Printf("✓ Keyspace '%s' created successfully", keyspace)

		// Now try to connect with the keyspace using a new cluster
		cluster = createCluster(keyspace)
		session, err = cluster.CreateSession()
		if err != nil {
			return nil, fmt.Errorf("failed to create session with keyspace '%s': %w", keyspace, err)
		}
	}

	log.Printf("✓ ScyllaDB connection established successfully to keyspace: %s", keyspace)

	storage := &ScyllaDBStorage{
		session:       session,
		keyspace:      keyspace,
		baseTableName: baseTableName,
		tenantTables:  sync.Map{},
	}

	return storage, nil
}

// generateTicketID generates a unique ticket ID if not provided
func (s *ScyllaDBStorage) generateTicketID() string {
	// Generate a simple ticket ID with timestamp
	return fmt.Sprintf("TKT-%d", time.Now().UnixNano()/1000000)
}

// getTenantTableName returns the table name for a specific tenant
func (s *ScyllaDBStorage) getTenantTableName(tenantID string) string {
	// Sanitize tenant ID for use in table name
	sanitized := strings.ReplaceAll(tenantID, "-", "_")
	sanitized = strings.ReplaceAll(sanitized, ".", "_")
	return fmt.Sprintf("%s_%s", s.baseTableName, sanitized)
}

// ensureKeyspace ensures that the keyspace exists (now handled in constructor)
func (s *ScyllaDBStorage) ensureKeyspace(ctx context.Context) error {
	// Keyspace creation is now handled in NewScyllaDBStorage
	// This method is kept for compatibility but does nothing
	return nil
}

// ensureTenantTable ensures that a table exists for the given tenant
func (s *ScyllaDBStorage) ensureTenantTable(ctx context.Context, tenantID string) error {
	tableName := s.getTenantTableName(tenantID)

	// Check if we already know this table exists
	if _, exists := s.tenantTables.Load(tenantID); exists {
		return nil
	}

	s.tableMutex.Lock()
	defer s.tableMutex.Unlock()

	// Double-check after acquiring lock
	if _, exists := s.tenantTables.Load(tenantID); exists {
		return nil
	}

	// Create the table
	if err := s.createTableIfNotExists(ctx, tableName); err != nil {
		return fmt.Errorf("failed to create table for tenant %s: %w", tenantID, err)
	}

	// Create indexes
	if err := s.createIndexes(ctx, tableName); err != nil {
		return fmt.Errorf("failed to create indexes for tenant %s: %w", tenantID, err)
	}

	log.Printf("Created new ScyllaDB table for tenant %s: %s", tenantID, tableName)

	// Store in map to avoid future checks
	s.tenantTables.Store(tenantID, tableName)
	return nil
}

// createTableIfNotExists creates a new ScyllaDB table with all 49 fields
func (s *ScyllaDBStorage) createTableIfNotExists(ctx context.Context, tableName string) error {
	createTableQuery := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.%s (
			-- Primary key with auto-increment equivalent (using UUID)
			id UUID,
			
			-- Core fields for protobuf compatibility (managed by application)
			ticket_id TEXT,
			created_at TIMESTAMP,
			updated_at TIMESTAMP,
			
			-- User and assignment fields
			updatedbyid BIGINT,
			createdbyid BIGINT,
			removedbyid BIGINT,
			requesterid BIGINT,
			technicianid BIGINT,
			closedby BIGINT,
			resolvedby BIGINT,
			
			-- Timestamp fields (Unix timestamps in milliseconds)
			updatedtime BIGINT,
			createdtime BIGINT,
			removedtime BIGINT,
			dueby BIGINT,
			firstresponsetime BIGINT,
			lastclosedtime BIGINT,
			lastopenedtime BIGINT,
			lastresolvedtime BIGINT,
			lastviolationtime BIGINT,
			olddueby BIGINT,
			oldresponsedue BIGINT,
			resolutionescalationtime BIGINT,
			responsedue BIGINT,
			responseescalationtime BIGINT,
			statuschangedtime BIGINT,
			groupchangedtime BIGINT,
			lastolaviolationtime BIGINT,
			oladueby BIGINT,
			oldoladueby BIGINT,
			askfeedbackdate BIGINT,
			firstfeedbackdate BIGINT,
			olaescalationtime BIGINT,
			lastucviolationtime BIGINT,
			olducdueby BIGINT,
			ucdueby BIGINT,
			ucescalationtime BIGINT,
			lastapproveddate BIGINT,
			
			-- Text fields
			name TEXT,
			oobtype TEXT,
			description TEXT,
			originaldescription TEXT,
			subject TEXT,
			callfrom TEXT,
			emailreadconfigemail TEXT,
			
			-- Boolean fields
			removed BOOLEAN,
			duetimemanuallyupdated BOOLEAN,
			reopened BOOLEAN,
			responsedueviolated BOOLEAN,
			slaviolated BOOLEAN,
			purchaserequest BOOLEAN,
			spam BOOLEAN,
			viprequest BOOLEAN,
			olaviolated BOOLEAN,
			ucviolated BOOLEAN,
			migrated BOOLEAN,
			
			-- Category and classification fields
			categoryid BIGINT,
			departmentid BIGINT,
			groupid BIGINT,
			impactid BIGINT,
			locationid BIGINT,
			priorityid BIGINT,
			statusid BIGINT,
			urgencyid BIGINT,
			violatedslaid BIGINT,
			servicecatalogid BIGINT,
			sourceid BIGINT,
			requesttype BIGINT,
			suggestedcategoryid BIGINT,
			suggestedgroupid BIGINT,
			companyid BIGINT,
			vendorid BIGINT,
			violateducid BIGINT,
			transitionmodelid BIGINT,
			messengerconfigid BIGINT,
			
			-- Approval and workflow fields
			approvalstatus INT,
			approvaltype INT,
			resolutionduelevel INT,
			responseduelevel INT,
			supportlevel INT,
			oladuelevel INT,
			ucduelevel INT,
			
			-- Duration and time tracking fields (in milliseconds)
			totalonholdduration BIGINT,
			totalresolutiontime BIGINT,
			totalslapausetime BIGINT,
			totalworkingtime BIGINT,
			totaluconholdduration BIGINT,
			totalucpausetime BIGINT,
			totalucworkingtime BIGINT,
			totalucresolutiontime BIGINT,
			
			-- Configuration and template fields
			templateid BIGINT,
			emailreadconfigid BIGINT,
			
			PRIMARY KEY (ticket_id)
		)`, s.keyspace, tableName)

	if err := s.session.Query(createTableQuery).Exec(); err != nil {
		return fmt.Errorf("failed to create table %s: %w", tableName, err)
	}

	log.Printf("Successfully created table %s.%s", s.keyspace, tableName)
	return nil
}

// createIndexes creates secondary indexes for all 49 fields
func (s *ScyllaDBStorage) createIndexes(ctx context.Context, tableName string) error {
	// List of all fields that need indexes (excluding primary key fields)
	indexFields := []string{
		"updatedbyid", "createdbyid", "removedbyid", "requesterid", "technicianid",
		"closedby", "resolvedby", "updatedtime", "createdtime", "removedtime",
		"dueby", "firstresponsetime", "lastclosedtime", "lastopenedtime",
		"lastresolvedtime", "lastviolationtime", "olddueby", "oldresponsedue",
		"resolutionescalationtime", "responsedue", "responseescalationtime",
		"statuschangedtime", "groupchangedtime", "lastolaviolationtime",
		"oladueby", "oldoladueby", "askfeedbackdate", "firstfeedbackdate",
		"olaescalationtime", "lastucviolationtime", "olducdueby", "ucdueby",
		"ucescalationtime", "lastapproveddate", "name", "oobtype",
		"removed", "duetimemanuallyupdated", "reopened", "responsedueviolated",
		"slaviolated", "purchaserequest", "spam", "viprequest", "olaviolated",
		"ucviolated", "migrated", "categoryid", "departmentid", "groupid",
		"impactid", "locationid", "priorityid", "statusid", "urgencyid",
		"violatedslaid", "servicecatalogid", "sourceid", "requesttype",
		"suggestedcategoryid", "suggestedgroupid", "companyid", "vendorid",
		"violateducid", "transitionmodelid", "messengerconfigid",
		"approvalstatus", "approvaltype", "resolutionduelevel",
		"responseduelevel", "supportlevel", "oladuelevel", "ucduelevel",
		"totalonholdduration", "totalresolutiontime", "totalslapausetime",
		"totalworkingtime", "totaluconholdduration", "totalucpausetime",
		"totalucworkingtime", "totalucresolutiontime", "templateid",
		"emailreadconfigid",
	}

	// Create indexes for each field
	for _, field := range indexFields {
		indexName := fmt.Sprintf("idx_%s_%s", tableName, field)
		createIndexQuery := fmt.Sprintf(
			"CREATE INDEX IF NOT EXISTS %s ON %s.%s (%s)",
			indexName, s.keyspace, tableName, field,
		)

		if err := s.session.Query(createIndexQuery).Exec(); err != nil {
			log.Printf("Warning: Failed to create index %s: %v", indexName, err)
			// Continue with other indexes even if one fails
		} else {
			log.Printf("Created index: %s", indexName)
		}
	}

	return nil
}

// protobufToScyllaDBRow converts a TicketData protobuf to ScyllaDB row data
func protobufToScyllaDBRow(ticketData *ticketpb.TicketData, isUpdate bool) (map[string]interface{}, error) {
	row := make(map[string]interface{})

	// Core fields - these are managed by the application
	row["ticket_id"] = ticketData.Id

	// Handle timestamps
	if !isUpdate {
		// For new tickets, set created_at to current time
		row["created_at"] = time.Now()
	}
	// Always update updated_at for both create and update
	row["updated_at"] = time.Now()

	// Generate UUID for id field if creating new ticket
	if !isUpdate {
		row["id"] = gocql.TimeUUID()
	}

	// Process dynamic fields from protobuf
	for fieldName, fieldValue := range ticketData.Fields {
		if fieldValue == nil {
			continue
		}

		var value interface{}
		switch v := fieldValue.Value.(type) {
		case *ticketpb.FieldValue_StringValue:
			value = v.StringValue
		case *ticketpb.FieldValue_IntValue:
			value = v.IntValue
		case *ticketpb.FieldValue_DoubleValue:
			value = v.DoubleValue
		case *ticketpb.FieldValue_BoolValue:
			value = v.BoolValue
		default:
			continue // Skip unknown types
		}

		// Convert field name to lowercase for consistency
		fieldName = strings.ToLower(fieldName)

		// Map to specific columns if they exist in the schema
		switch fieldName {
		case "updatedbyid", "createdbyid", "removedbyid", "requesterid", "technicianid",
			"closedby", "resolvedby", "categoryid", "departmentid", "groupid", "impactid",
			"locationid", "priorityid", "statusid", "urgencyid", "violatedslaid",
			"servicecatalogid", "sourceid", "requesttype", "suggestedcategoryid", "suggestedgroupid",
			"companyid", "vendorid", "violateducid", "transitionmodelid", "messengerconfigid",
			"templateid", "emailreadconfigid":
			// Convert to int64 for BIGINT columns with strict type checking
			convertedVal := convertToBigInt(value)
			if convertedVal != nil {
				row[fieldName] = *convertedVal
			}

		case "updatedtime", "createdtime", "removedtime", "dueby", "firstresponsetime",
			"lastclosedtime", "lastopenedtime", "lastresolvedtime", "lastviolationtime",
			"olddueby", "oldresponsedue", "resolutionescalationtime", "responsedue",
			"responseescalationtime", "statuschangedtime", "groupchangedtime",
			"lastolaviolationtime", "oladueby", "oldoladueby", "askfeedbackdate",
			"firstfeedbackdate", "olaescalationtime", "lastucviolationtime", "olducdueby",
			"ucdueby", "ucescalationtime", "lastapproveddate", "totalonholdduration",
			"totalresolutiontime", "totalslapausetime", "totalworkingtime",
			"totaluconholdduration", "totalucpausetime", "totalucworkingtime",
			"totalucresolutiontime":
			// Convert to int64 for BIGINT timestamp columns with strict type checking
			convertedVal := convertToBigInt(value)
			if convertedVal != nil {
				row[fieldName] = *convertedVal
			}

		case "name", "oobtype", "description", "originaldescription", "subject", "callfrom", "emailreadconfigemail":
			if strVal, ok := value.(string); ok {
				row[fieldName] = strVal
			}

		case "removed", "duetimemanuallyupdated", "reopened", "responsedueviolated",
			"slaviolated", "purchaserequest", "spam", "viprequest", "olaviolated",
			"ucviolated", "migrated":
			if boolVal, ok := value.(bool); ok {
				row[fieldName] = boolVal
			} else if strVal, ok := value.(string); ok {
				row[fieldName] = strings.ToLower(strVal) == "true"
			}

		case "approvalstatus", "approvaltype", "resolutionduelevel", "responseduelevel",
			"supportlevel", "oladuelevel", "ucduelevel":
			// Convert to int32 for INT columns with strict type checking
			convertedVal := convertToInt(value)
			if convertedVal != nil {
				row[fieldName] = *convertedVal
			}
		}
	}

	return row, nil
}

// convertToBigInt safely converts various types to int64 for BIGINT columns
func convertToBigInt(value interface{}) *int64 {
	switch v := value.(type) {
	case int64:
		return &v
	case int:
		result := int64(v)
		return &result
	case int32:
		result := int64(v)
		return &result
	case float64:
		// Ensure the float64 is within int64 range and has no fractional part
		if v >= float64(math.MinInt64) && v <= float64(math.MaxInt64) && v == math.Trunc(v) {
			result := int64(v)
			return &result
		}
		log.Printf("Warning: float64 value %v cannot be safely converted to int64", v)
		return nil
	case float32:
		result := int64(v)
		return &result
	case string:
		if intVal, err := strconv.ParseInt(v, 10, 64); err == nil {
			return &intVal
		}
		log.Printf("Warning: string value '%s' cannot be converted to int64", v)
		return nil
	default:
		log.Printf("Warning: unsupported type %T for BIGINT conversion: %v", v, v)
		return nil
	}
}

// convertToInt safely converts various types to int32 for INT columns
func convertToInt(value interface{}) *int32 {
	switch v := value.(type) {
	case int32:
		return &v
	case int:
		if v >= math.MinInt32 && v <= math.MaxInt32 {
			result := int32(v)
			return &result
		}
		log.Printf("Warning: int value %v is out of int32 range", v)
		return nil
	case int64:
		if v >= math.MinInt32 && v <= math.MaxInt32 {
			result := int32(v)
			return &result
		}
		log.Printf("Warning: int64 value %v is out of int32 range", v)
		return nil
	case float64:
		if v >= float64(math.MinInt32) && v <= float64(math.MaxInt32) && v == math.Trunc(v) {
			result := int32(v)
			return &result
		}
		log.Printf("Warning: float64 value %v cannot be safely converted to int32", v)
		return nil
	case float32:
		if v >= float32(math.MinInt32) && v <= float32(math.MaxInt32) && v == float32(math.Trunc(float64(v))) {
			result := int32(v)
			return &result
		}
		log.Printf("Warning: float32 value %v cannot be safely converted to int32", v)
		return nil
	case string:
		if intVal, err := strconv.ParseInt(v, 10, 32); err == nil {
			result := int32(intVal)
			return &result
		}
		log.Printf("Warning: string value '%s' cannot be converted to int32", v)
		return nil
	default:
		log.Printf("Warning: unsupported type %T for INT conversion: %v", v, v)
		return nil
	}
}

// scyllaDBRowToProtobuf converts a ScyllaDB row back to a TicketData protobuf
func scyllaDBRowToProtobuf(row map[string]interface{}) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Extract core fields
	if ticketID, ok := row["ticket_id"].(string); ok {
		ticketData.Id = ticketID
	}

	if createdAt, ok := row["created_at"].(time.Time); ok {
		ticketData.CreatedAt = createdAt.Format(time.RFC3339)
	}
	if updatedAt, ok := row["updated_at"].(time.Time); ok {
		ticketData.UpdatedAt = updatedAt.Format(time.RFC3339)
	}

	// Convert all other fields to protobuf FieldValue
	for key, value := range row {
		// Skip core fields that are already handled
		if key == "id" || key == "ticket_id" || key == "created_at" || key == "updated_at" {
			continue
		}

		if value == nil {
			continue
		}

		var fieldValue *ticketpb.FieldValue

		switch v := value.(type) {
		case string:
			fieldValue = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_StringValue{StringValue: v},
			}
		case int64:
			fieldValue = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_IntValue{IntValue: v},
			}
		case int32:
			fieldValue = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
			}
		case int:
			fieldValue = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
			}
		case float64:
			fieldValue = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: v},
			}
		case float32:
			fieldValue = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: float64(v)},
			}
		case bool:
			fieldValue = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_BoolValue{BoolValue: v},
			}
		default:
			continue // Skip unknown types
		}

		if fieldValue != nil {
			ticketData.Fields[key] = fieldValue
		}
	}

	return ticketData
}

// CreateTicket stores a new ticket in the ScyllaDB table
func (s *ScyllaDBStorage) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure table exists
	if err := s.createTableIfNotExists(ctx, s.baseTableName); err != nil {
		return fmt.Errorf("failed to ensure table: %w", err), nil
	}

	// Generate ticket ID if not provided
	if ticketData.Id == "" {
		ticketData.Id = s.generateTicketID()
	}

	// Convert protobuf to ScyllaDB row (isUpdate = false for create)
	row, err := protobufToScyllaDBRow(ticketData, false)
	if err != nil {
		return fmt.Errorf("failed to convert protobuf to row: %w", err), nil
	}

	// Use base table name
	tableName := s.baseTableName

	// Build INSERT query dynamically
	columns := make([]string, 0, len(row))
	placeholders := make([]string, 0, len(row))
	values := make([]interface{}, 0, len(row))

	for column, value := range row {
		columns = append(columns, column)
		placeholders = append(placeholders, "?")
		values = append(values, value)
	}

	insertCQL := fmt.Sprintf(
		"INSERT INTO %s.%s (%s) VALUES (%s)",
		s.keyspace,
		tableName,
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	)

	if err := s.session.Query(insertCQL, values...).Exec(); err != nil {
		return fmt.Errorf("failed to create ticket in table %s: %w", tableName, err), nil
	}

	log.Printf("Created ticket %s in table %s", ticketData.Id, tableName)

	result := map[string]interface{}{
		"id":        row["id"],
		"ticket_id": ticketData.Id,
	}

	return nil, result
}

// GetTicket retrieves a single ticket by ID from the ScyllaDB table
func (s *ScyllaDBStorage) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure table exists
	if err := s.createTableIfNotExists(ctx, s.baseTableName); err != nil {
		log.Printf("ERROR: Failed to ensure table: %v", err)
		return nil, false
	}

	// Use base table name
	tableName := s.baseTableName

	// Build SELECT query
	selectCQL := fmt.Sprintf(
		"SELECT * FROM %s.%s WHERE ticket_id = ?",
		s.keyspace, tableName,
	)

	// Execute query
	iter := s.session.Query(selectCQL, id).Iter()
	defer iter.Close()

	// Scan the row safely
	if rowMap, found := scanRowSafely(iter); found {
		// Convert to protobuf
		ticketData := scyllaDBRowToProtobuf(rowMap)
		log.Printf("Found ticket %s", id)
		return ticketData, true
	}

	if err := iter.Close(); err != nil {
		log.Printf("ERROR: Failed to close iterator: %v", err)
	}

	log.Printf("Ticket %s not found", id)
	return nil, false
}

// UpdateTicket updates an existing ticket in the ScyllaDB table
func (s *ScyllaDBStorage) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure table exists
	if err := s.createTableIfNotExists(ctx, s.baseTableName); err != nil {
		log.Printf("ERROR: Failed to ensure table: %v", err)
		return false
	}

	// Convert protobuf to ScyllaDB row (isUpdate = true for update)
	row, err := protobufToScyllaDBRow(ticketData, true)
	if err != nil {
		log.Printf("ERROR: Failed to convert protobuf to row: %v", err)
		return false
	}

	// Remove primary key fields from update
	delete(row, "ticket_id")
	delete(row, "id")

	if len(row) == 0 {
		log.Printf("No fields to update for ticket %s", ticketData.Id)
		return true
	}

	// Use base table name
	tableName := s.baseTableName

	// Build UPDATE query dynamically
	setClauses := make([]string, 0, len(row))
	values := make([]interface{}, 0, len(row)+2) // +2 for WHERE clause

	for column, value := range row {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", column))
		values = append(values, value)
	}

	// Add WHERE clause values
	values = append(values, ticketData.Id)

	updateCQL := fmt.Sprintf(
		"UPDATE %s.%s SET %s WHERE ticket_id = ?",
		s.keyspace,
		tableName,
		strings.Join(setClauses, ", "),
	)

	if err := s.session.Query(updateCQL, values...).Exec(); err != nil {
		log.Printf("ERROR: Failed to update ticket %s in table %s: %v", ticketData.Id, tableName, err)
		return false
	}

	log.Printf("Updated ticket %s in table %s", ticketData.Id, tableName)
	return true
}

// DeleteTicket removes a ticket from the ScyllaDB table
func (s *ScyllaDBStorage) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
	// First get the ticket to return it
	ticketData, exists := s.GetTicket(id, nil)
	if !exists {
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure table exists
	if err := s.createTableIfNotExists(ctx, s.baseTableName); err != nil {
		log.Printf("ERROR: Failed to ensure table: %v", err)
		return nil, false
	}

	// Use base table name
	tableName := s.baseTableName

	// Build DELETE query
	deleteCQL := fmt.Sprintf(
		"DELETE FROM %s.%s WHERE ticket_id = ?",
		s.keyspace, tableName,
	)

	if err := s.session.Query(deleteCQL, id).Exec(); err != nil {
		log.Printf("ERROR: Failed to delete ticket %s from table %s: %v", id, tableName, err)
		return nil, false
	}

	log.Printf("Deleted ticket %s from table %s", id, tableName)
	return ticketData, true
}

// ListTickets retrieves all tickets from the ScyllaDB table
func (s *ScyllaDBStorage) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure table exists
	if err := s.createTableIfNotExists(ctx, s.baseTableName); err != nil {
		return nil, fmt.Errorf("failed to ensure table: %w", err)
	}

	// Use base table name
	tableName := s.baseTableName

	// Build SELECT query
	selectCQL := fmt.Sprintf(
		"SELECT * FROM %s.%s",
		s.keyspace, tableName,
	)

	// Execute query
	iter := s.session.Query(selectCQL).Iter()
	defer iter.Close()

	var tickets []*ticketpb.TicketData

	// Scan all rows safely
	for {
		if rowMap, found := scanRowSafely(iter); found {
			// Convert to protobuf
			ticketData := scyllaDBRowToProtobuf(rowMap)
			tickets = append(tickets, ticketData)
		} else {
			break
		}
	}

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("error closing iterator: %w", err)
	}

	log.Printf("Found %d tickets", len(tickets))
	return tickets, nil
}

// SearchTickets searches for tickets based on conditions
func (s *ScyllaDBStorage) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	return s.SearchTicketsWithProjection(request)
}

// SearchTicketsWithProjection searches for tickets with optional field projection
func (s *ScyllaDBStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure table exists
	if err := s.createTableIfNotExists(ctx, s.baseTableName); err != nil {
		return nil, fmt.Errorf("failed to ensure table: %w", err)
	}

	// Use base table name
	tableName := s.baseTableName

	// Build SELECT clause with projection
	selectClause := "*"
	if len(request.ProjectedFields) > 0 {
		// Always include primary key fields and core fields
		projectedFields := []string{"ticket_id", "id", "created_at", "updated_at"}
		projectedFields = append(projectedFields, request.ProjectedFields...)
		selectClause = strings.Join(projectedFields, ", ")
	}

	// Build WHERE clause
	whereClause, values := s.buildWhereClause(request.Conditions)

	// Build ORDER BY clause - only use if sorting by clustering column (ticket_id)
	orderByClause := ""
	useServerSideSort := false

	// Check if we can use server-side sorting (only for ticket_id)
	if len(request.SortFields) == 1 && strings.ToLower(request.SortFields[0].Field) == "ticket_id" {
		direction := "ASC"
		if strings.ToLower(request.SortFields[0].Order) == "desc" {
			direction = "DESC"
		}
		orderByClause = fmt.Sprintf("ORDER BY ticket_id %s", direction)
		useServerSideSort = true
		log.Printf("Using server-side sorting by ticket_id %s", direction)
	} else {
		log.Printf("Using client-side sorting for %d sort fields", len(request.SortFields))
	}

	// Build complete query
	selectCQL := fmt.Sprintf(
		"SELECT %s FROM %s.%s WHERE %s %s ALLOW FILTERING",
		selectClause, s.keyspace, tableName, whereClause, orderByClause,
	)

	// Log the final query for debugging
	log.Printf("ScyllaDB Query: %s", selectCQL)
	log.Printf("Query Values: %v", values)

	// Execute query
	iter := s.session.Query(selectCQL, values...).Iter()
	defer iter.Close()

	var tickets []*ticketpb.TicketData

	// Scan all rows safely
	for {
		if rowMap, found := scanRowSafely(iter); found {
			// Convert to protobuf
			ticketData := scyllaDBRowToProtobuf(rowMap)
			tickets = append(tickets, ticketData)
		} else {
			break
		}
	}

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("error closing iterator: %w", err)
	}

	// Apply client-side sorting only if server-side sorting wasn't used
	if !useServerSideSort && len(request.SortFields) > 0 {
		log.Printf("Applying client-side sorting for %d sort fields", len(request.SortFields))
		tickets = s.applySortingClientSide(tickets, request.SortFields)
	}

	log.Printf("Found %d tickets matching search criteria", len(tickets))
	return tickets, nil
}

// buildWhereClause builds a WHERE clause from search conditions
func (s *ScyllaDBStorage) buildWhereClause(conditions []SearchCondition) (string, []interface{}) {
	var clauses []string
	var values []interface{}

	for _, condition := range conditions {
		field := strings.ToLower(condition.Operand)
		operator := strings.ToLower(condition.Operator)
		value := condition.Value

		// Convert value to appropriate type for ScyllaDB column
		convertedValue := s.convertValueForScyllaDBColumn(field, value)
		if convertedValue == nil {
			log.Printf("Warning: Could not convert value %v for field %s, skipping condition", value, field)
			continue
		}

		var clause string
		switch operator {
		case "eq", "=":
			clause = fmt.Sprintf("%s = ?", field)
			values = append(values, convertedValue)
		case "ne", "!=":
			clause = fmt.Sprintf("%s != ?", field)
			values = append(values, convertedValue)
		case "gt", ">":
			clause = fmt.Sprintf("%s > ?", field)
			values = append(values, convertedValue)
		case "gte", ">=":
			clause = fmt.Sprintf("%s >= ?", field)
			values = append(values, convertedValue)
		case "lt", "<":
			clause = fmt.Sprintf("%s < ?", field)
			values = append(values, convertedValue)
		case "lte", "<=":
			clause = fmt.Sprintf("%s <= ?", field)
			values = append(values, convertedValue)
		case "contains":
			// ScyllaDB doesn't support LIKE with wildcards in the same way as SQL
			// We'll use a simple equality check for now
			log.Printf("Warning: 'contains' operator not fully supported in ScyllaDB, using equality")
			clause = fmt.Sprintf("%s = ?", field)
			values = append(values, convertedValue)
		case "begins_with":
			// ScyllaDB doesn't support LIKE with wildcards in the same way as SQL
			// We'll use a simple equality check for now
			log.Printf("Warning: 'begins_with' operator not fully supported in ScyllaDB, using equality")
			clause = fmt.Sprintf("%s = ?", field)
			values = append(values, convertedValue)
		default:
			log.Printf("Warning: Unsupported operator '%s', skipping condition", operator)
			continue
		}

		if clause != "" {
			clauses = append(clauses, clause)
		}
	}

	return strings.Join(clauses, " AND "), values
}

// convertValueForScyllaDBColumn converts a search condition value to the appropriate type for ScyllaDB
func (s *ScyllaDBStorage) convertValueForScyllaDBColumn(fieldName string, value interface{}) interface{} {
	fieldName = strings.ToLower(fieldName)

	// BIGINT columns
	bigintFields := map[string]bool{
		"updatedbyid": true, "createdbyid": true, "removedbyid": true, "requesterid": true,
		"technicianid": true, "closedby": true, "resolvedby": true, "categoryid": true,
		"departmentid": true, "groupid": true, "impactid": true, "locationid": true,
		"priorityid": true, "statusid": true, "urgencyid": true, "violatedslaid": true,
		"servicecatalogid": true, "sourceid": true, "requesttype": true, "suggestedcategoryid": true,
		"suggestedgroupid": true, "companyid": true, "vendorid": true, "violateducid": true,
		"transitionmodelid": true, "messengerconfigid": true, "templateid": true, "emailreadconfigid": true,
		"updatedtime": true, "createdtime": true, "removedtime": true, "dueby": true,
		"firstresponsetime": true, "lastclosedtime": true, "lastopenedtime": true,
		"lastresolvedtime": true, "lastviolationtime": true, "olddueby": true, "oldresponsedue": true,
		"resolutionescalationtime": true, "responsedue": true, "responseescalationtime": true,
		"statuschangedtime": true, "groupchangedtime": true, "lastolaviolationtime": true,
		"oladueby": true, "oldoladueby": true, "askfeedbackdate": true, "firstfeedbackdate": true,
		"olaescalationtime": true, "lastucviolationtime": true, "olducdueby": true,
		"ucdueby": true, "ucescalationtime": true, "lastapproveddate": true,
		"totalonholdduration": true, "totalresolutiontime": true, "totalslapausetime": true,
		"totalworkingtime": true, "totaluconholdduration": true, "totalucpausetime": true,
		"totalucworkingtime": true, "totalucresolutiontime": true,
	}

	// INT columns
	intFields := map[string]bool{
		"approvalstatus": true, "approvaltype": true, "resolutionduelevel": true,
		"responseduelevel": true, "supportlevel": true, "oladuelevel": true, "ucduelevel": true,
	}

	// BOOLEAN columns
	boolFields := map[string]bool{
		"removed": true, "duetimemanuallyupdated": true, "reopened": true,
		"responsedueviolated": true, "slaviolated": true, "purchaserequest": true,
		"spam": true, "viprequest": true, "olaviolated": true, "ucviolated": true, "migrated": true,
	}

	// Convert based on column type
	if bigintFields[fieldName] {
		if converted := convertToBigInt(value); converted != nil {
			return *converted
		}
		return nil
	}

	if intFields[fieldName] {
		if converted := convertToInt(value); converted != nil {
			return *converted
		}
		return nil
	}

	if boolFields[fieldName] {
		switch v := value.(type) {
		case bool:
			return v
		case string:
			return strings.ToLower(v) == "true"
		case int, int32, int64:
			return v != 0
		case float32, float64:
			return v != 0.0
		}
		return nil
	}

	// For TEXT/VARCHAR columns and unknown fields, return as-is
	return value
}

// buildOrderByClause builds an ORDER BY clause from sort fields
// ScyllaDB only supports ORDER BY on clustering columns (ticket_id in our case)
// This function is now deprecated in favor of inline logic in SearchTicketsWithProjection
func (s *ScyllaDBStorage) buildOrderByClause(sortFields []SortField) string {
	// This function is kept for backward compatibility but should not be used
	// The ORDER BY logic is now handled directly in SearchTicketsWithProjection
	log.Printf("Warning: buildOrderByClause is deprecated. Use inline ORDER BY logic instead.")
	return ""
}

// applySortingClientSide applies sorting on the client side for non-clustering columns
// This is necessary because ScyllaDB doesn't support ORDER BY on secondary indexed columns
func (s *ScyllaDBStorage) applySortingClientSide(tickets []*ticketpb.TicketData, sortFields []SortField) []*ticketpb.TicketData {
	if len(sortFields) == 0 || len(tickets) <= 1 {
		return tickets
	}

	// Check if we need client-side sorting (any non-clustering column)
	needsClientSorting := false
	for _, sortField := range sortFields {
		if strings.ToLower(sortField.Field) != "ticket_id" {
			needsClientSorting = true
			break
		}
	}

	if !needsClientSorting {
		return tickets // ScyllaDB already sorted by ticket_id
	}

	// Implement client-side sorting
	sort.Slice(tickets, func(i, j int) bool {
		for _, sortField := range sortFields {
			var result int

			// Get field values for comparison
			valueI := s.getFieldValueFromTicketData(tickets[i], sortField.Field)
			valueJ := s.getFieldValueFromTicketData(tickets[j], sortField.Field)

			// Compare values
			result = s.compareValues(valueI, valueJ)

			if result != 0 {
				if strings.ToLower(sortField.Order) == "desc" {
					return result > 0
				}
				return result < 0
			}
		}
		return false // Equal values
	})

	return tickets
}

// getFieldValueFromTicketData extracts a field value from TicketData for sorting
func (s *ScyllaDBStorage) getFieldValueFromTicketData(ticketData *ticketpb.TicketData, fieldName string) interface{} {
	// Handle dynamic fields from the Fields map
	if ticketData.Fields != nil {
		if value, exists := ticketData.Fields[fieldName]; exists {
			// Extract the actual value based on the field type
			switch v := value.Value.(type) {
			case *ticketpb.FieldValue_StringValue:
				return v.StringValue
			case *ticketpb.FieldValue_IntValue:
				return v.IntValue
			case *ticketpb.FieldValue_DoubleValue:
				return v.DoubleValue
			case *ticketpb.FieldValue_BoolValue:
				return v.BoolValue
			case *ticketpb.FieldValue_BytesValue:
				return v.BytesValue
			case *ticketpb.FieldValue_StringArray:
				return v.StringArray.Values
			default:
				// Fallback to string representation
				return value.GetValue()
			}
		}
	}
	return nil
}

// compareValues compares two values for sorting
// Returns: -1 if a < b, 0 if a == b, 1 if a > b
func (s *ScyllaDBStorage) compareValues(a, b interface{}) int {
	// Handle nil values
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	// Compare by type
	switch va := a.(type) {
	case string:
		if vb, ok := b.(string); ok {
			if va < vb {
				return -1
			} else if va > vb {
				return 1
			}
			return 0
		}
	case int64:
		if vb, ok := b.(int64); ok {
			if va < vb {
				return -1
			} else if va > vb {
				return 1
			}
			return 0
		}
	case int32:
		if vb, ok := b.(int32); ok {
			if va < vb {
				return -1
			} else if va > vb {
				return 1
			}
			return 0
		}
	case float64:
		if vb, ok := b.(float64); ok {
			if va < vb {
				return -1
			} else if va > vb {
				return 1
			}
			return 0
		}
	case []string:
		// Handle string arrays by comparing first element or length
		if vb, ok := b.([]string); ok {
			if len(va) == 0 && len(vb) == 0 {
				return 0
			}
			if len(va) == 0 {
				return -1
			}
			if len(vb) == 0 {
				return 1
			}
			// Compare first elements
			if va[0] < vb[0] {
				return -1
			} else if va[0] > vb[0] {
				return 1
			}
			return 0
		}
	case []byte:
		// Handle byte arrays by comparing as strings
		if vb, ok := b.([]byte); ok {
			strA := string(va)
			strB := string(vb)
			if strA < strB {
				return -1
			} else if strA > strB {
				return 1
			}
			return 0
		}
	case bool:
		if vb, ok := b.(bool); ok {
			if !va && vb {
				return -1
			} else if va && !vb {
				return 1
			}
			return 0
		}
	case time.Time:
		if vb, ok := b.(time.Time); ok {
			if va.Before(vb) {
				return -1
			} else if va.After(vb) {
				return 1
			}
			return 0
		}
	}

	// Fallback to string comparison
	strA := fmt.Sprintf("%v", a)
	strB := fmt.Sprintf("%v", b)
	if strA < strB {
		return -1
	} else if strA > strB {
		return 1
	}
	return 0
}

// scanRowSafely scans a row with proper NULL value handling
func scanRowSafely(iter *gocql.Iter) (map[string]interface{}, bool) {
	columns := iter.Columns()
	if len(columns) == 0 {
		return nil, false
	}

	// Create slice to hold values with proper NULL handling
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))

	for i, column := range columns {
		// Use appropriate types based on CQL type to handle NULLs
		switch column.TypeInfo.Type() {
		case gocql.TypeBigInt, gocql.TypeCounter:
			var v *int64
			valuePtrs[i] = &v
		case gocql.TypeInt:
			var v *int32
			valuePtrs[i] = &v
		case gocql.TypeSmallInt:
			var v *int16
			valuePtrs[i] = &v
		case gocql.TypeTinyInt:
			var v *int8
			valuePtrs[i] = &v
		case gocql.TypeDouble:
			var v *float64
			valuePtrs[i] = &v
		case gocql.TypeFloat:
			var v *float32
			valuePtrs[i] = &v
		case gocql.TypeBoolean:
			var v *bool
			valuePtrs[i] = &v
		case gocql.TypeText, gocql.TypeVarchar, gocql.TypeAscii:
			var v *string
			valuePtrs[i] = &v
		case gocql.TypeTimestamp:
			var v *time.Time
			valuePtrs[i] = &v
		case gocql.TypeUUID, gocql.TypeTimeUUID:
			var v *gocql.UUID
			valuePtrs[i] = &v
		default:
			// For unknown types, use interface{}
			valuePtrs[i] = &values[i]
		}
	}

	// Scan the row
	if !iter.Scan(valuePtrs...) {
		return nil, false
	}

	// Convert to map with proper NULL handling
	rowMap := make(map[string]interface{})
	for i, column := range columns {
		columnName := column.Name

		switch column.TypeInfo.Type() {
		case gocql.TypeBigInt, gocql.TypeCounter:
			if v := valuePtrs[i].(**int64); *v != nil {
				rowMap[columnName] = **v
			}
		case gocql.TypeInt:
			if v := valuePtrs[i].(**int32); *v != nil {
				rowMap[columnName] = **v
			}
		case gocql.TypeSmallInt:
			if v := valuePtrs[i].(**int16); *v != nil {
				rowMap[columnName] = **v
			}
		case gocql.TypeTinyInt:
			if v := valuePtrs[i].(**int8); *v != nil {
				rowMap[columnName] = **v
			}
		case gocql.TypeDouble:
			if v := valuePtrs[i].(**float64); *v != nil {
				rowMap[columnName] = **v
			}
		case gocql.TypeFloat:
			if v := valuePtrs[i].(**float32); *v != nil {
				rowMap[columnName] = **v
			}
		case gocql.TypeBoolean:
			if v := valuePtrs[i].(**bool); *v != nil {
				rowMap[columnName] = *(*v)
			}
		case gocql.TypeText, gocql.TypeVarchar, gocql.TypeAscii:
			if v := valuePtrs[i].(**string); *v != nil {
				rowMap[columnName] = **v
			}
		case gocql.TypeTimestamp:
			if v := valuePtrs[i].(**time.Time); *v != nil {
				rowMap[columnName] = **v
			}
		case gocql.TypeUUID, gocql.TypeTimeUUID:
			if v := valuePtrs[i].(**gocql.UUID); *v != nil {
				rowMap[columnName] = (*v).String()
			}
		default:
			// For unknown types, include if not nil
			if values[i] != nil {
				rowMap[columnName] = values[i]
			}
		}
	}

	return rowMap, true
}

// Close closes the ScyllaDB session
func (s *ScyllaDBStorage) Close() error {
	if s.session != nil {
		s.session.Close()
		log.Printf("ScyllaDB session closed")
	}
	return nil
}
