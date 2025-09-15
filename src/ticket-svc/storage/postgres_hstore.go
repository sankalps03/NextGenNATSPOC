package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// PostgreSQLHstoreStorage implements ticket storage using PostgreSQL with hstore extension
// Uses hstore for flexible field storage with core metadata as regular columns
type PostgreSQLHstoreStorage struct {
	db        *sql.DB
	tableName string
}

// NewPostgreSQLHstoreStorage creates a new PostgreSQL hstore storage instance
func NewPostgreSQLHstoreStorage(ctx context.Context, tableName, connectionString string) (*PostgreSQLHstoreStorage, error) {
	log.Printf("Initializing PostgreSQL Hstore storage with table: %s", tableName)

	// Open database connection
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to open PostgreSQL connection: %w", err)
	}

	// Test the connection
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping PostgreSQL database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	log.Printf("PostgreSQL Hstore connection established successfully")

	storage := &PostgreSQLHstoreStorage{
		db:        db,
		tableName: tableName,
	}

	// Ensure the table exists
	if err := storage.ensureTableExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure table exists: %w", err)
	}

	return storage, nil
}

// generateTicketID generates a unique ticket ID if not provided
func (p *PostgreSQLHstoreStorage) generateTicketID() string {
	return fmt.Sprintf("TKT-%d", time.Now().UnixNano()/1000000)
}

// ensureTableExists ensures that the hstore tickets table exists
func (p *PostgreSQLHstoreStorage) ensureTableExists(ctx context.Context) error {
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
		// Create the table
		if err := p.createTableIfNotExists(ctx); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
		log.Printf("Created new PostgreSQL Hstore table: %s", p.tableName)
	} else {
		log.Printf("Found existing PostgreSQL Hstore table: %s", p.tableName)
	}

	return nil
}

// loadSchemaFromFile loads hstore SQL schema from the database/postgresql directory
func (p *PostgreSQLHstoreStorage) loadSchemaFromFile() (string, error) {
	// Try to find the hstore schema file in common locations
	possiblePaths := []string{
		"database/postgresql/schema_hstore.sql",
		"../database/postgresql/schema_hstore.sql",
		"../../database/postgresql/schema_hstore.sql",
		"./database/postgresql/schema_hstore.sql",
	}

	var schemaContent string

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			content, readErr := ioutil.ReadFile(path)
			if readErr == nil {
				schemaContent = string(content)
				log.Printf("Loaded PostgreSQL Hstore schema from: %s", path)
				break
			}
		}
	}

	if schemaContent == "" {
		return "", fmt.Errorf("could not find schema_hstore.sql file in any of the expected locations: %v", possiblePaths)
	}

	return schemaContent, nil
}

// createTableIfNotExists creates the PostgreSQL hstore table using the schema from file
func (p *PostgreSQLHstoreStorage) createTableIfNotExists(ctx context.Context) error {
	// Load hstore schema from file
	schemaContent, err := p.loadSchemaFromFile()
	if err != nil {
		return fmt.Errorf("failed to load hstore schema file: %w", err)
	}

	// Replace table name in schema if needed
	adaptedSchema := strings.ReplaceAll(schemaContent, "tickets_hstore", p.tableName)

	// Execute the schema
	_, err = p.db.ExecContext(ctx, adaptedSchema)
	if err != nil {
		return fmt.Errorf("failed to create hstore table %s: %w", p.tableName, err)
	}

	log.Printf("Created PostgreSQL Hstore table %s", p.tableName)
	return nil
}

// protobufToHstoreRow converts a TicketData protobuf to PostgreSQL hstore row data
func protobufToHstoreRow(ticketData *ticketpb.TicketData, isUpdate bool) (map[string]interface{}, error) {
	row := make(map[string]interface{})

	// Core fields - these are managed by the application as regular columns
	row["ticket_id"] = ticketData.Id

	// Handle timestamps
	if !isUpdate {
		// For new tickets, set created_at to current time
		row["created_at"] = time.Now()
	}
	// Always update updated_at for both create and update
	row["updated_at"] = time.Now()

	// Convert all other fields to hstore format
	hstoreFields := make(map[string]string)
	for key, fieldValue := range ticketData.Fields {
		if fieldValue == nil {
			continue
		}
		// Convert FieldValue to string for hstore storage
		stringValue := convertFieldValueToString(fieldValue)
		if stringValue != "" {
			hstoreFields[key] = stringValue
		}
	}

	// Convert map to hstore string format
	hstoreString := mapToHstoreString(hstoreFields)
	row["fields"] = hstoreString

	return row, nil
}

// mapToHstoreString converts a map to PostgreSQL hstore string format
func mapToHstoreString(fields map[string]string) string {
	if len(fields) == 0 {
		return ""
	}

	var pairs []string
	for key, value := range fields {
		// Escape quotes in key and value
		escapedKey := strings.ReplaceAll(key, `"`, `\"`)
		escapedValue := strings.ReplaceAll(value, `"`, `\"`)
		pairs = append(pairs, fmt.Sprintf(`"%s"=>"%s"`, escapedKey, escapedValue))
	}

	return strings.Join(pairs, ",")
}

// hstoreRowToProtobuf converts a PostgreSQL hstore row to TicketData protobuf
func hstoreRowToProtobuf(row map[string]interface{}) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Extract core fields
	if id, ok := row["ticket_id"].(string); ok {
		ticketData.Id = id
	}

	// Parse hstore fields
	if fieldsValue, ok := row["fields"]; ok {
		if fieldsStr, ok := fieldsValue.(string); ok {
			hstoreFields := parseHstoreString(fieldsStr)
			for key, value := range hstoreFields {
				fieldValue := stringToFieldValue(value)
				if fieldValue != nil {
					ticketData.Fields[key] = fieldValue
				}
			}
		}
	}

	return ticketData
}

// parseHstoreString parses PostgreSQL hstore string format to map
func parseHstoreString(hstoreStr string) map[string]string {
	fields := make(map[string]string)

	if hstoreStr == "" {
		return fields
	}

	// Simple hstore parser - handles basic cases
	// For production, consider using a more robust parser
	pairs := strings.Split(hstoreStr, ",")
	for _, pair := range pairs {
		if strings.Contains(pair, "=>") {
			parts := strings.SplitN(pair, "=>", 2)
			if len(parts) == 2 {
				key := strings.Trim(strings.TrimSpace(parts[0]), `"`)
				value := strings.Trim(strings.TrimSpace(parts[1]), `"`)
				fields[key] = value
			}
		}
	}

	return fields
}

// CreateTicket stores a new ticket in the PostgreSQL hstore table
func (p *PostgreSQLHstoreStorage) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Generate ticket ID if not provided
	if ticketData.Id == "" {
		ticketData.Id = p.generateTicketID()
	}

	// Convert protobuf to PostgreSQL hstore row (isUpdate = false for create)
	row, err := protobufToHstoreRow(ticketData, false)
	if err != nil {
		return fmt.Errorf("failed to convert protobuf to hstore row: %w", err), nil
	}

	// Build INSERT query
	insertSQL := fmt.Sprintf(
		"INSERT INTO %s (ticket_id, created_at, updated_at, fields) VALUES ($1, $2, $3, $4) RETURNING id",
		p.tableName,
	)

	var generatedID int64
	err = p.db.QueryRowContext(ctx, insertSQL,
		row["ticket_id"], row["created_at"], row["updated_at"], row["fields"]).Scan(&generatedID)
	if err != nil {
		return fmt.Errorf("failed to create ticket in hstore table %s: %w", p.tableName, err), nil
	}

	log.Printf("Created ticket %s in hstore table %s with ID %d", ticketData.Id, p.tableName, generatedID)

	// Return the created ticket data
	result := map[string]interface{}{
		"id":         generatedID,
		"ticket_id":  ticketData.Id,
		"created_at": row["created_at"],
		"updated_at": row["updated_at"],
	}

	return nil, result
}

// GetTicket retrieves a ticket by ID from the PostgreSQL hstore table
func (p *PostgreSQLHstoreStorage) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query the ticket
	query := fmt.Sprintf("SELECT ticket_id, created_at, updated_at, fields FROM %s WHERE ticket_id = $1", p.tableName)

	row := p.db.QueryRowContext(ctx, query, id)

	var ticketID string
	var createdAt, updatedAt time.Time
	var fieldsHstore string

	err := row.Scan(&ticketID, &createdAt, &updatedAt, &fieldsHstore)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("Ticket %s not found in hstore table %s", id, p.tableName)
			return nil, false
		}
		log.Printf("ERROR: Failed to scan hstore row for ticket %s: %v", id, err)
		return nil, false
	}

	// Convert to map for processing
	rowMap := map[string]interface{}{
		"ticket_id":  ticketID,
		"created_at": createdAt,
		"updated_at": updatedAt,
		"fields":     fieldsHstore,
	}

	// Convert to protobuf
	ticketData := hstoreRowToProtobuf(rowMap)

	log.Printf("Retrieved ticket %s from hstore table %s", id, p.tableName)
	return ticketData, true
}

// UpdateTicket updates an existing ticket in the PostgreSQL hstore table
func (p *PostgreSQLHstoreStorage) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Convert protobuf to PostgreSQL hstore row (isUpdate = true for update)
	row, err := protobufToHstoreRow(ticketData, true)
	if err != nil {
		log.Printf("ERROR: Failed to convert protobuf to hstore row: %v", err)
		return false
	}

	// Build UPDATE query
	updateSQL := fmt.Sprintf(
		"UPDATE %s SET updated_at = $1, fields = $2 WHERE ticket_id = $3",
		p.tableName,
	)

	result, err := p.db.ExecContext(ctx, updateSQL, row["updated_at"], row["fields"], ticketData.Id)
	if err != nil {
		log.Printf("ERROR: Failed to update ticket %s in hstore table %s: %v", ticketData.Id, p.tableName, err)
		return false
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("ERROR: Failed to get rows affected for ticket %s: %v", ticketData.Id, err)
		return false
	}

	if rowsAffected == 0 {
		log.Printf("No rows updated for ticket %s in hstore table %s", ticketData.Id, p.tableName)
		return false
	}

	log.Printf("Updated ticket %s in hstore table %s", ticketData.Id, p.tableName)
	return true
}

// DeleteTicket removes a ticket from the PostgreSQL hstore table
func (p *PostgreSQLHstoreStorage) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First, get the ticket data before deletion
	ticketData, exists := p.GetTicket(id, nil)
	if !exists {
		log.Printf("Ticket %s not found for deletion in hstore table %s", id, p.tableName)
		return nil, false
	}

	// Delete the ticket
	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE ticket_id = $1", p.tableName)

	result, err := p.db.ExecContext(ctx, deleteSQL, id)
	if err != nil {
		log.Printf("ERROR: Failed to delete ticket %s from hstore table %s: %v", id, p.tableName, err)
		return nil, false
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("ERROR: Failed to get rows affected for deletion of ticket %s: %v", id, err)
		return nil, false
	}

	if rowsAffected == 0 {
		log.Printf("No rows deleted for ticket %s in hstore table %s", id, p.tableName)
		return nil, false
	}

	log.Printf("Deleted ticket %s from hstore table %s", id, p.tableName)
	return ticketData, true
}

// ListTickets retrieves all tickets from the PostgreSQL hstore table
func (p *PostgreSQLHstoreStorage) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query all tickets
	query := fmt.Sprintf("SELECT ticket_id, created_at, updated_at, fields FROM %s ORDER BY created_at DESC", p.tableName)

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tickets from hstore table %s: %w", p.tableName, err)
	}
	defer rows.Close()

	var tickets []*ticketpb.TicketData

	for rows.Next() {
		var ticketID string
		var createdAt, updatedAt time.Time
		var fieldsHstore string

		err := rows.Scan(&ticketID, &createdAt, &updatedAt, &fieldsHstore)
		if err != nil {
			log.Printf("ERROR: Failed to scan hstore row: %v", err)
			continue
		}

		// Convert to map for processing
		rowMap := map[string]interface{}{
			"ticket_id":  ticketID,
			"created_at": createdAt,
			"updated_at": updatedAt,
			"fields":     fieldsHstore,
		}

		// Convert to protobuf
		ticketData := hstoreRowToProtobuf(rowMap)
		tickets = append(tickets, ticketData)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over hstore rows: %w", err)
	}

	log.Printf("Listed %d tickets from hstore table %s", len(tickets), p.tableName)
	return tickets, nil
}

// SearchTickets searches for tickets based on conditions using hstore operators
func (p *PostgreSQLHstoreStorage) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build WHERE clause for hstore queries
	whereClause, values, err := p.buildHstoreWhereClause(request.Conditions)
	if err != nil {
		return nil, fmt.Errorf("failed to build hstore WHERE clause: %w", err)
	}

	// Build ORDER BY clause
	orderByClause := p.buildHstoreOrderByClause(request.SortFields)

	// Build complete query
	var query string
	if whereClause != "" {
		query = fmt.Sprintf("SELECT ticket_id, created_at, updated_at, fields FROM %s WHERE %s %s",
			p.tableName, whereClause, orderByClause)
	} else {
		query = fmt.Sprintf("SELECT ticket_id, created_at, updated_at, fields FROM %s %s",
			p.tableName, orderByClause)
	}

	// Execute query
	rows, err := p.db.QueryContext(ctx, query, values...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute hstore search query: %w", err)
	}
	defer rows.Close()

	var tickets []*ticketpb.TicketData

	for rows.Next() {
		var ticketID string
		var createdAt, updatedAt time.Time
		var fieldsHstore string

		err := rows.Scan(&ticketID, &createdAt, &updatedAt, &fieldsHstore)
		if err != nil {
			log.Printf("ERROR: Failed to scan hstore search row: %v", err)
			continue
		}

		// Convert to map for processing
		rowMap := map[string]interface{}{
			"ticket_id":  ticketID,
			"created_at": createdAt,
			"updated_at": updatedAt,
			"fields":     fieldsHstore,
		}

		// Convert to protobuf
		ticketData := hstoreRowToProtobuf(rowMap)
		tickets = append(tickets, ticketData)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over hstore search rows: %w", err)
	}

	log.Printf("Found %d tickets matching search criteria in hstore table %s", len(tickets), p.tableName)
	return tickets, nil
}

// SearchTicketsWithProjection searches for tickets with optional field projection using hstore operators
func (p *PostgreSQLHstoreStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build SELECT clause with projection
	selectClause := "ticket_id, created_at, updated_at"
	if len(request.ProjectedFields) > 0 {
		// Add specific hstore field projections
		for _, field := range request.ProjectedFields {
			selectClause += fmt.Sprintf(", fields->'%s' AS %s", field, field)
		}
	} else {
		// Include all fields
		selectClause += ", fields"
	}

	// Build WHERE clause for hstore queries
	whereClause, values, err := p.buildHstoreWhereClause(request.Conditions)
	if err != nil {
		return nil, fmt.Errorf("failed to build hstore WHERE clause: %w", err)
	}

	// Build ORDER BY clause
	orderByClause := p.buildHstoreOrderByClause(request.SortFields)

	// Build complete query
	var query string
	if whereClause != "" {
		query = fmt.Sprintf("SELECT %s FROM %s WHERE %s %s",
			selectClause, p.tableName, whereClause, orderByClause)
	} else {
		query = fmt.Sprintf("SELECT %s FROM %s %s",
			selectClause, p.tableName, orderByClause)
	}

	// Execute query
	rows, err := p.db.QueryContext(ctx, query, values...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute hstore projection query: %w", err)
	}
	defer rows.Close()

	// Get column names for dynamic scanning
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get column names: %w", err)
	}

	var tickets []*ticketpb.TicketData

	for rows.Next() {
		// Create slice to hold values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan the row
		if err := rows.Scan(valuePtrs...); err != nil {
			log.Printf("ERROR: Failed to scan hstore projection row: %v", err)
			continue
		}

		// Convert to map
		rowMap := make(map[string]interface{})
		for i, column := range columns {
			rowMap[column] = values[i]
		}

		// Convert to protobuf with projection handling
		ticketData := p.hstoreProjectionRowToProtobuf(rowMap, request.ProjectedFields)
		tickets = append(tickets, ticketData)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over hstore projection rows: %w", err)
	}

	log.Printf("Found %d tickets with projection in hstore table %s", len(tickets), p.tableName)
	return tickets, nil
}

// buildHstoreWhereClause builds WHERE clause for hstore queries
func (p *PostgreSQLHstoreStorage) buildHstoreWhereClause(conditions []SearchCondition) (string, []interface{}, error) {
	if len(conditions) == 0 {
		return "", nil, nil
	}

	var clauses []string
	var values []interface{}
	paramIndex := 1

	for _, condition := range conditions {
		var clause string

		// Handle core metadata fields differently from hstore fields
		if condition.Operand == "ticket_id" || condition.Operand == "created_at" || condition.Operand == "updated_at" {
			// Core fields - use regular column syntax
			switch condition.Operator {
			case "eq":
				clause = fmt.Sprintf("%s = $%d", condition.Operand, paramIndex)
			case "ne":
				clause = fmt.Sprintf("%s != $%d", condition.Operand, paramIndex)
			case "gt":
				clause = fmt.Sprintf("%s > $%d", condition.Operand, paramIndex)
			case "lt":
				clause = fmt.Sprintf("%s < $%d", condition.Operand, paramIndex)
			case "gte":
				clause = fmt.Sprintf("%s >= $%d", condition.Operand, paramIndex)
			case "lte":
				clause = fmt.Sprintf("%s <= $%d", condition.Operand, paramIndex)
			case "contains":
				clause = fmt.Sprintf("%s ILIKE $%d", condition.Operand, paramIndex)
				condition.Value = fmt.Sprintf("%%%v%%", condition.Value)
			default:
				return "", nil, fmt.Errorf("unsupported operator for core field %s: %s", condition.Operand, condition.Operator)
			}
		} else {
			// Hstore fields - use hstore operators
			switch condition.Operator {
			case "eq":
				clause = fmt.Sprintf("fields->'%s' = $%d", condition.Operand, paramIndex)
			case "ne":
				clause = fmt.Sprintf("fields->'%s' != $%d", condition.Operand, paramIndex)
			case "gt":
				clause = fmt.Sprintf("(fields->'%s')::numeric > $%d", condition.Operand, paramIndex)
			case "lt":
				clause = fmt.Sprintf("(fields->'%s')::numeric < $%d", condition.Operand, paramIndex)
			case "gte":
				clause = fmt.Sprintf("(fields->'%s')::numeric >= $%d", condition.Operand, paramIndex)
			case "lte":
				clause = fmt.Sprintf("(fields->'%s')::numeric <= $%d", condition.Operand, paramIndex)
			case "contains":
				clause = fmt.Sprintf("fields->'%s' ILIKE $%d", condition.Operand, paramIndex)
				condition.Value = fmt.Sprintf("%%%v%%", condition.Value)
			case "begins_with":
				clause = fmt.Sprintf("fields->'%s' ILIKE $%d", condition.Operand, paramIndex)
				condition.Value = fmt.Sprintf("%v%%", condition.Value)
			default:
				return "", nil, fmt.Errorf("unsupported operator for hstore field %s: %s", condition.Operand, condition.Operator)
			}
		}

		clauses = append(clauses, clause)
		values = append(values, condition.Value)
		paramIndex++
	}

	whereClause := strings.Join(clauses, " AND ")
	return whereClause, values, nil
}

// buildHstoreOrderByClause builds ORDER BY clause for hstore queries
func (p *PostgreSQLHstoreStorage) buildHstoreOrderByClause(sortFields []SortField) string {
	if len(sortFields) == 0 {
		return "ORDER BY created_at DESC"
	}

	var orderClauses []string
	for _, sortField := range sortFields {
		direction := "ASC"
		if strings.ToUpper(sortField.Order) == "DESC" {
			direction = "DESC"
		}

		var orderClause string
		// Handle core metadata fields differently from hstore fields
		if sortField.Field == "ticket_id" || sortField.Field == "created_at" || sortField.Field == "updated_at" {
			orderClause = fmt.Sprintf("%s %s", sortField.Field, direction)
		} else {
			// For hstore fields, try to cast to numeric if possible, otherwise use text
			orderClause = fmt.Sprintf("CASE WHEN fields->'%s' ~ '^[0-9]+$' THEN (fields->'%s')::numeric ELSE 0 END %s, fields->'%s' %s",
				sortField.Field, sortField.Field, direction, sortField.Field, direction)
		}
		orderClauses = append(orderClauses, orderClause)
	}

	return "ORDER BY " + strings.Join(orderClauses, ", ")
}

// hstoreProjectionRowToProtobuf converts a projected hstore row to TicketData protobuf
func (p *PostgreSQLHstoreStorage) hstoreProjectionRowToProtobuf(row map[string]interface{}, projectedFields []string) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Extract core fields
	if id, ok := row["ticket_id"].(string); ok {
		ticketData.Id = id
	}

	if len(projectedFields) > 0 {
		// Handle projected fields
		for _, field := range projectedFields {
			if value, ok := row[field]; ok && value != nil {
				var strValue string
				if sv, ok := value.(string); ok {
					strValue = sv
				} else {
					strValue = fmt.Sprintf("%v", value)
				}
				fieldValue := stringToFieldValue(strValue)
				if fieldValue != nil {
					ticketData.Fields[field] = fieldValue
				}
			}
		}
	} else {
		// Handle full hstore fields
		if fieldsValue, ok := row["fields"]; ok {
			if fieldsStr, ok := fieldsValue.(string); ok {
				hstoreFields := parseHstoreString(fieldsStr)
				for key, value := range hstoreFields {
					fieldValue := stringToFieldValue(value)
					if fieldValue != nil {
						ticketData.Fields[key] = fieldValue
					}
				}
			}
		}
	}

	return ticketData
}

// convertFieldValueToString converts a protobuf FieldValue to string for hstore storage
func convertFieldValueToString(fieldValue *ticketpb.FieldValue) string {
	if fieldValue == nil {
		return ""
	}

	switch v := fieldValue.Value.(type) {
	case *ticketpb.FieldValue_StringValue:
		return v.StringValue
	case *ticketpb.FieldValue_IntValue:
		return fmt.Sprintf("%d", v.IntValue)
	case *ticketpb.FieldValue_DoubleValue:
		return fmt.Sprintf("%g", v.DoubleValue)
	case *ticketpb.FieldValue_BoolValue:
		return fmt.Sprintf("%t", v.BoolValue)
	case *ticketpb.FieldValue_BytesValue:
		return string(v.BytesValue)
	case *ticketpb.FieldValue_StringArray:
		if v.StringArray != nil {
			return strings.Join(v.StringArray.Values, ",")
		}
		return ""
	default:
		return ""
	}
}

// stringToFieldValue converts a string back to protobuf FieldValue
// This function attempts to infer the original type from the string representation
func stringToFieldValue(value string) *ticketpb.FieldValue {
	if value == "" {
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: ""}}
	}

	// Try to parse as boolean
	if value == "true" {
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_BoolValue{BoolValue: true}}
	}
	if value == "false" {
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_BoolValue{BoolValue: false}}
	}

	// Try to parse as integer
	if intVal, err := fmt.Sscanf(value, "%d", new(int64)); err == nil && intVal == 1 {
		var parsedInt int64
		fmt.Sscanf(value, "%d", &parsedInt)
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_IntValue{IntValue: parsedInt}}
	}

	// Try to parse as float
	if floatVal, err := fmt.Sscanf(value, "%g", new(float64)); err == nil && floatVal == 1 {
		var parsedFloat float64
		fmt.Sscanf(value, "%g", &parsedFloat)
		// Check if it's actually an integer value
		if parsedFloat == float64(int64(parsedFloat)) {
			return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_IntValue{IntValue: int64(parsedFloat)}}
		}
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: parsedFloat}}
	}

	// Check if it looks like a comma-separated array
	if strings.Contains(value, ",") {
		values := strings.Split(value, ",")
		for i, v := range values {
			values[i] = strings.TrimSpace(v)
		}
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringArray{
			StringArray: &ticketpb.StringArray{Values: values},
		}}
	}

	// Default to string
	return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: value}}
}

// Close closes the database connection
func (p *PostgreSQLHstoreStorage) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}
