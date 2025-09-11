package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// PostgreSQLStorage implements tenant-aware ticket storage using PostgreSQL
// Each tenant gets its own table for complete data isolation
type PostgreSQLStorage struct {
	db            *sql.DB
	baseTableName string
	tenantTables  sync.Map     // tenant -> tableName mapping for performance
	tableMutex    sync.RWMutex // synchronizes table creation operations
}

// NewPostgreSQLStorage creates a new tenant-aware PostgreSQL storage instance
func NewPostgreSQLStorage(ctx context.Context, baseTableName, connectionString string) (*PostgreSQLStorage, error) {
	// Open database connection
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Test the connection
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	log.Printf("PostgreSQL connection established successfully")

	storage := &PostgreSQLStorage{
		db:            db,
		baseTableName: baseTableName,
		tenantTables:  sync.Map{},
	}

	return storage, nil
}

// generateTicketID generates a unique ticket ID if not provided
func (p *PostgreSQLStorage) generateTicketID() string {
	// Generate a simple ticket ID with timestamp
	return fmt.Sprintf("TKT-%d", time.Now().UnixNano()/1000000)
}

// getTenantTableName generates a tenant-specific table name
func (p *PostgreSQLStorage) getTenantTableName(tenantID string) string {
	// Use a consistent naming pattern: baseTableName_tenantID
	// Replace any special characters in tenant ID to make it SQL-safe
	safeTenantID := strings.ReplaceAll(tenantID, "-", "_")
	safeTenantID = strings.ReplaceAll(safeTenantID, ".", "_")
	return fmt.Sprintf("%s_%s", p.baseTableName, safeTenantID)
}

// ensureTenantTable ensures that a table exists for the given tenant
func (p *PostgreSQLStorage) ensureTenantTable(ctx context.Context, tenantID string) error {
	tableName := p.getTenantTableName(tenantID)

	// Check if we already know this table exists
	if _, exists := p.tenantTables.Load(tenantID); exists {
		return nil
	}

	p.tableMutex.Lock()
	defer p.tableMutex.Unlock()

	// Double-check after acquiring lock
	if _, exists := p.tenantTables.Load(tenantID); exists {
		return nil
	}

	// Check if table exists in database
	var exists bool
	checkQuery := `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = $1
		)`

	err := p.db.QueryRowContext(ctx, checkQuery, tableName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if table exists: %w", err)
	}

	if !exists {
		// Create the table
		if err := p.createTableIfNotExists(ctx, tableName); err != nil {
			return fmt.Errorf("failed to create table for tenant %s: %w", tenantID, err)
		}
		log.Printf("Created new PostgreSQL table for tenant %s: %s", tenantID, tableName)
	} else {
		log.Printf("Found existing PostgreSQL table for tenant %s: %s", tenantID, tableName)
	}

	// Store in map to avoid future checks
	p.tenantTables.Store(tenantID, tableName)
	return nil
}

// loadSchemaFromFile loads SQL schema from the database/postgresql directory
func (p *PostgreSQLStorage) loadSchemaFromFile() (string, error) {
	// Try to find the schema file in common locations
	possiblePaths := []string{
		"database/postgresql/schema.sql",
		"../database/postgresql/schema.sql",
		"../../database/postgresql/schema.sql",
		"./database/postgresql/schema.sql",
	}

	var schemaContent string

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			content, readErr := ioutil.ReadFile(path)
			if readErr == nil {
				schemaContent = string(content)
				log.Printf("Loaded PostgreSQL schema from: %s", path)
				break
			}
		}
	}

	if schemaContent == "" {
		return "", fmt.Errorf("could not find schema.sql file in any of the expected locations: %v", possiblePaths)
	}

	return schemaContent, nil
}

// createTableIfNotExists creates a new PostgreSQL table using the schema from file
// The schema includes clustered indexes grouped by business domain to reduce write burden
// and improve query performance compared to individual field indexes
func (p *PostgreSQLStorage) createTableIfNotExists(ctx context.Context, tableName string) error {
	// Load schema from file (includes clustered indexing strategy)
	schemaContent, err := p.loadSchemaFromFile()
	if err != nil {
		return fmt.Errorf("failed to load schema file: %w", err)
	}

	// Parse the schema and adapt it for tenant-specific table
	adaptedSchema, err := p.adaptSchemaForTenant(schemaContent, tableName)
	if err != nil {
		return fmt.Errorf("failed to adapt schema for tenant: %w", err)
	}

	// Execute the adapted schema with clustered indexes
	_, err = p.db.ExecContext(ctx, adaptedSchema)
	if err != nil {
		return fmt.Errorf("failed to create table %s with clustered indexes: %w", tableName, err)
	}

	log.Printf("Created PostgreSQL table %s with clustered indexing strategy", tableName)

	log.Printf("Successfully created table %s from schema file", tableName)
	return nil
}

// adaptSchemaForTenant modifies the schema to work with tenant-specific tables
func (p *PostgreSQLStorage) adaptSchemaForTenant(schemaContent, tableName string) (string, error) {
	// Split the schema into statements
	statements := strings.Split(schemaContent, ";")
	var adaptedStatements []string

	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		// Skip DROP statements for safety
		if strings.HasPrefix(strings.ToUpper(stmt), "DROP") {
			continue
		}

		// Replace table name in CREATE TABLE statements
		if strings.Contains(strings.ToUpper(stmt), "CREATE TABLE") {
			// Replace "CREATE TABLE tickets" with "CREATE TABLE {tableName}"
			stmt = strings.ReplaceAll(stmt, "CREATE TABLE tickets", fmt.Sprintf("CREATE TABLE %s", tableName))
		}

		// Replace table name in INDEX statements
		if strings.Contains(strings.ToUpper(stmt), "CREATE INDEX") {
			// Replace "ON tickets" with "ON {tableName}"
			stmt = strings.ReplaceAll(stmt, "ON tickets(", fmt.Sprintf("ON %s(", tableName))
			stmt = strings.ReplaceAll(stmt, "ON tickets ", fmt.Sprintf("ON %s ", tableName))

			// Replace index names to be table-specific
			if strings.Contains(stmt, "idx_tickets_") {
				stmt = strings.ReplaceAll(stmt, "idx_tickets_", fmt.Sprintf("idx_%s_", tableName))
			}
		}

		if strings.Contains(strings.ToUpper(stmt), "CREATE UNIQUE INDEX") {
			// Replace "ON tickets" with "ON {tableName}"
			stmt = strings.ReplaceAll(stmt, "ON tickets(", fmt.Sprintf("ON %s(", tableName))
			stmt = strings.ReplaceAll(stmt, "ON tickets ", fmt.Sprintf("ON %s ", tableName))

			// Replace index names to be table-specific
			if strings.Contains(stmt, "idx_tickets_") {
				stmt = strings.ReplaceAll(stmt, "idx_tickets_", fmt.Sprintf("idx_%s_", tableName))
			}
		}

		// Replace table name in TRIGGER statements
		if strings.Contains(strings.ToUpper(stmt), "CREATE TRIGGER") {
			stmt = strings.ReplaceAll(stmt, "ON tickets", fmt.Sprintf("ON %s", tableName))
			stmt = strings.ReplaceAll(stmt, "update_tickets_updated_at", fmt.Sprintf("update_%s_updated_at", tableName))
		}

		// Replace table name in COMMENT statements
		if strings.Contains(strings.ToUpper(stmt), "COMMENT ON") {
			stmt = strings.ReplaceAll(stmt, "ON TABLE tickets", fmt.Sprintf("ON TABLE %s", tableName))
			stmt = strings.ReplaceAll(stmt, "ON COLUMN tickets.", fmt.Sprintf("ON COLUMN %s.", tableName))
		}

		adaptedStatements = append(adaptedStatements, stmt)
	}

	return strings.Join(adaptedStatements, ";\n"), nil
}

// protobufToPostgreSQLRow converts a TicketData protobuf to PostgreSQL row data
func protobufToPostgreSQLRow(ticketData *ticketpb.TicketData, tenant string, isUpdate bool) (map[string]interface{}, error) {
	row := make(map[string]interface{})

	// Core fields - these are managed by the application
	row["ticket_id"] = ticketData.Id
	row["tenant"] = tenant // Use the tenant parameter passed to the function

	// Handle timestamps
	if !isUpdate {
		// For new tickets, set created_at to current time
		row["created_at"] = time.Now()
	}
	// Always update updated_at for both create and update
	row["updated_at"] = time.Now()

	// Convert protobuf fields to PostgreSQL columns
	for fieldName, fieldValue := range ticketData.Fields {
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
		case *ticketpb.FieldValue_BytesValue:
			value = v.BytesValue
		case *ticketpb.FieldValue_StringArray:
			value = v.StringArray.Values
		default:
			value = nil
		}

		// Map to specific columns if they exist in the schema
		switch fieldName {
		case "updatedbyid", "createdbyid", "removedbyid", "requesterid", "technicianid",
			"closedby", "resolvedby", "categoryid", "departmentid", "groupid", "impactid",
			"locationid", "priorityid", "statusid", "urgencyid", "violatedslaid",
			"servicecatalogid", "sourceid", "requesttype", "suggestedcategoryid", "suggestedgroupid",
			"companyid", "vendorid", "violateducid", "transitionmodelid", "messengerconfigid",
			"templateid", "emailreadconfigid":
			if intVal, ok := value.(int64); ok {
				row[fieldName] = intVal
			} else if floatVal, ok := value.(float64); ok {
				row[fieldName] = int64(floatVal)
			} else if strVal, ok := value.(string); ok {
				if intVal, err := strconv.ParseInt(strVal, 10, 64); err == nil {
					row[fieldName] = intVal
				} else if floatVal, err := strconv.ParseFloat(strVal, 64); err == nil {
					row[fieldName] = int64(floatVal)
				}
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
			if intVal, ok := value.(int64); ok {
				row[fieldName] = intVal
			} else if floatVal, ok := value.(float64); ok {
				row[fieldName] = int64(floatVal)
			} else if strVal, ok := value.(string); ok {
				if intVal, err := strconv.ParseInt(strVal, 10, 64); err == nil {
					row[fieldName] = intVal
				} else if floatVal, err := strconv.ParseFloat(strVal, 64); err == nil {
					row[fieldName] = int64(floatVal)
				}
			}
		case "name", "oobtype", "description", "originaldescription", "subject",
			"callfrom", "emailreadconfigemail":
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
			if intVal, ok := value.(int64); ok {
				row[fieldName] = int(intVal)
			} else if floatVal, ok := value.(float64); ok {
				row[fieldName] = int(floatVal)
			} else if strVal, ok := value.(string); ok {
				if intVal, err := strconv.Atoi(strVal); err == nil {
					row[fieldName] = intVal
				} else if floatVal, err := strconv.ParseFloat(strVal, 64); err == nil {
					row[fieldName] = int(floatVal)
				}
			}
		default:
			// Skip unknown fields - all CSV fields should be mapped to columns
			log.Printf("WARNING: Unmapped field %s with value %v", fieldName, value)
		}
	}

	return row, nil
}

// postgreSQLRowToProtobuf converts a PostgreSQL row back to a TicketData protobuf
func postgreSQLRowToProtobuf(row map[string]interface{}) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Extract core fields
	if ticketID, ok := row["ticket_id"].(string); ok {
		ticketData.Id = ticketID
	}
	if tenant, ok := row["tenant"].(string); ok {
		ticketData.Tenant = tenant
	}
	if createdAt, ok := row["created_at"].(time.Time); ok {
		ticketData.CreatedAt = createdAt.Format(time.RFC3339)
	}
	if updatedAt, ok := row["updated_at"].(time.Time); ok {
		ticketData.UpdatedAt = updatedAt.Format(time.RFC3339)
	}

	// Convert all other fields to protobuf FieldValue
	for key, value := range row {
		if key == "id" || key == "ticket_id" || key == "tenant" ||
			key == "created_at" || key == "updated_at" {
			continue
		}

		if value == nil {
			continue
		}

		fieldValue := &ticketpb.FieldValue{}

		switch v := value.(type) {
		case string:
			fieldValue.Value = &ticketpb.FieldValue_StringValue{StringValue: v}
		case int64:
			fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: v}
		case int32:
			fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: int64(v)}
		case int:
			fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: int64(v)}
		case float64:
			fieldValue.Value = &ticketpb.FieldValue_DoubleValue{DoubleValue: v}
		case bool:
			fieldValue.Value = &ticketpb.FieldValue_BoolValue{BoolValue: v}
		case []byte:
			fieldValue.Value = &ticketpb.FieldValue_BytesValue{BytesValue: v}
		default:
			// Convert to string as fallback
			fieldValue.Value = &ticketpb.FieldValue_StringValue{StringValue: fmt.Sprintf("%v", v)}
		}

		ticketData.Fields[key] = fieldValue
	}

	return ticketData
}

// CreateTicket stores a new ticket in the tenant-specific PostgreSQL table
func (p *PostgreSQLStorage) CreateTicket(tenant string, ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure tenant table exists
	if err := p.ensureTenantTable(ctx, tenant); err != nil {
		return fmt.Errorf("failed to ensure tenant table: %w", err), nil
	}

	// Generate ticket ID if not provided
	if ticketData.Id == "" {
		ticketData.Id = p.generateTicketID()
	}

	// Ensure tenant is set correctly
	ticketData.Tenant = tenant

	// Convert protobuf to PostgreSQL row (isUpdate = false for create)
	row, err := protobufToPostgreSQLRow(ticketData, tenant, false)
	if err != nil {
		return fmt.Errorf("failed to convert protobuf to row: %w", err), nil
	}

	// Get tenant-specific table name
	tableName := p.getTenantTableName(tenant)

	// Build INSERT query dynamically
	columns := make([]string, 0, len(row))
	placeholders := make([]string, 0, len(row))
	values := make([]interface{}, 0, len(row))

	i := 1
	for column, value := range row {
		columns = append(columns, column)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		values = append(values, value)
		i++
	}

	insertSQL := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		tableName,
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	)

	var generatedID int64
	err = p.db.QueryRowContext(ctx, insertSQL, values...).Scan(&generatedID)
	if err != nil {
		return fmt.Errorf("failed to create ticket in table %s: %w", tableName, err), nil
	}

	log.Printf("Created ticket %s for tenant %s in table %s with ID %d", ticketData.Id, tenant, tableName, generatedID)

	result := map[string]interface{}{
		"id":        generatedID,
		"ticket_id": ticketData.Id,
	}

	return nil, result
}

// GetTicket retrieves a ticket by tenant and ID from the tenant-specific PostgreSQL table
func (p *PostgreSQLStorage) GetTicket(tenant, id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure tenant table exists
	if err := p.ensureTenantTable(ctx, tenant); err != nil {
		log.Printf("ERROR: Failed to ensure tenant table for %s: %v", tenant, err)
		return nil, false
	}

	tableName := p.getTenantTableName(tenant)

	// Query for the ticket
	query := fmt.Sprintf("SELECT * FROM %s WHERE ticket_id = $1", tableName)

	rows, err := p.db.QueryContext(ctx, query, id)
	if err != nil {
		log.Printf("ERROR: Failed to query ticket %s from table %s: %v", id, tableName, err)
		return nil, false
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, false
	}

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		log.Printf("ERROR: Failed to get columns: %v", err)
		return nil, false
	}

	// Create slice to hold values
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	// Scan the row
	if err := rows.Scan(valuePtrs...); err != nil {
		log.Printf("ERROR: Failed to scan row: %v", err)
		return nil, false
	}

	// Convert to map
	rowMap := make(map[string]interface{})
	for i, column := range columns {
		rowMap[column] = values[i]
	}

	// Convert to protobuf
	ticketData := postgreSQLRowToProtobuf(rowMap)

	log.Printf("Retrieved ticket %s for tenant %s from table %s", id, tenant, tableName)
	return ticketData, true
}

// UpdateTicket updates an existing ticket in the tenant-specific PostgreSQL table
func (p *PostgreSQLStorage) UpdateTicket(tenant string, ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Ensure tenant table exists
	if err := p.ensureTenantTable(ctx, tenant); err != nil {
		log.Printf("ERROR: Failed to ensure tenant table for %s: %v", tenant, err)
		return false
	}

	// Convert protobuf to PostgreSQL row (isUpdate = true for update)
	row, err := protobufToPostgreSQLRow(ticketData, tenant, true)
	if err != nil {
		log.Printf("ERROR: Failed to convert protobuf to row: %v", err)
		return false
	}

	tableName := p.getTenantTableName(tenant)

	// Build UPDATE query dynamically
	setParts := make([]string, 0, len(row))
	values := make([]interface{}, 0, len(row)+1)

	i := 1
	for column, value := range row {
		if column == "ticket_id" || column == "created_at" || column == "tenant" {
			continue // Don't update these immutable fields
		}
		setParts = append(setParts, fmt.Sprintf("%s = $%d", column, i))
		values = append(values, value)
		i++
	}

	// Add ticket_id for WHERE clause
	values = append(values, ticketData.Id)

	updateSQL := fmt.Sprintf(
		"UPDATE %s SET %s WHERE ticket_id = $%d",
		tableName,
		strings.Join(setParts, ", "),
		i,
	)

	result, err := p.db.ExecContext(ctx, updateSQL, values...)
	if err != nil {
		log.Printf("ERROR: Failed to update ticket in table %s: %v", tableName, err)
		return false
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("ERROR: Failed to get rows affected: %v", err)
		return false
	}

	if rowsAffected == 0 {
		log.Printf("No ticket found with ID %s in table %s", ticketData.Id, tableName)
		return false
	}

	log.Printf("Updated ticket %s for tenant %s in table %s", ticketData.Id, tenant, tableName)
	return true
}

// DeleteTicket removes a ticket from the tenant-specific PostgreSQL table
func (p *PostgreSQLStorage) DeleteTicket(tenant, id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First, get the ticket to return it
	ticketData, exists := p.GetTicket(tenant, id, nil)
	if !exists {
		return nil, false
	}

	tableName := p.getTenantTableName(tenant)

	// Delete the ticket
	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE ticket_id = $1", tableName)

	result, err := p.db.ExecContext(ctx, deleteSQL, id)
	if err != nil {
		log.Printf("ERROR: Failed to delete ticket %s from table %s: %v", id, tableName, err)
		return nil, false
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("ERROR: Failed to get rows affected: %v", err)
		return nil, false
	}

	if rowsAffected == 0 {
		return nil, false
	}

	log.Printf("Deleted ticket %s for tenant %s from table %s", id, tenant, tableName)
	return ticketData, true
}

// ListTickets retrieves all tickets for a tenant from the tenant-specific PostgreSQL table
func (p *PostgreSQLStorage) ListTickets(tenant string, store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Ensure tenant table exists
	if err := p.ensureTenantTable(ctx, tenant); err != nil {
		return nil, fmt.Errorf("failed to ensure tenant table: %w", err)
	}

	tableName := p.getTenantTableName(tenant)

	// Query all tickets for the tenant
	query := fmt.Sprintf("SELECT * FROM %s WHERE tenant = $1 ORDER BY created_at DESC", tableName)

	rows, err := p.db.QueryContext(ctx, query, tenant)
	if err != nil {
		return nil, fmt.Errorf("failed to query tickets from table %s: %w", tableName, err)
	}
	defer rows.Close()

	var tickets []*ticketpb.TicketData

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	for rows.Next() {
		// Create slice to hold values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan the row
		if err := rows.Scan(valuePtrs...); err != nil {
			log.Printf("ERROR: Failed to scan row: %v", err)
			continue
		}

		// Convert to map
		rowMap := make(map[string]interface{})
		for i, column := range columns {
			rowMap[column] = values[i]
		}

		// Convert to protobuf
		ticketData := postgreSQLRowToProtobuf(rowMap)
		tickets = append(tickets, ticketData)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	log.Printf("Retrieved %d tickets for tenant %s from table %s", len(tickets), tenant, tableName)
	return tickets, nil
}

// buildWhereClause builds a WHERE clause from search conditions
func (p *PostgreSQLStorage) buildWhereClause(conditions []SearchCondition, tenant string) (string, []interface{}, error) {
	var whereParts []string
	var values []interface{}
	paramIndex := 1

	// Always add tenant filter first
	whereParts = append(whereParts, fmt.Sprintf("tenant = $%d", paramIndex))
	values = append(values, tenant)
	paramIndex++

	if len(conditions) == 0 {
		return strings.Join(whereParts, " AND "), values, nil
	}

	for _, condition := range conditions {
		var clause string

		switch strings.ToLower(condition.Operator) {
		case "eq", "=":
			clause = fmt.Sprintf("%s = $%d", condition.Operand, paramIndex)
			values = append(values, condition.Value)
		case "ne", "!=":
			clause = fmt.Sprintf("%s != $%d", condition.Operand, paramIndex)
			values = append(values, condition.Value)
		case "gt", ">":
			clause = fmt.Sprintf("%s > $%d", condition.Operand, paramIndex)
			values = append(values, condition.Value)
		case "gte", ">=":
			clause = fmt.Sprintf("%s >= $%d", condition.Operand, paramIndex)
			values = append(values, condition.Value)
		case "lt", "<":
			clause = fmt.Sprintf("%s < $%d", condition.Operand, paramIndex)
			values = append(values, condition.Value)
		case "lte", "<=":
			clause = fmt.Sprintf("%s <= $%d", condition.Operand, paramIndex)
			values = append(values, condition.Value)
		case "contains":
			clause = fmt.Sprintf("%s ILIKE $%d", condition.Operand, paramIndex)
			values = append(values, fmt.Sprintf("%%%v%%", condition.Value))
		case "begins_with":
			clause = fmt.Sprintf("%s ILIKE $%d", condition.Operand, paramIndex)
			values = append(values, fmt.Sprintf("%v%%", condition.Value))
		case "in":
			// Handle IN operator for arrays
			if valueSlice, ok := condition.Value.([]interface{}); ok {
				placeholders := make([]string, len(valueSlice))
				for i, val := range valueSlice {
					placeholders[i] = fmt.Sprintf("$%d", paramIndex)
					values = append(values, val)
					paramIndex++
				}
				clause = fmt.Sprintf("%s IN (%s)", condition.Operand, strings.Join(placeholders, ", "))
				paramIndex-- // Adjust because we incremented in the loop
			} else {
				return "", nil, fmt.Errorf("IN operator requires array value")
			}
		default:
			return "", nil, fmt.Errorf("unsupported operator: %s", condition.Operator)
		}

		whereParts = append(whereParts, clause)
		paramIndex++
	}

	whereClause := strings.Join(whereParts, " AND ")
	return whereClause, values, nil
}

// buildOrderByClause builds an ORDER BY clause from sort fields
func (p *PostgreSQLStorage) buildOrderByClause(sortFields []SortField) string {
	if len(sortFields) == 0 {
		return "ORDER BY created_at DESC" // Default sort
	}

	var orderParts []string
	for _, sortField := range sortFields {
		direction := "ASC"
		if strings.ToLower(sortField.Order) == "desc" {
			direction = "DESC"
		}
		orderParts = append(orderParts, fmt.Sprintf("%s %s", sortField.Field, direction))
	}

	return "ORDER BY " + strings.Join(orderParts, ", ")
}

// SearchTickets searches for tickets based on conditions
func (p *PostgreSQLStorage) SearchTickets(tenant string, request SearchRequest) ([]*ticketpb.TicketData, error) {
	return p.SearchTicketsWithProjection(tenant, request)
}

// SearchTicketsWithProjection searches for tickets with optional field projection
func (p *PostgreSQLStorage) SearchTicketsWithProjection(tenant string, request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Ensure tenant table exists
	if err := p.ensureTenantTable(ctx, tenant); err != nil {
		return nil, fmt.Errorf("failed to ensure tenant table: %w", err)
	}

	tableName := p.getTenantTableName(tenant)

	// Build SELECT clause
	selectClause := "*"
	if len(request.ProjectedFields) > 0 {
		// Always include core fields for protobuf compatibility
		coreFields := []string{"id", "ticket_id", "tenant", "created_at", "updated_at"}
		allFields := append(coreFields, request.ProjectedFields...)

		// Remove duplicates
		fieldMap := make(map[string]bool)
		var uniqueFields []string
		for _, field := range allFields {
			if !fieldMap[field] {
				fieldMap[field] = true
				uniqueFields = append(uniqueFields, field)
			}
		}

		selectClause = strings.Join(uniqueFields, ", ")
	}

	// Build WHERE clause
	whereClause, values, err := p.buildWhereClause(request.Conditions, tenant)
	if err != nil {
		return nil, fmt.Errorf("failed to build WHERE clause: %w", err)
	}

	// Build ORDER BY clause
	orderByClause := p.buildOrderByClause(request.SortFields)

	// Build complete query
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s %s", selectClause, tableName, whereClause, orderByClause)

	// Execute query
	rows, err := p.db.QueryContext(ctx, query, values...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search query: %w", err)
	}
	defer rows.Close()

	var tickets []*ticketpb.TicketData

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	for rows.Next() {
		// Create slice to hold values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan the row
		if err := rows.Scan(valuePtrs...); err != nil {
			log.Printf("ERROR: Failed to scan row: %v", err)
			continue
		}

		// Convert to map
		rowMap := make(map[string]interface{})
		for i, column := range columns {
			rowMap[column] = values[i]
		}

		// Convert to protobuf
		ticketData := postgreSQLRowToProtobuf(rowMap)
		tickets = append(tickets, ticketData)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	log.Printf("Found %d tickets for tenant %s matching search criteria", len(tickets), tenant)
	return tickets, nil
}

// Close closes the database connection
func (p *PostgreSQLStorage) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}
