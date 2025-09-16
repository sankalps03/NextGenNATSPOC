package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// PostgreSQLDocumentDBStorage implements ticket storage with all fixed fields as columns + one BSON custom field
type PostgreSQLDocumentDBStorage struct {
	db        *sql.DB
	tableName string
}

// NewPostgreSQLDocumentDBStorage creates a new PostgreSQL hybrid storage instance
func NewPostgreSQLDocumentDBStorage(ctx context.Context, tableName, connectionString string) (*PostgreSQLDocumentDBStorage, error) {
	if tableName == "" {
		tableName = "tickets"
	}

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

	log.Printf("PostgreSQLDocument BD Hybrid V connection established successfully")

	storage := &PostgreSQLDocumentDBStorage{
		db:        db,
		tableName: tableName,
	}

	// Ensure the table exists
	if err := storage.ensureTableExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure table exists: %w", err)
	}

	return storage, nil
}

// getFixedFields returns the set of fields that are stored as columns
func (p *PostgreSQLDocumentDBStorage) getFixedFields() map[string]bool {
	return map[string]bool{
		// Core fields
		"ticket_id": true, "created_at": true, "updated_at": true,

		// User and assignment fields
		"updatedbyid": true, "createdbyid": true, "removedbyid": true, "requesterid": true,
		"technicianid": true, "closedby": true, "resolvedby": true,

		// Timestamp fields
		"updatedtime": true, "createdtime": true, "removedtime": true, "dueby": true,
		"firstresponsetime": true, "lastclosedtime": true, "lastopenedtime": true,
		"lastresolvedtime": true, "lastviolationtime": true, "olddueby": true,
		"oldresponsedue": true, "resolutionescalationtime": true, "responsedue": true,
		"responseescalationtime": true, "statuschangedtime": true, "groupchangedtime": true,
		"lastolaviolationtime": true, "oladueby": true, "oldoladueby": true,
		"askfeedbackdate": true, "firstfeedbackdate": true, "olaescalationtime": true,
		"lastucviolationtime": true, "olducdueby": true, "ucdueby": true,
		"ucescalationtime": true, "lastapproveddate": true,

		// Text fields
		"name": true, "oobtype": true, "description": true, "originaldescription": true,
		"subject": true, "callfrom": true, "emailreadconfigemail": true,

		// Boolean fields
		"removed": true, "duetimemanuallyupdated": true,
		"responsedueviolated": true, "slaviolated": true, "purchaserequest": true,
		"spam": true, "viprequest": true, "olaviolated": true, "ucviolated": true,
		"migrated": true,

		// Category and classification fields
		"categoryid": true, "departmentid": true, "groupid": true, "impactid": true,
		"locationid": true, "priorityid": true, "statusid": true, "urgencyid": true,
		"violatedslaid": true, "servicecatalogid": true, "sourceid": true,
		"requesttype": true, "suggestedcategoryid": true, "suggestedgroupid": true,
		"companyid": true, "vendorid": true, "violateducid": true,
		"transitionmodelid": true, "messengerconfigid": true,

		// Approval and workflow fields
		"approvalstatus": true, "approvaltype": true, "resolutionduelevel": true,
		"responseduelevel": true, "supportlevel": true, "oladuelevel": true,
		"ucduelevel": true,

		// Duration and time tracking fields
		"totalonholdduration": true, "totalresolutiontime": true, "totalslapausetime": true,
		"totalworkingtime": true, "totaluconholdduration": true, "totalucpausetime": true,
		"totalucworkingtime": true, "totalucresolutiontime": true,

		// Configuration and template fields
		"templateid": true, "emailreadconfigid": true,
	}
}

// generateTicketID generates a unique ticket ID if not provided
func (p *PostgreSQLDocumentDBStorage) generateTicketID() string {
	return fmt.Sprintf("TKT-%d", time.Now().UnixNano()/1000000)
}

// ensureTableExists ensures that the tickets table exists
func (p *PostgreSQLDocumentDBStorage) ensureTableExists(ctx context.Context) error {
	// Check if table exists in database
	var exists bool
	checkQuery := `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = $1
		)`

	err := p.db.QueryRowContext(ctx, checkQuery, p.tableName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if table exists: %w", err)
	}

	if !exists {
		// Create the table using the schema file
		if err := p.createTableIfNotExists(ctx); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
		log.Printf("Created new PostgreSQL DocumentDB hybrid table: %s", p.tableName)
	} else {
		log.Printf("Found existing PostgreSQL DocumentDB hybrid table: %s", p.tableName)
	}

	return nil
}

// createTableIfNotExists creates the PostgreSQL table using the hybrid schema from file
func (p *PostgreSQLDocumentDBStorage) createTableIfNotExists(ctx context.Context) error {
	// Load schema from file
	schemaContent, err := p.loadSchemaFromFile()
	if err != nil {
		return fmt.Errorf("failed to load schema file: %w", err)
	}

	// Replace table name in schema if needed
	adaptedSchema := strings.ReplaceAll(schemaContent, "tickets", p.tableName)

	// Execute the schema
	_, err = p.db.ExecContext(ctx, adaptedSchema)
	if err != nil {
		return fmt.Errorf("failed to create hybrid table %s: %w", p.tableName, err)
	}

	log.Printf("Created PostgreSQL DocumentDB hybrid table %s", p.tableName)
	return nil
}

// loadSchemaFromFile loads SQL schema from the database/postgresql directory
func (p *PostgreSQLDocumentDBStorage) loadSchemaFromFile() (string, error) {
	// Try to find the hybrid schema file in common locations
	possiblePaths := []string{
		"database/postgresql/schema_hybrid.sql",
		"../database/postgresql/schema_hybrid.sql",
		"../../database/postgresql/schema_hybrid.sql",
		"./database/postgresql/schema_hybrid.sql",
		"/home/sankalp-singh/Workspace/NextGenNATSPOC/database/postgresql/schema_hybrid.sql",
	}

	var schemaContent string

	for _, path := range possiblePaths {
		content, readErr := p.readFileIfExists(path)
		if readErr == nil && content != "" {
			schemaContent = content
			log.Printf("Loaded PostgreSQL DocumentDB hybrid schema from: %s", path)
			break
		}
	}

	if schemaContent == "" {
		return "", fmt.Errorf("could not find schema_hybrid.sql file in any of the expected locations: %v", possiblePaths)
	}

	return schemaContent, nil
}

// readFileIfExists reads a file if it exists, returns empty string and nil error if file doesn't exist
func (p *PostgreSQLDocumentDBStorage) readFileIfExists(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", err // File doesn't exist
		}
		return "", err // Other error
	}
	return string(content), nil
}

// protobufToHybridData converts a TicketData protobuf to fixed fields + custom data
func (p *PostgreSQLDocumentDBStorage) protobufToHybridData(ticketData *ticketpb.TicketData, isUpdate bool) (map[string]interface{}, map[string]interface{}, error) {
	fixedFields := make(map[string]interface{})
	customData := make(map[string]interface{})

	fixedFieldsSet := p.getFixedFields()

	// Core fields
	fixedFields["ticket_id"] = ticketData.Id

	// Handle timestamps
	if !isUpdate {
		fixedFields["created_at"] = time.Now()
		fixedFields["createdtime"] = time.Now().UnixNano() / 1000000
	}
	fixedFields["updated_at"] = time.Now()
	fixedFields["updatedtime"] = time.Now().UnixNano() / 1000000

	// Process all fields from the protobuf Fields map
	for fieldName, fieldValue := range ticketData.Fields {
		if fieldValue == nil {
			continue
		}

		// Convert protobuf field value to Go interface
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
			continue
		}

		fieldNameLower := strings.ToLower(fieldName)

		// Check if this field is a fixed column
		if fixedFieldsSet[fieldNameLower] {
			// Store in fixed fields
			fixedFields[fieldNameLower] = value
		} else {
			// Store in custom data BSON
			customData[fieldName] = value
		}
	}

	return fixedFields, customData, nil
}

// hybridDataToProtobuf converts fixed fields + custom data back to protobuf
func (p *PostgreSQLDocumentDBStorage) hybridDataToProtobuf(fixedRow map[string]interface{}, customDataBytes []byte) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Extract core fields
	if ticketID, ok := fixedRow["ticket_id"].(string); ok {
		ticketData.Id = ticketID
	}
	if createdAt, ok := fixedRow["created_at"].(time.Time); ok {
		ticketData.CreatedAt = createdAt.Format(time.RFC3339)
	}
	if updatedAt, ok := fixedRow["updated_at"].(time.Time); ok {
		ticketData.UpdatedAt = updatedAt.Format(time.RFC3339)
	}

	// Process fixed fields
	for fieldName, value := range fixedRow {
		// Skip core fields that are handled separately
		if fieldName == "id" || fieldName == "ticket_id" || fieldName == "created_at" || fieldName == "updated_at" {
			continue
		}

		if value != nil {
			fieldValue := interfaceToFieldValue(value)
			if fieldValue != nil {
				ticketData.Fields[fieldName] = fieldValue
			}
		}
	}

	// Process custom data from BSON
	if len(customDataBytes) > 0 {
		var customData map[string]interface{}
		if err := json.Unmarshal(customDataBytes, &customData); err == nil {
			for fieldName, value := range customData {
				if value != nil {
					fieldValue := interfaceToFieldValue(value)
					if fieldValue != nil {
						ticketData.Fields[fieldName] = fieldValue
					}
				}
			}
		}
	}

	return ticketData
}

// CreateTicket stores a new ticket
func (p *PostgreSQLDocumentDBStorage) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Generate ticket ID if not provided
	if ticketData.Id == "" {
		ticketData.Id = p.generateTicketID()
	}

	// Convert protobuf to fixed fields + custom data
	fixedFields, customData, err := p.protobufToHybridData(ticketData, false)
	if err != nil {
		return fmt.Errorf("failed to convert protobuf to hybrid data: %w", err), nil
	}

	// Serialize custom data to JSON string
	customDataJSON, err := json.Marshal(customData)
	if err != nil {
		return fmt.Errorf("failed to marshal custom data: %w", err), nil
	}
	fixedFields["custom_data"] = string(customDataJSON)

	// Build dynamic INSERT query
	columns := make([]string, 0, len(fixedFields))
	placeholders := make([]string, 0, len(fixedFields))
	values := make([]interface{}, 0, len(fixedFields))

	i := 1
	for column, value := range fixedFields {
		columns = append(columns, column)
		placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		values = append(values, value)
		i++
	}

	insertSQL := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		p.tableName,
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	)

	var generatedID int64
	err = p.db.QueryRowContext(ctx, insertSQL, values...).Scan(&generatedID)
	if err != nil {
		return fmt.Errorf("failed to create ticket: %w", err), nil
	}

	log.Printf("Created ticket %s with ID %d", ticketData.Id, generatedID)

	result := map[string]interface{}{
		"id":        generatedID,
		"ticket_id": ticketData.Id,
	}

	return nil, result
}

// GetTicket retrieves a ticket by ID
func (p *PostgreSQLDocumentDBStorage) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	query := fmt.Sprintf("SELECT * FROM %s WHERE ticket_id = $1", p.tableName)

	rows, err := p.db.QueryContext(ctx, query, id)
	if err != nil {
		log.Printf("ERROR: Failed to query ticket %s: %v", id, err)
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
	var customDataBytes []byte

	for i, column := range columns {
		if column == "custom_data" {
			if values[i] != nil {
				switch v := values[i].(type) {
				case []byte:
					customDataBytes = v
				case string:
					customDataBytes = []byte(v)
				}
			}
		} else {
			rowMap[column] = values[i]
		}
	}

	// Convert to protobuf
	ticketData := p.hybridDataToProtobuf(rowMap, customDataBytes)

	log.Printf("Retrieved ticket %s", id)
	return ticketData, true
}

// UpdateTicket updates an existing ticket
func (p *PostgreSQLDocumentDBStorage) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Convert protobuf to fixed fields + custom data
	fixedFields, customData, err := p.protobufToHybridData(ticketData, true)
	if err != nil {
		log.Printf("ERROR: Failed to convert protobuf to hybrid data: %v", err)
		return false
	}

	// Serialize custom data to JSON string
	customDataJSON, err := json.Marshal(customData)
	if err != nil {
		log.Printf("ERROR: Failed to marshal custom data: %v", err)
		return false
	}
	fixedFields["custom_data"] = string(customDataJSON)

	// Build dynamic UPDATE query
	setParts := make([]string, 0, len(fixedFields))
	values := make([]interface{}, 0, len(fixedFields)+1)

	i := 1
	for column, value := range fixedFields {
		if column == "ticket_id" || column == "created_at" || column == "createdtime" {
			continue // Don't update immutable fields
		}
		setParts = append(setParts, fmt.Sprintf("%s = $%d", column, i))
		values = append(values, value)
		i++
	}

	// Add ticket_id for WHERE clause
	values = append(values, ticketData.Id)

	updateSQL := fmt.Sprintf(
		"UPDATE %s SET %s WHERE ticket_id = $%d",
		p.tableName,
		strings.Join(setParts, ", "),
		i,
	)

	result, err := p.db.ExecContext(ctx, updateSQL, values...)
	if err != nil {
		log.Printf("ERROR: Failed to update ticket: %v", err)
		return false
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("ERROR: Failed to get rows affected: %v", err)
		return false
	}

	if rowsAffected == 0 {
		log.Printf("No ticket found with ID %s", ticketData.Id)
		return false
	}

	log.Printf("Updated ticket %s", ticketData.Id)
	return true
}

// DeleteTicket removes a ticket
func (p *PostgreSQLDocumentDBStorage) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First get the ticket to return it
	ticketData, exists := p.GetTicket(id, nil)
	if !exists {
		return nil, false
	}

	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE ticket_id = $1", p.tableName)

	result, err := p.db.ExecContext(ctx, deleteSQL, id)
	if err != nil {
		log.Printf("ERROR: Failed to delete ticket %s: %v", id, err)
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

	log.Printf("Deleted ticket %s", id)
	return ticketData, true
}

// ListTickets retrieves all tickets
func (p *PostgreSQLDocumentDBStorage) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	query := fmt.Sprintf("SELECT * FROM %s ORDER BY created_at DESC", p.tableName)

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tickets: %w", err)
	}
	defer rows.Close()

	return p.processRows(rows)
}

// SearchTickets performs a search with dynamic field detection
func (p *PostgreSQLDocumentDBStorage) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	return p.SearchTicketsWithProjection(request)
}

// SearchTicketsWithProjection performs a search with field projection
func (p *PostgreSQLDocumentDBStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Build WHERE clause with dynamic field detection
	whereClause, values, err := p.buildDynamicWhereClause(request.Conditions)
	if err != nil {
		return nil, fmt.Errorf("failed to build WHERE clause: %w", err)
	}

	// Build ORDER BY clause
	orderByClause := p.buildOrderByClause(request.SortFields)

	// Build SELECT clause with projection
	selectClause := p.buildSelectClause(request.ProjectedFields)

	// Build complete query
	var query string
	if whereClause != "" {
		query = fmt.Sprintf("%s FROM %s WHERE %s %s", selectClause, p.tableName, whereClause, orderByClause)
	} else {
		query = fmt.Sprintf("%s FROM %s %s", selectClause, p.tableName, orderByClause)
	}

	log.Printf("DB Query executed : %s", query)

	dbStart := time.Now()

	rows, err := p.db.QueryContext(ctx, query, values...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search query: %w", err)
	}
	defer rows.Close()

	dbLatency := time.Since(dbStart)

	log.Printf("DB Query execution time : %s", dbLatency)

	return p.processRows(rows)
}

// buildDynamicWhereClause builds WHERE clause with automatic field detection
func (p *PostgreSQLDocumentDBStorage) buildDynamicWhereClause(conditions []SearchCondition) (string, []interface{}, error) {
	if len(conditions) == 0 {
		return "", nil, nil
	}

	fixedFields := p.getFixedFields()
	var whereParts []string
	var values []interface{}
	paramIndex := 1

	for _, condition := range conditions {
		fieldName := strings.ToLower(condition.Operand)

		var clause string

		if fixedFields[fieldName] {
			// Fixed field - query column directly
			clause = p.buildFixedFieldCondition(fieldName, condition, paramIndex)
		} else {
			// Custom field - query BSON
			clause = p.buildCustomFieldCondition(condition.Operand, condition, paramIndex)
		}

		whereParts = append(whereParts, clause)
		values = append(values, condition.Value)
		paramIndex++
	}

	return strings.Join(whereParts, " AND "), values, nil
}

// buildFixedFieldCondition builds condition for fixed schema fields
func (p *PostgreSQLDocumentDBStorage) buildFixedFieldCondition(fieldName string, condition SearchCondition, paramIndex int) string {
	switch strings.ToLower(condition.Operator) {
	case "eq", "=":
		return fmt.Sprintf("%s = $%d", fieldName, paramIndex)
	case "ne", "!=":
		return fmt.Sprintf("%s != $%d", fieldName, paramIndex)
	case "gt", ">":
		return fmt.Sprintf("%s > $%d", fieldName, paramIndex)
	case "gte", ">=":
		return fmt.Sprintf("%s >= $%d", fieldName, paramIndex)
	case "lt", "<":
		return fmt.Sprintf("%s < $%d", fieldName, paramIndex)
	case "lte", "<=":
		return fmt.Sprintf("%s <= $%d", fieldName, paramIndex)
	case "contains", "like":
		return fmt.Sprintf("%s ILIKE $%d", fieldName, paramIndex)
	case "begins_with", "startswith":
		return fmt.Sprintf("%s ILIKE $%d", fieldName, paramIndex)
	default:
		return fmt.Sprintf("%s = $%d", fieldName, paramIndex)
	}
}

// buildCustomFieldCondition builds condition for custom BSON fields
func (p *PostgreSQLDocumentDBStorage) buildCustomFieldCondition(fieldName string, condition SearchCondition, paramIndex int) string {
	switch strings.ToLower(condition.Operator) {
	case "eq", "=":
		return fmt.Sprintf("custom_data->>'%s' = $%d", fieldName, paramIndex)
	case "ne", "!=":
		return fmt.Sprintf("custom_data->>'%s' != $%d", fieldName, paramIndex)
	case "gt", ">":
		return fmt.Sprintf("(custom_data->>'%s')::numeric > $%d", fieldName, paramIndex)
	case "gte", ">=":
		return fmt.Sprintf("(custom_data->>'%s')::numeric >= $%d", fieldName, paramIndex)
	case "lt", "<":
		return fmt.Sprintf("(custom_data->>'%s')::numeric < $%d", fieldName, paramIndex)
	case "lte", "<=":
		return fmt.Sprintf("(custom_data->>'%s')::numeric <= $%d", fieldName, paramIndex)
	case "contains", "like":
		return fmt.Sprintf("custom_data->>'%s' ILIKE $%d", fieldName, paramIndex)
	case "begins_with", "startswith":
		return fmt.Sprintf("custom_data->>'%s' ILIKE $%d", fieldName, paramIndex)
	default:
		return fmt.Sprintf("custom_data->>'%s' = $%d", fieldName, paramIndex)
	}
}

// buildSelectClause builds SELECT clause with projection
func (p *PostgreSQLDocumentDBStorage) buildSelectClause(projectedFields []string) string {
	if len(projectedFields) == 0 {
		return "SELECT *"
	}

	fixedFields := p.getFixedFields()
	var selectParts []string

	// Always include core fields
	selectParts = append(selectParts, "id", "ticket_id", "created_at", "updated_at")

	for _, field := range projectedFields {
		fieldLower := strings.ToLower(field)
		if fixedFields[fieldLower] {
			// Include fixed field if not already included
			found := false
			for _, existing := range selectParts {
				if existing == fieldLower {
					found = true
					break
				}
			}
			if !found {
				selectParts = append(selectParts, fieldLower)
			}
		} else {
			// Add custom field extraction
			selectParts = append(selectParts, fmt.Sprintf("custom_data->>'%s' as %s", field, field))
		}
	}

	return "SELECT " + strings.Join(selectParts, ", ")
}

// buildOrderByClause builds ORDER BY clause
func (p *PostgreSQLDocumentDBStorage) buildOrderByClause(sortFields []SortField) string {
	if len(sortFields) == 0 {
		return "ORDER BY created_at DESC"
	}

	fixedFields := p.getFixedFields()
	var orderParts []string

	for _, sortField := range sortFields {
		direction := "ASC"
		if strings.ToLower(sortField.Order) == "desc" {
			direction = "DESC"
		}

		fieldLower := strings.ToLower(sortField.Field)
		if fixedFields[fieldLower] {
			orderParts = append(orderParts, fmt.Sprintf("%s %s", fieldLower, direction))
		} else {
			// Sort by custom field (try numeric first, fall back to text)
			orderParts = append(orderParts,
				fmt.Sprintf("COALESCE((custom_data->>'%s')::numeric, 0) %s, custom_data->>'%s' %s",
					sortField.Field, direction, sortField.Field, direction))
		}
	}

	if len(orderParts) == 0 {
		return "ORDER BY created_at DESC"
	}

	return "ORDER BY " + strings.Join(orderParts, ", ")
}

// processRows processes query result rows
func (p *PostgreSQLDocumentDBStorage) processRows(rows *sql.Rows) ([]*ticketpb.TicketData, error) {

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	var valuesFinal [][]interface{}

	dbStart := time.Now()

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

		valuesFinal = append(valuesFinal, values)

	}

	dbLatency := time.Since(dbStart)

	log.Printf("DB Query scan time : %s", dbLatency)

	tickets := make([]*ticketpb.TicketData, len(valuesFinal))

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	// Convert to map
	rowMap := make(map[string]interface{})
	var customDataBytes []byte

	for i, values := range valuesFinal {

		for i, column := range columns {
			if column == "custom_data" {
				if values[i] != nil {
					switch v := values[i].(type) {
					case []byte:
						customDataBytes = v
					case string:
						customDataBytes = []byte(v)
					}
				}
			} else {
				rowMap[column] = values[i]
			}
		}

		tickets[i] = p.hybridDataToProtobuf(rowMap, customDataBytes)
	}

	log.Printf("Processed %d tickets", len(tickets))
	return tickets, nil
}

// Close closes the database connection
func (p *PostgreSQLDocumentDBStorage) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}
