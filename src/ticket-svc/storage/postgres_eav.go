package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// Data type constants for EAV storage
const (
	DataTypeString  = 1 // String/text data
	DataTypeInt     = 2 // Integer/timestamp data
	DataTypeBoolean = 3 // Boolean data
)

// FieldMetadata defines the data type and properties of a field
type FieldMetadata struct {
	AttributeID int // Numeric ID for the field
	DataType    int // 1=string, 2=int, 3=boolean
	Required    bool
	Description string
}

// PostgreSQLEAVStorage implements ticket storage using PostgreSQL EAV design
// Uses a single EAV table for all tickets
type PostgreSQLEAVStorage struct {
	db              *sql.DB
	tableName       string
	tableMutex      sync.RWMutex             // synchronizes table creation operations
	fieldMetadata   map[string]FieldMetadata // field_name -> metadata
	attributeToName map[int]string           // attribute_id -> field_name
	nameToAttribute map[string]int           // field_name -> attribute_id
}

// NewPostgreSQLEAVStorage creates a new PostgreSQL EAV storage instance
func NewPostgreSQLEAVStorage(ctx context.Context, tableName, connectionString string) (*PostgreSQLEAVStorage, error) {
	log.Printf("Initializing PostgreSQL EAV storage with table: %s", tableName)

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

	log.Printf("PostgreSQL EAV connection established successfully")

	storage := &PostgreSQLEAVStorage{
		db:              db,
		tableName:       tableName,
		fieldMetadata:   make(map[string]FieldMetadata),
		attributeToName: make(map[int]string),
		nameToAttribute: make(map[string]int),
	}

	// Initialize field metadata and attribute mappings
	storage.initializeFieldMetadata()

	// Create the EAV table if it doesn't exist
	if err := storage.createTableIfNotExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to create EAV table: %w", err)
	}

	return storage, nil
}

// initializeFieldMetadata creates the in-memory field definitions and attribute mappings
func (p *PostgreSQLEAVStorage) initializeFieldMetadata() {
	// Define all fields with their attribute IDs, data types, and metadata
	fieldDefinitions := []struct {
		name        string
		attributeID int
		dataType    int
		required    bool
		description string
	}{

		// Core fields
		{"ticket_id", 1, DataTypeString, true, "Unique ticket identifier"},
		{"created_at", 2, DataTypeInt, false, "Creation timestamp"},

		// User and assignment fields (BIGINT)
		{"updatedbyid", 3, DataTypeInt, false, "User ID field"},
		{"createdbyid", 4, DataTypeInt, false, "User ID field"},
		{"removedbyid", 5, DataTypeInt, false, "User ID field"},
		{"requesterid", 6, DataTypeInt, false, "User ID field"},
		{"technicianid", 7, DataTypeInt, false, "User ID field"},
		{"closedby", 8, DataTypeInt, false, "User ID field"},
		{"resolvedby", 9, DataTypeInt, false, "User ID field"},

		// Timestamp fields (BIGINT - Unix timestamps in milliseconds)
		{"updatedtime", 10, DataTypeInt, false, "Timestamp field"},
		{"createdtime", 11, DataTypeInt, false, "Timestamp field"},
		{"removedtime", 12, DataTypeInt, false, "Timestamp field"},
		{"dueby", 13, DataTypeInt, false, "Timestamp field"},
		{"firstresponsetime", 14, DataTypeInt, false, "Timestamp field"},
		{"lastclosedtime", 15, DataTypeInt, false, "Timestamp field"},
		{"lastopenedtime", 16, DataTypeInt, false, "Timestamp field"},
		{"lastresolvedtime", 17, DataTypeInt, false, "Timestamp field"},
		{"lastviolationtime", 18, DataTypeInt, false, "Timestamp field"},
		{"olddueby", 19, DataTypeInt, false, "Timestamp field"},
		{"oldresponsedue", 20, DataTypeInt, false, "Timestamp field"},
		{"resolutionescalationtime", 21, DataTypeInt, false, "Timestamp field"},
		{"responsedue", 22, DataTypeInt, false, "Timestamp field"},
		{"responseescalationtime", 23, DataTypeInt, false, "Timestamp field"},
		{"statuschangedtime", 24, DataTypeInt, false, "Timestamp field"},
		{"groupchangedtime", 25, DataTypeInt, false, "Timestamp field"},
		{"lastolaviolationtime", 26, DataTypeInt, false, "Timestamp field"},
		{"oladueby", 27, DataTypeInt, false, "Timestamp field"},
		{"oldoladueby", 28, DataTypeInt, false, "Timestamp field"},
		{"askfeedbackdate", 29, DataTypeInt, false, "Timestamp field"},
		{"firstfeedbackdate", 30, DataTypeInt, false, "Timestamp field"},
		{"olaescalationtime", 31, DataTypeInt, false, "Timestamp field"},
		{"lastucviolationtime", 32, DataTypeInt, false, "Timestamp field"},
		{"olducdueby", 33, DataTypeInt, false, "Timestamp field"},
		{"ucdueby", 34, DataTypeInt, false, "Timestamp field"},
		{"ucescalationtime", 35, DataTypeInt, false, "Timestamp field"},
		{"lastapproveddate", 36, DataTypeInt, false, "Timestamp field"},

		// Text fields (VARCHAR/TEXT)
		{"name", 37, DataTypeString, false, "Text field"},
		{"oobtype", 38, DataTypeString, false, "Text field"},
		{"description", 39, DataTypeString, false, "Text field"},
		{"originaldescription", 40, DataTypeString, false, "Text field"},
		{"subject", 41, DataTypeString, false, "Text field"},
		{"callfrom", 42, DataTypeString, false, "Text field"},
		{"emailreadconfigemail", 43, DataTypeString, false, "Text field"},

		// Boolean fields
		{"removed", 44, DataTypeBoolean, false, "Boolean flag"},
		{"duetimemanuallyupdated", 45, DataTypeBoolean, false, "Boolean flag"},
		{"reopened", 46, DataTypeBoolean, false, "Boolean flag"},
		{"responsedueviolated", 47, DataTypeBoolean, false, "Boolean flag"},
		{"slaviolated", 48, DataTypeBoolean, false, "Boolean flag"},
		{"purchaserequest", 49, DataTypeBoolean, false, "Boolean flag"},
		{"spam", 50, DataTypeBoolean, false, "Boolean flag"},
		{"viprequest", 51, DataTypeBoolean, false, "Boolean flag"},
		{"olaviolated", 52, DataTypeBoolean, false, "Boolean flag"},
		{"ucviolated", 53, DataTypeBoolean, false, "Boolean flag"},
		{"migrated", 54, DataTypeBoolean, false, "Boolean flag"},

		// Category and classification fields (BIGINT)
		{"categoryid", 55, DataTypeInt, false, "Category/ID field"},
		{"departmentid", 56, DataTypeInt, false, "Category/ID field"},
		{"groupid", 57, DataTypeInt, false, "Category/ID field"},
		{"impactid", 58, DataTypeInt, false, "Category/ID field"},
		{"locationid", 59, DataTypeInt, false, "Category/ID field"},
		{"priorityid", 60, DataTypeInt, false, "Category/ID field"},
		{"statusid", 61, DataTypeInt, false, "Category/ID field"},
		{"urgencyid", 62, DataTypeInt, false, "Category/ID field"},
		{"violatedslaid", 63, DataTypeInt, false, "Category/ID field"},
		{"servicecatalogid", 64, DataTypeInt, false, "Category/ID field"},
		{"sourceid", 65, DataTypeInt, false, "Category/ID field"},
		{"requesttype", 66, DataTypeInt, false, "Category/ID field"},
		{"suggestedcategoryid", 67, DataTypeInt, false, "Category/ID field"},
		{"suggestedgroupid", 68, DataTypeInt, false, "Category/ID field"},
		{"companyid", 69, DataTypeInt, false, "Category/ID field"},
		{"vendorid", 70, DataTypeInt, false, "Category/ID field"},
		{"violateducid", 71, DataTypeInt, false, "Category/ID field"},
		{"transitionmodelid", 72, DataTypeInt, false, "Category/ID field"},
		{"messengerconfigid", 73, DataTypeInt, false, "Category/ID field"},

		// Approval and workflow fields (INTEGER)
		{"approvalstatus", 74, DataTypeInt, false, "Workflow field"},
		{"approvaltype", 75, DataTypeInt, false, "Workflow field"},
		{"resolutionduelevel", 76, DataTypeInt, false, "Workflow field"},
		{"responseduelevel", 77, DataTypeInt, false, "Workflow field"},
		{"supportlevel", 78, DataTypeInt, false, "Workflow field"},
		{"oladuelevel", 79, DataTypeInt, false, "Workflow field"},
		{"ucduelevel", 80, DataTypeInt, false, "Workflow field"},

		// Duration and time tracking fields (BIGINT)
		{"totalonholdduration", 81, DataTypeInt, false, "Duration field"},
		{"totalresolutiontime", 82, DataTypeInt, false, "Duration field"},
		{"totalslapausetime", 83, DataTypeInt, false, "Duration field"},
		{"totalworkingtime", 84, DataTypeInt, false, "Duration field"},
		{"totaluconholdduration", 85, DataTypeInt, false, "Duration field"},
		{"totalucpausetime", 86, DataTypeInt, false, "Duration field"},
		{"totalucworkingtime", 87, DataTypeInt, false, "Duration field"},
		{"totalucresolutiontime", 88, DataTypeInt, false, "Duration field"},

		// Configuration and template fields (BIGINT)
		{"templateid", 89, DataTypeInt, false, "Configuration field"},
		{"emailreadconfigid", 90, DataTypeInt, false, "Configuration field"},
	}

	// Build the mappings
	for _, field := range fieldDefinitions {
		p.fieldMetadata[field.name] = FieldMetadata{
			AttributeID: field.attributeID,
			DataType:    field.dataType,
			Required:    field.required,
			Description: field.description,
		}
		p.attributeToName[field.attributeID] = field.name
		p.nameToAttribute[field.name] = field.attributeID
	}
}

// generateTicketID generates a unique ticket ID if not provided
func (p *PostgreSQLEAVStorage) generateTicketID() string {
	return fmt.Sprintf("TKT-%d", time.Now().UnixNano()/1000000)
}

// No tenant-specific table methods needed - using single table

// loadSchemaFromFile loads EAV SQL schema from the database/postgresql directory
func (p *PostgreSQLEAVStorage) loadSchemaFromFile() (string, error) {
	// Try to find the EAV schema file in common locations
	possiblePaths := []string{
		"database/postgresql/schema_eav.sql",
		"../database/postgresql/schema_eav.sql",
		"../../database/postgresql/schema_eav.sql",
		"./database/postgresql/schema_eav.sql",
	}

	var schemaContent string

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			content, readErr := ioutil.ReadFile(path)
			if readErr == nil {
				schemaContent = string(content)
				log.Printf("Loaded PostgreSQL EAV schema from: %s", path)
				break
			}
		}
	}

	if schemaContent == "" {
		return "", fmt.Errorf("could not find EAV schema file in any of the expected locations")
	}

	return schemaContent, nil
}

// createTableIfNotExists creates the PostgreSQL EAV table using the schema from file
func (p *PostgreSQLEAVStorage) createTableIfNotExists(ctx context.Context) error {
	// Load EAV schema from file
	schemaContent, err := p.loadSchemaFromFile()
	if err != nil {
		return fmt.Errorf("failed to load EAV schema file: %w", err)
	}

	// Replace table name in schema if needed
	adaptedSchema := strings.ReplaceAll(schemaContent, "ticket_eav", p.tableName)

	// Execute the schema
	_, err = p.db.ExecContext(ctx, adaptedSchema)
	if err != nil {
		return fmt.Errorf("failed to create EAV table %s: %w", p.tableName, err)
	}

	log.Printf("Created PostgreSQL EAV table %s", p.tableName)
	return nil
}

// No need for tenant-specific schema adaptation

// convertTicketToEAVRows converts a TicketData protobuf to EAV rows
func (p *PostgreSQLEAVStorage) convertTicketToEAVRows(ticketData *ticketpb.TicketData) ([]map[string]interface{}, error) {
	var rows []map[string]interface{}

	// Process all fields from the protobuf Fields map
	for fieldName, fieldValue := range ticketData.Fields {
		metadata, exists := p.fieldMetadata[fieldName]
		if !exists {
			log.Printf("Warning: Unknown field %s, treating as string", fieldName)
			metadata = FieldMetadata{AttributeID: 999, DataType: DataTypeString, Required: false, Description: "Unknown field"}
		}

		row := map[string]interface{}{
			"entity_id":    ticketData.Id,
			"attribute_id": metadata.AttributeID,
			"datatype":     metadata.DataType,
		}

		// Set the appropriate value column based on data type
		switch metadata.DataType {
		case DataTypeString:
			if fieldValue.GetStringValue() != "" {
				row["string_value"] = fieldValue.GetStringValue()
			}
		case DataTypeInt:
			if _, ok := fieldValue.Value.(*ticketpb.FieldValue_IntValue); ok {
				row["int_value"] = fieldValue.GetIntValue()
			} else if _, ok := fieldValue.Value.(*ticketpb.FieldValue_DoubleValue); ok {
				row["int_value"] = int64(fieldValue.GetDoubleValue())
			}
		case DataTypeBoolean:
			row["boolean_value"] = fieldValue.GetBoolValue()
		default:
			return nil, fmt.Errorf("unsupported data type %d for field %s", metadata.DataType, fieldName)
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// convertEAVRowsToTicket reconstructs a TicketData from EAV rows
func (p *PostgreSQLEAVStorage) convertEAVRowsToTicket(rows []map[string]interface{}) (*ticketpb.TicketData, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("no EAV rows provided")
	}

	// Initialize ticket data
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Extract entity_id from first row
	if entityID, ok := rows[0]["entity_id"].(string); ok {
		ticketData.Id = entityID
	}

	// Process each EAV row
	for _, row := range rows {
		attributeID, ok := row["attribute_id"].(int)
		if !ok {
			// Try to handle as int16 (SMALLINT from database)
			if attrID16, ok := row["attribute_id"].(int16); ok {
				attributeID = int(attrID16)
			} else {
				continue
			}
		}

		// Get field name from attribute ID
		attributeName, exists := p.attributeToName[attributeID]
		if !exists {
			log.Printf("Warning: Unknown attribute ID %d", attributeID)
			continue
		}

		datatype, ok := row["datatype"].(int)
		if !ok {
			// Try to handle legacy string datatype for backward compatibility
			if datatypeStr, ok := row["datatype"].(string); ok {
				switch datatypeStr {
				case "string":
					datatype = DataTypeString
				case "int":
					datatype = DataTypeInt
				case "boolean":
					datatype = DataTypeBoolean
				default:
					continue
				}
			} else {
				continue
			}
		}

		fieldValue := &ticketpb.FieldValue{}

		// Extract value based on data type
		switch datatype {
		case DataTypeString:
			if stringVal, exists := row["string_value"]; exists && stringVal != nil {
				if strVal, ok := stringVal.(string); ok {
					fieldValue.Value = &ticketpb.FieldValue_StringValue{StringValue: strVal}
				}
			}
		case DataTypeInt:
			if intVal, exists := row["int_value"]; exists && intVal != nil {
				if val, ok := intVal.(int64); ok {
					fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: val}
				}
			}
		case DataTypeBoolean:
			if boolVal, exists := row["boolean_value"]; exists && boolVal != nil {
				if val, ok := boolVal.(bool); ok {
					fieldValue.Value = &ticketpb.FieldValue_BoolValue{BoolValue: val}
				}
			}
		}

		ticketData.Fields[attributeName] = fieldValue
	}

	return ticketData, nil
}

// CreateTicket stores a new ticket in the PostgreSQL EAV table
func (p *PostgreSQLEAVStorage) CreateTicket(tenant string, ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Generate ticket ID if not provided
	if ticketData.Id == "" {
		ticketData.Id = p.generateTicketID()
	}

	// Convert ticket to EAV rows
	eavRows, err := p.convertTicketToEAVRows(ticketData)
	if err != nil {
		return fmt.Errorf("failed to convert ticket to EAV rows: %w", err), nil
	}

	// Begin transaction
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err), nil
	}
	defer tx.Rollback()

	var firstInsertedID int64

	// Insert each EAV row
	for i, row := range eavRows {
		columns := make([]string, 0, len(row))
		placeholders := make([]string, 0, len(row))
		values := make([]interface{}, 0, len(row))

		j := 1
		for column, value := range row {
			columns = append(columns, column)
			placeholders = append(placeholders, fmt.Sprintf("$%d", j))
			values = append(values, value)
			j++
		}

		insertSQL := fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s) RETURNING id",
			p.tableName,
			strings.Join(columns, ", "),
			strings.Join(placeholders, ", "),
		)

		var insertedID int64
		err = tx.QueryRowContext(ctx, insertSQL, values...).Scan(&insertedID)
		if err != nil {
			return fmt.Errorf("failed to insert EAV row %d: %w", i, err), nil
		}

		if i == 0 {
			firstInsertedID = insertedID
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err), nil
	}

	log.Printf("Created ticket %s in EAV table %s with %d rows", ticketData.Id, p.tableName, len(eavRows))

	result := map[string]interface{}{
		"id":        firstInsertedID,
		"ticket_id": ticketData.Id,
		"rows":      len(eavRows),
	}

	return nil, result
}

// GetTicket retrieves a ticket by ID from the PostgreSQL EAV table
func (p *PostgreSQLEAVStorage) GetTicket(tenant, id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query all EAV rows for this ticket
	query := fmt.Sprintf(`
		SELECT entity_id, attribute_id, string_value, int_value, boolean_value, datatype
		FROM %s
		WHERE entity_id = $1
		ORDER BY attribute_id
	`, p.tableName)

	rows, err := p.db.QueryContext(ctx, query, id)
	if err != nil {
		log.Printf("ERROR: Failed to query EAV rows for ticket %s: %v", id, err)
		return nil, false
	}
	defer rows.Close()

	var eavRows []map[string]interface{}

	for rows.Next() {
		var entityID string
		var attributeID int16
		var datatype int16
		var stringValue sql.NullString
		var intValue sql.NullInt64
		var booleanValue sql.NullBool

		err := rows.Scan(&entityID, &attributeID, &stringValue, &intValue, &booleanValue, &datatype)
		if err != nil {
			log.Printf("ERROR: Failed to scan EAV row: %v", err)
			continue
		}

		row := map[string]interface{}{
			"entity_id":    entityID,
			"attribute_id": int(attributeID),
			"datatype":     int(datatype),
		}

		if stringValue.Valid {
			row["string_value"] = stringValue.String
		}
		if intValue.Valid {
			row["int_value"] = intValue.Int64
		}
		if booleanValue.Valid {
			row["boolean_value"] = booleanValue.Bool
		}

		eavRows = append(eavRows, row)
	}

	if len(eavRows) == 0 {
		return nil, false
	}

	// Convert EAV rows back to ticket
	ticketData, err := p.convertEAVRowsToTicket(eavRows)
	if err != nil {
		log.Printf("ERROR: Failed to convert EAV rows to ticket: %v", err)
		return nil, false
	}

	return ticketData, true
}

// UpdateTicket updates an existing ticket in the PostgreSQL EAV table
func (p *PostgreSQLEAVStorage) UpdateTicket(tenant string, ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Convert ticket to EAV rows
	eavRows, err := p.convertTicketToEAVRows(ticketData)
	if err != nil {
		log.Printf("ERROR: Failed to convert ticket to EAV rows: %v", err)
		return false
	}

	// Begin transaction
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("ERROR: Failed to begin transaction: %v", err)
		return false
	}
	defer tx.Rollback()

	// Delete existing EAV rows for this ticket
	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE entity_id = $1", p.tableName)
	_, err = tx.ExecContext(ctx, deleteSQL, ticketData.Id)
	if err != nil {
		log.Printf("ERROR: Failed to delete existing EAV rows: %v", err)
		return false
	}

	// Insert updated EAV rows
	for i, row := range eavRows {
		columns := make([]string, 0, len(row))
		placeholders := make([]string, 0, len(row))
		values := make([]interface{}, 0, len(row))

		j := 1
		for column, value := range row {
			columns = append(columns, column)
			placeholders = append(placeholders, fmt.Sprintf("$%d", j))
			values = append(values, value)
			j++
		}

		insertSQL := fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s)",
			p.tableName,
			strings.Join(columns, ", "),
			strings.Join(placeholders, ", "),
		)

		_, err = tx.ExecContext(ctx, insertSQL, values...)
		if err != nil {
			log.Printf("ERROR: Failed to insert updated EAV row %d: %v", i, err)
			return false
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		log.Printf("ERROR: Failed to commit update transaction: %v", err)
		return false
	}

	log.Printf("Updated ticket %s in EAV table %s with %d rows", ticketData.Id, p.tableName, len(eavRows))
	return true
}

// DeleteTicket removes a ticket from the PostgreSQL EAV table
func (p *PostgreSQLEAVStorage) DeleteTicket(tenant, id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First, get the ticket data before deletion
	ticketData, exists := p.GetTicket(tenant, id, nil)
	if !exists {
		return nil, false
	}

	// Delete all EAV rows for this ticket
	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE entity_id = $1", p.tableName)
	result, err := p.db.ExecContext(ctx, deleteSQL, id)
	if err != nil {
		log.Printf("ERROR: Failed to delete EAV rows for ticket %s: %v", id, err)
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

	log.Printf("Deleted ticket %s from EAV table %s (%d rows)", id, p.tableName, rowsAffected)
	return ticketData, true
}

// ListTickets retrieves all tickets from the PostgreSQL EAV table
func (p *PostgreSQLEAVStorage) ListTickets(tenant string, store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query all EAV rows, grouped by entity_id
	query := fmt.Sprintf(`
		SELECT entity_id, attribute_id, string_value, int_value, boolean_value, datatype
		FROM %s
		ORDER BY entity_id, attribute_id
	`, p.tableName)

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query EAV rows for tenant %s: %w", tenant, err)
	}
	defer rows.Close()

	// Group EAV rows by entity_id
	ticketRows := make(map[string][]map[string]interface{})

	for rows.Next() {
		var entityID string
		var attributeID int16
		var datatype int16
		var stringValue sql.NullString
		var intValue sql.NullInt64
		var booleanValue sql.NullBool

		err := rows.Scan(&entityID, &attributeID, &stringValue, &intValue, &booleanValue, &datatype)
		if err != nil {
			log.Printf("ERROR: Failed to scan EAV row: %v", err)
			continue
		}

		row := map[string]interface{}{
			"entity_id":    entityID,
			"attribute_id": int(attributeID),
			"datatype":     int(datatype),
		}

		if stringValue.Valid {
			row["string_value"] = stringValue.String
		}
		if intValue.Valid {
			row["int_value"] = intValue.Int64
		}
		if booleanValue.Valid {
			row["boolean_value"] = booleanValue.Bool
		}

		ticketRows[entityID] = append(ticketRows[entityID], row)
	}

	// Convert each group of EAV rows to tickets
	var tickets []*ticketpb.TicketData
	for entityID, eavRows := range ticketRows {
		ticketData, err := p.convertEAVRowsToTicket(eavRows)
		if err != nil {
			log.Printf("ERROR: Failed to convert EAV rows to ticket %s: %v", entityID, err)
			continue
		}
		tickets = append(tickets, ticketData)
	}

	log.Printf("Listed %d tickets from EAV table %s", len(tickets), p.tableName)
	return tickets, nil
}

// SearchTickets searches for tickets based on conditions in the PostgreSQL EAV table
func (p *PostgreSQLEAVStorage) SearchTickets(tenant string, request SearchRequest) ([]*ticketpb.TicketData, error) {
	return p.SearchTicketsWithProjection(tenant, request)
}

// SearchTicketsWithProjection searches for tickets with optional field projection using efficient CTE and LEFT JOINs
func (p *PostgreSQLEAVStorage) SearchTicketsWithProjection(tenant string, request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build the efficient projection query
	query, args, err := p.buildProjectionQuery(request)
	if err != nil {
		return nil, fmt.Errorf("failed to build projection query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute projection query: %w", err)
	}
	defer rows.Close()

	// Get column names from the result set
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get column names: %w", err)
	}

	var tickets []*ticketpb.TicketData

	for rows.Next() {
		// Create a slice to hold the column values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan the row
		if err := rows.Scan(valuePtrs...); err != nil {
			log.Printf("ERROR: Failed to scan projection row: %v", err)
			continue
		}

		// Convert to ticket
		ticket, err := p.convertProjectionRowToTicket(columns, values)
		if err != nil {
			log.Printf("ERROR: Failed to convert projection row to ticket: %v", err)
			continue
		}

		tickets = append(tickets, ticket)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over projection rows: %w", err)
	}

	log.Printf("Found %d tickets from projection search", len(tickets))
	return tickets, nil
}

// buildProjectionQuery builds an efficient CTE-based query with LEFT JOINs for field projection
func (p *PostgreSQLEAVStorage) buildProjectionQuery(request SearchRequest) (string, []interface{}, error) {
	var args []interface{}
	argIndex := 1

	// Step 1: Build the CTE to find matching entity_ids
	var cteQuery string
	if len(request.Conditions) == 0 {
		// No conditions - get all tickets (with limit for performance)
		cteQuery = fmt.Sprintf(`
			WITH matching_tickets AS (
				SELECT DISTINCT entity_id
				FROM %s
				ORDER BY entity_id
				LIMIT 1000
			)`, p.tableName)
	} else {
		// Build subqueries for each condition
		var subqueries []string
		for _, condition := range request.Conditions {
			metadata, exists := p.fieldMetadata[condition.Operand]
			if !exists {
				return "", nil, fmt.Errorf("unknown field: %s", condition.Operand)
			}

			var valueColumn string
			switch metadata.DataType {
			case DataTypeString:
				valueColumn = "string_value"
			case DataTypeInt:
				valueColumn = "int_value"
			case DataTypeBoolean:
				valueColumn = "boolean_value"
			default:
				return "", nil, fmt.Errorf("unsupported data type: %d", metadata.DataType)
			}

			var operator string
			switch condition.Operator {
			case "eq":
				operator = "="
			case "ne":
				operator = "!="
			case "gt":
				operator = ">"
			case "lt":
				operator = "<"
			case "gte":
				operator = ">="
			case "lte":
				operator = "<="
			case "contains":
				operator = "ILIKE"
				condition.Value = fmt.Sprintf("%%%s%%", condition.Value)
			case "begins_with":
				operator = "ILIKE"
				condition.Value = fmt.Sprintf("%s%%", condition.Value)
			default:
				return "", nil, fmt.Errorf("unsupported operator: %s", condition.Operator)
			}

			// Get attribute ID for the field name
			attributeID, exists := p.nameToAttribute[condition.Operand]
			if !exists {
				return "", nil, fmt.Errorf("unknown field: %s", condition.Operand)
			}

			args = append(args, attributeID, condition.Value)
			subquery := fmt.Sprintf(`
				SELECT entity_id FROM %s
				WHERE attribute_id = $%d AND %s %s $%d
			`, p.tableName, argIndex, valueColumn, operator, argIndex+1)

			subqueries = append(subqueries, subquery)
			argIndex += 2
		}

		// Find entity_ids that match ALL conditions (intersection)
		var entityQuery string
		if len(subqueries) == 1 {
			entityQuery = subqueries[0]
		} else {
			entityQuery = strings.Join(subqueries, " INTERSECT ")
		}

		cteQuery = fmt.Sprintf(`
			WITH matching_tickets AS (
				%s
			)`, entityQuery)
	}

	// Step 2: Determine which fields to project
	var fieldsToProject []string
	if len(request.ProjectedFields) > 0 {
		// Use specified projection fields
		fieldsToProject = request.ProjectedFields
	} else {
		// Project all fields
		for fieldName := range p.fieldMetadata {
			fieldsToProject = append(fieldsToProject, fieldName)
		}
	}

	// Step 3: Build the main SELECT with LEFT JOINs
	var selectColumns []string
	var leftJoins []string

	selectColumns = append(selectColumns, "mt.entity_id")

	for i, fieldName := range fieldsToProject {
		metadata, exists := p.fieldMetadata[fieldName]
		if !exists {
			continue
		}

		alias := fmt.Sprintf("f%d", i)

		var valueColumn string
		switch metadata.DataType {
		case DataTypeString:
			valueColumn = "string_value"
		case DataTypeInt:
			valueColumn = "int_value"
		case DataTypeBoolean:
			valueColumn = "boolean_value"
		}

		selectColumns = append(selectColumns, fmt.Sprintf("%s.%s AS %s", alias, valueColumn, fieldName))
		leftJoins = append(leftJoins, fmt.Sprintf(
			"LEFT JOIN %s %s ON %s.entity_id = mt.entity_id AND %s.attribute_id = %d",
			p.tableName, alias, alias, alias, metadata.AttributeID))
	}

	// Step 4: Combine everything
	finalQuery := fmt.Sprintf(`
		%s
		SELECT %s
		FROM matching_tickets mt
		%s
		ORDER BY mt.entity_id
	`, cteQuery, strings.Join(selectColumns, ",\n       "), strings.Join(leftJoins, "\n"))

	return finalQuery, args, nil
}

// convertProjectionRowToTicket converts a projection query result row to a TicketData
func (p *PostgreSQLEAVStorage) convertProjectionRowToTicket(columns []string, values []interface{}) (*ticketpb.TicketData, error) {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	for i, column := range columns {
		if column == "entity_id" {
			if entityID, ok := values[i].(string); ok {
				ticketData.Id = entityID
			}
			continue
		}

		// Skip null values
		if values[i] == nil {
			continue
		}

		// Get field metadata
		metadata, exists := p.fieldMetadata[column]
		if !exists {
			continue
		}

		fieldValue := &ticketpb.FieldValue{}

		// Convert based on data type
		switch metadata.DataType {
		case DataTypeString:
			if strVal, ok := values[i].(string); ok {
				fieldValue.Value = &ticketpb.FieldValue_StringValue{StringValue: strVal}
			}
		case DataTypeInt:
			if intVal, ok := values[i].(int64); ok {
				fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: intVal}
			}
		case DataTypeBoolean:
			if boolVal, ok := values[i].(bool); ok {
				fieldValue.Value = &ticketpb.FieldValue_BoolValue{BoolValue: boolVal}
			}
		}

		if fieldValue.Value != nil {
			ticketData.Fields[column] = fieldValue
		}
	}

	return ticketData, nil
}

// buildEAVSearchQuery builds a complex query for searching in EAV structure (legacy method)
func (p *PostgreSQLEAVStorage) buildEAVSearchQuery(request SearchRequest) (string, []interface{}, error) {
	if len(request.Conditions) == 0 {
		// No conditions - return all tickets
		query := fmt.Sprintf(`
			SELECT entity_id, attribute_id, string_value, int_value, boolean_value, datatype
			FROM %s
			ORDER BY entity_id, attribute_id
		`, p.tableName)
		return query, []interface{}{}, nil
	}

	// Build subqueries for each condition
	var subqueries []string
	var args []interface{}
	argIndex := 1

	for _, condition := range request.Conditions {
		metadata, exists := p.fieldMetadata[condition.Operand]
		if !exists {
			return "", nil, fmt.Errorf("unknown field: %s", condition.Operand)
		}

		var valueColumn string
		switch metadata.DataType {
		case DataTypeString:
			valueColumn = "string_value"
		case DataTypeInt:
			valueColumn = "int_value"
		case DataTypeBoolean:
			valueColumn = "boolean_value"
		default:
			return "", nil, fmt.Errorf("unsupported data type: %d", metadata.DataType)
		}

		var operator string
		switch condition.Operator {
		case "eq":
			operator = "="
		case "ne":
			operator = "!="
		case "gt":
			operator = ">"
		case "lt":
			operator = "<"
		case "gte":
			operator = ">="
		case "lte":
			operator = "<="
		case "contains":
			if metadata.DataType != DataTypeString {
				return "", nil, fmt.Errorf("contains operator only supported for string fields")
			}
			operator = "ILIKE"
			condition.Value = fmt.Sprintf("%%%s%%", condition.Value)
		case "begins_with":
			if metadata.DataType != DataTypeString {
				return "", nil, fmt.Errorf("begins_with operator only supported for string fields")
			}
			operator = "ILIKE"
			condition.Value = fmt.Sprintf("%s%%", condition.Value)
		default:
			return "", nil, fmt.Errorf("unsupported operator: %s", condition.Operator)
		}

		// Get attribute ID for the field name
		attributeID, exists := p.nameToAttribute[condition.Operand]
		if !exists {
			return "", nil, fmt.Errorf("unknown field: %s", condition.Operand)
		}

		args = append(args, attributeID, condition.Value)
		subquery := fmt.Sprintf(`
			SELECT entity_id FROM %s
			WHERE attribute_id = $%d AND %s %s $%d
		`, p.tableName, argIndex, valueColumn, operator, argIndex+1)

		subqueries = append(subqueries, subquery)
		argIndex += 2
	}

	// Find entity_ids that match ALL conditions (intersection)
	var entityQuery string
	if len(subqueries) == 1 {
		entityQuery = subqueries[0]
	} else {
		entityQuery = strings.Join(subqueries, " INTERSECT ")
	}

	// Final query to get all EAV rows for matching entities
	finalQuery := fmt.Sprintf(`
		SELECT entity_id, attribute_id, string_value, int_value, boolean_value, datatype
		FROM %s
		WHERE entity_id IN (%s)
		ORDER BY entity_id, attribute_id
	`, p.tableName, entityQuery)

	return finalQuery, args, nil
}

// applyFieldProjection filters ticket fields based on projected fields
func (p *PostgreSQLEAVStorage) applyFieldProjection(ticketData *ticketpb.TicketData, projectedFields []string) *ticketpb.TicketData {
	if len(projectedFields) == 0 {
		return ticketData
	}

	projectedTicket := &ticketpb.TicketData{
		Id:     ticketData.Id,
		Tenant: ticketData.Tenant,
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Always include ticket_id in projection
	projectedFieldsMap := make(map[string]bool)
	projectedFieldsMap["ticket_id"] = true
	for _, field := range projectedFields {
		projectedFieldsMap[field] = true
	}

	// Copy only projected fields
	for fieldName, fieldValue := range ticketData.Fields {
		if projectedFieldsMap[fieldName] {
			projectedTicket.Fields[fieldName] = fieldValue
		}
	}

	return projectedTicket
}

// Close closes the database connection
func (p *PostgreSQLEAVStorage) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}
