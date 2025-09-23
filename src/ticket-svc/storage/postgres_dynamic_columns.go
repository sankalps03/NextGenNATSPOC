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
	"github.com/platform/ticket-svc/logger"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// FieldMapping represents a mapping between a field name and a database column
type FieldMapping struct {
	CategoryID int64  `json:"category_id"`
	FieldName  string `json:"field_name"`
	ColumnName string `json:"column_name"`
	DataType   string `json:"data_type"` // "string" or "numeric"
}

// PostgreSQLDynamicStorage implements ticket storage using PostgreSQL with dynamic column mapping
// Uses static base columns + 50 string columns + 50 numeric columns
// Custom fields are mapped to available columns per category
type PostgreSQLDynamicStorage struct {
	db                *sql.DB
	tableName         string
	mappingTableName  string
	fieldMappings     map[string]map[string]FieldMapping // categoryID -> fieldName -> mapping
	mappingMutex      sync.RWMutex                       // protects fieldMappings
	nextStringColumn  map[int64]int                      // categoryID -> next available string column number
	nextNumericColumn map[int64]int                      // categoryID -> next available numeric column number
	logger            logger.Logger                      // logger for performance metrics
}

// NewPostgreSQLDynamicStorage creates a new PostgreSQL storage instance with dynamic column mapping
func NewPostgreSQLDynamicStorage(ctx context.Context, tableName, connectionString string) (*PostgreSQLDynamicStorage, error) {
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

	log.Printf("PostgreSQL Dynamic Columns connection established successfully")

	storage := &PostgreSQLDynamicStorage{
		db:                db,
		tableName:         tableName,
		mappingTableName:  "field_mappings",
		fieldMappings:     make(map[string]map[string]FieldMapping),
		nextStringColumn:  make(map[int64]int),
		nextNumericColumn: make(map[int64]int),
		logger:            logger.NewLogger("postgresql-storage", "ticket-svc"),
	}

	// Ensure the tables exist
	if err := storage.ensureTablesExist(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure tables exist: %w", err)
	}

	// Load existing field mappings into memory
	if err := storage.loadFieldMappings(ctx); err != nil {
		return nil, fmt.Errorf("failed to load field mappings: %w", err)
	}

	return storage, nil
}

// generateTicketID generates a unique ticket ID if not provided
func (p *PostgreSQLDynamicStorage) generateTicketID() string {
	return fmt.Sprintf("TKT-%d", time.Now().UnixNano()/1000000)
}

// ensureTablesExist ensures that both the tickets and field_mappings tables exist
func (p *PostgreSQLDynamicStorage) ensureTablesExist(ctx context.Context) error {
	// Check if main table exists
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
		// Create the tables using schema file
		if err := p.createTablesIfNotExist(ctx); err != nil {
			return fmt.Errorf("failed to create tables: %w", err)
		}
		log.Printf("Created new PostgreSQL dynamic tables: %s and %s", p.tableName, p.mappingTableName)
	} else {
		log.Printf("Found existing PostgreSQL dynamic table: %s", p.tableName)
	}

	return nil
}

// loadSchemaFromFile loads SQL schema from the database/postgresql directory
func (p *PostgreSQLDynamicStorage) loadSchemaFromFile() (string, error) {
	// Try to find the schema file in common locations
	possiblePaths := []string{
		"database/postgresql/schema_dynamic_columns.sql",
		"../database/postgresql/schema_dynamic_columns.sql",
		"../../database/postgresql/schema_dynamic_columns.sql",
		"./database/postgresql/schema_dynamic_columns.sql",
	}

	var schemaContent string

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			content, readErr := ioutil.ReadFile(path)
			if readErr == nil {
				schemaContent = string(content)
				log.Printf("Loaded PostgreSQL dynamic schema from: %s", path)
				break
			}
		}
	}

	if schemaContent == "" {
		return "", fmt.Errorf("could not find schema_dynamic_columns.sql file in any of the expected locations: %v", possiblePaths)
	}

	return schemaContent, nil
}

// createTablesIfNotExist creates the PostgreSQL tables using the schema from file
func (p *PostgreSQLDynamicStorage) createTablesIfNotExist(ctx context.Context) error {
	// Load schema from file
	schemaContent, err := p.loadSchemaFromFile()
	if err != nil {
		return fmt.Errorf("failed to load schema file: %w", err)
	}

	// Replace table name placeholder if needed
	schemaContent = strings.ReplaceAll(schemaContent, "tickets_dynamic", p.tableName)

	// Execute the schema
	_, err = p.db.ExecContext(ctx, schemaContent)
	if err != nil {
		return fmt.Errorf("failed to create tables %s: %w", p.tableName, err)
	}

	log.Printf("Created PostgreSQL dynamic tables %s and %s", p.tableName, p.mappingTableName)
	return nil
}

// loadFieldMappings loads existing field mappings from database into memory
func (p *PostgreSQLDynamicStorage) loadFieldMappings(ctx context.Context) error {
	query := `
		SELECT category_id, field_name, column_name, data_type 
		FROM field_mappings 
		ORDER BY category_id, field_name`

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to load field mappings: %w", err)
	}
	defer rows.Close()

	p.mappingMutex.Lock()
	defer p.mappingMutex.Unlock()

	// Reset mappings
	p.fieldMappings = make(map[string]map[string]FieldMapping)
	p.nextStringColumn = make(map[int64]int)
	p.nextNumericColumn = make(map[int64]int)

	for rows.Next() {
		var mapping FieldMapping
		if err := rows.Scan(&mapping.CategoryID, &mapping.FieldName, &mapping.ColumnName, &mapping.DataType); err != nil {
			return fmt.Errorf("failed to scan field mapping: %w", err)
		}

		// Initialize category map if needed
		categoryKey := fmt.Sprintf("%d", mapping.CategoryID)
		if p.fieldMappings[categoryKey] == nil {
			p.fieldMappings[categoryKey] = make(map[string]FieldMapping)
		}

		// Store mapping
		p.fieldMappings[categoryKey][mapping.FieldName] = mapping

		// Update next available column counters
		if mapping.DataType == "string" {
			columnNum := extractColumnNumber(mapping.ColumnName, "string")
			if columnNum >= p.nextStringColumn[mapping.CategoryID] {
				p.nextStringColumn[mapping.CategoryID] = columnNum + 1
			}
		} else if mapping.DataType == "numeric" {
			columnNum := extractColumnNumber(mapping.ColumnName, "numeric")
			if columnNum >= p.nextNumericColumn[mapping.CategoryID] {
				p.nextNumericColumn[mapping.CategoryID] = columnNum + 1
			}
		}
	}

	log.Printf("Loaded %d field mappings from database", len(p.fieldMappings))
	return nil
}

// extractColumnNumber extracts the column number from column name (e.g., "c5_string" -> 5)
func extractColumnNumber(columnName, dataType string) int {
	prefix := "c"
	suffix := "_" + dataType

	if !strings.HasPrefix(columnName, prefix) || !strings.HasSuffix(columnName, suffix) {
		return 0
	}

	numStr := strings.TrimPrefix(strings.TrimSuffix(columnName, suffix), prefix)
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0
	}

	return num
}

// ensureFieldMapping ensures a field mapping exists for the given category and field
// All operations are done in memory - database is only updated for persistence
func (p *PostgreSQLDynamicStorage) ensureFieldMapping(categoryID int64, fieldName string, dataType string) (FieldMapping, error) {
	p.mappingMutex.Lock()
	defer p.mappingMutex.Unlock()

	categoryKey := fmt.Sprintf("%d", categoryID)

	// Check if mapping already exists in memory
	if categoryMappings, exists := p.fieldMappings[categoryKey]; exists {
		if mapping, exists := categoryMappings[fieldName]; exists {
			return mapping, nil
		}
	}

	// Create new mapping in memory
	var columnName string
	var nextColumn int

	if dataType == "string" {
		nextColumn = p.nextStringColumn[categoryID]
		if nextColumn == 0 {
			nextColumn = 1 // Start from c1_string
		}
		if nextColumn > 50 {
			return FieldMapping{}, fmt.Errorf("no more string columns available for category %d (max 50)", categoryID)
		}
		columnName = fmt.Sprintf("c%d_string", nextColumn)
		p.nextStringColumn[categoryID] = nextColumn + 1
	} else if dataType == "numeric" {
		nextColumn = p.nextNumericColumn[categoryID]
		if nextColumn == 0 {
			nextColumn = 1 // Start from c1_numeric
		}
		if nextColumn > 50 {
			return FieldMapping{}, fmt.Errorf("no more numeric columns available for category %d (max 50)", categoryID)
		}
		columnName = fmt.Sprintf("c%d_numeric", nextColumn)
		p.nextNumericColumn[categoryID] = nextColumn + 1
	} else {
		return FieldMapping{}, fmt.Errorf("unsupported data type: %s", dataType)
	}

	// Create the mapping
	mapping := FieldMapping{
		CategoryID: categoryID,
		FieldName:  fieldName,
		ColumnName: columnName,
		DataType:   dataType,
	}

	// Save to memory first
	if p.fieldMappings[categoryKey] == nil {
		p.fieldMappings[categoryKey] = make(map[string]FieldMapping)
	}
	p.fieldMappings[categoryKey][fieldName] = mapping

	// Persist to database asynchronously (non-blocking)
	go p.persistFieldMapping(mapping)

	log.Printf("Created new field mapping in memory: category=%d, field=%s, column=%s, type=%s",
		categoryID, fieldName, columnName, dataType)

	return mapping, nil
}

// persistFieldMapping saves a field mapping to the database for persistence
// This runs asynchronously to avoid blocking the main operations
func (p *PostgreSQLDynamicStorage) persistFieldMapping(mapping FieldMapping) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	insertQuery := `
		INSERT INTO field_mappings (category_id, field_name, column_name, data_type)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (category_id, field_name) DO NOTHING`

	_, err := p.db.ExecContext(ctx, insertQuery, mapping.CategoryID, mapping.FieldName, mapping.ColumnName, mapping.DataType)
	if err != nil {
		log.Printf("Warning: Failed to persist field mapping to database: %v", err)
		// Note: We don't fail the operation since the mapping is already in memory
		// The application can continue working, and the mapping will be lost only on restart
		// Consider implementing a retry mechanism or batch persistence for production
	} else {
		log.Printf("Persisted field mapping to database: category=%d, field=%s, column=%s",
			mapping.CategoryID, mapping.FieldName, mapping.ColumnName)
	}
}

// determineDataType determines the data type from protobuf FieldValue
func determineDataType(fieldValue *ticketpb.FieldValue) string {
	switch fieldValue.Value.(type) {
	case *ticketpb.FieldValue_StringValue, *ticketpb.FieldValue_BytesValue, *ticketpb.FieldValue_StringArray:
		return "string"
	case *ticketpb.FieldValue_IntValue, *ticketpb.FieldValue_DoubleValue, *ticketpb.FieldValue_BoolValue:
		return "numeric"
	default:
		return "string" // default to string for unknown types
	}
}

// isTimestampField checks if a field name represents a timestamp field
func isTimestampField(fieldName string) bool {
	timestampFields := map[string]bool{
		"createdtime":                true,
		"updatedtime":                true,
		"dueby":                      true,
		"firstresponsetime":          true,
		"responsedue":                true,
		"resolutionescalationtime":   true,
		"responsetimeescalationtime": true,
		"removedtime":                true,
		"lastopenedtime":             true,
		"lastresolvedtime":           true,
		"lastclosedtime":             true,
		"statuschangedtime":          true,
		"groupchangedtime":           true,
		"lastviolationtime":          true,
		"lastolaviolationtime":       true,
		"lastucviolationtime":        true,
		"oladueby":                   true,
		"olaescalationtime":          true,
		"ucdueby":                    true,
		"ucescalationtime":           true,
	}
	return timestampFields[fieldName]
}

// protobufToDynamicRow converts a TicketData protobuf to PostgreSQL row data using in-memory dynamic column mapping
func (p *PostgreSQLDynamicStorage) protobufToDynamicRow(ticketData *ticketpb.TicketData, categoryID int64, isUpdate bool) (map[string]interface{}, error) {
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

	// Category ID is now passed as a parameter - no need to extract from ticket data
	// This eliminates the collision between categoryid as data field and category for field mapping

	// Process static base columns first - all fields from hybrid schema (excluding document_core)
	staticFields := map[string]bool{
		// User and assignment fields
		"updatedbyid": true, "createdbyid": true, "removedbyid": true, "requesterid": true,
		"technicianid": true, "closedby": true, "resolvedby": true,

		// Timestamp fields (stored as BIGINT Unix timestamps in milliseconds)
		"updatedtime": true, "createdtime": true, "removedtime": true, "dueby": true,
		"firstresponsetime": true, "lastclosedtime": true, "lastopenedtime": true, "lastresolvedtime": true,
		"lastviolationtime": true, "olddueby": true, "oldresponsedue": true, "resolutionescalationtime": true,
		"responsedue": true, "responseescalationtime": true, "statuschangedtime": true, "groupchangedtime": true,
		"lastolaviolationtime": true, "oladueby": true, "oldoladueby": true, "askfeedbackdate": true,
		"firstfeedbackdate": true, "olaescalationtime": true, "lastucviolationtime": true, "olducdueby": true,
		"ucdueby": true, "ucescalationtime": true, "lastapproveddate": true,

		// Text fields
		"name": true, "oobtype": true, "description": true, "originaldescription": true,
		"subject": true, "callfrom": true, "emailreadconfigemail": true,

		// Boolean fields
		"removed": true, "duetimemanuallyupdated": true, "reopened": true, "responsedueviolated": true,
		"slaviolated": true, "purchaserequest": true, "spam": true, "viprequest": true,
		"olaviolated": true, "ucviolated": true, "migrated": true,

		// Category and classification fields
		"categoryid": true, "departmentid": true, "groupid": true, "impactid": true, "locationid": true,
		"priorityid": true, "statusid": true, "urgencyid": true, "violatedslaid": true, "servicecatalogid": true,
		"sourceid": true, "requesttype": true, "suggestedcategoryid": true, "suggestedgroupid": true,
		"companyid": true, "vendorid": true, "violateducid": true, "transitionmodelid": true, "messengerconfigid": true,

		// Approval and workflow fields
		"approvalstatus": true, "approvaltype": true, "resolutionduelevel": true, "responseduelevel": true,
		"supportlevel": true, "oladuelevel": true, "ucduelevel": true,

		// Duration and time tracking fields (in milliseconds)
		"totalonholdduration": true, "totalresolutiontime": true, "totalslapausetime": true, "totalworkingtime": true,
		"totaluconholdduration": true, "totalucpausetime": true, "totalucworkingtime": true, "totalucresolutiontime": true,

		// Configuration and template fields
		"templateid": true, "emailreadconfigid": true,
	}

	// Handle static fields
	for fieldName, fieldValue := range ticketData.Fields {
		if staticFields[fieldName] {
			var value interface{}
			switch v := fieldValue.Value.(type) {
			case *ticketpb.FieldValue_StringValue:
				value = v.StringValue
			case *ticketpb.FieldValue_IntValue:
				// For timestamp fields, we need to be more careful about the target column type
				// The safest approach is to store Unix timestamps as BIGINT rather than converting to TIMESTAMP
				if isTimestampField(fieldName) {
					// Always store timestamp fields as Unix timestamps (BIGINT)
					// This avoids PostgreSQL TIMESTAMP range issues and maintains consistency
					value = v.IntValue
				} else {
					value = v.IntValue
				}
			case *ticketpb.FieldValue_DoubleValue:
				value = int64(v.DoubleValue)
			case *ticketpb.FieldValue_BoolValue:
				if v.BoolValue {
					value = int64(1)
				} else {
					value = int64(0)
				}
			default:
				value = nil
			}
			row[fieldName] = value
		}
	}

	// Handle dynamic fields using in-memory field mappings
	// This now includes timestamp fields like createdtime, updatedtime since we excluded them from static fields
	for fieldName, fieldValue := range ticketData.Fields {
		if !staticFields[fieldName] && fieldName != "categoryid" { // skip static fields and categoryid (already processed)
			dataType := determineDataType(fieldValue)

			// Ensure field mapping exists (in-memory operation)
			mapping, err := p.ensureFieldMapping(categoryID, fieldName, dataType)
			if err != nil {
				return nil, fmt.Errorf("failed to ensure field mapping for %s: %w", fieldName, err)
			}

			// Convert value based on data type
			var value interface{}
			switch v := fieldValue.Value.(type) {
			case *ticketpb.FieldValue_StringValue:
				value = v.StringValue
			case *ticketpb.FieldValue_IntValue:
				// For dynamic fields, store timestamp fields as Unix timestamps
				// This maintains consistency with other storage backends and avoids conversion issues
				if isTimestampField(fieldName) {
					if mapping.DataType == "string" {
						// Store as string column - convert to ISO format for readability
						value = time.Unix(0, v.IntValue*int64(time.Millisecond)).Format(time.RFC3339)
					} else {
						// Store as numeric column - keep as Unix timestamp in milliseconds
						value = v.IntValue
					}
				} else {
					value = v.IntValue
				}
			case *ticketpb.FieldValue_DoubleValue:
				if mapping.DataType == "numeric" {
					value = int64(v.DoubleValue) // convert to int64 for storage
				} else {
					value = fmt.Sprintf("%.6f", v.DoubleValue) // convert to string
				}
			case *ticketpb.FieldValue_BoolValue:
				if mapping.DataType == "numeric" {
					if v.BoolValue {
						value = int64(1)
					} else {
						value = int64(0)
					}
				} else {
					value = fmt.Sprintf("%t", v.BoolValue)
				}
			case *ticketpb.FieldValue_BytesValue:
				value = string(v.BytesValue)
			case *ticketpb.FieldValue_StringArray:
				value = strings.Join(v.StringArray.Values, ",") // join array as comma-separated string
			default:
				value = nil
			}

			// Store in the mapped column
			row[mapping.ColumnName] = value
		}
	}

	return row, nil
}

// dynamicRowToProtobuf converts a PostgreSQL row back to protobuf using field mappings
func (p *PostgreSQLDynamicStorage) dynamicRowToProtobuf(row map[string]interface{}, categoryID int64) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Set core fields
	if id, ok := row["ticket_id"].(string); ok {
		ticketData.Id = id
	}
	if createdAt, ok := row["created_at"].(time.Time); ok {
		ticketData.CreatedAt = createdAt.Format(time.RFC3339)
	}
	if updatedAt, ok := row["updated_at"].(time.Time); ok {
		ticketData.UpdatedAt = updatedAt.Format(time.RFC3339)
	}

	// Handle static base columns - all fields from hybrid schema (excluding document_core)
	staticFields := map[string]bool{
		// User and assignment fields
		"updatedbyid": true, "createdbyid": true, "removedbyid": true, "requesterid": true,
		"technicianid": true, "closedby": true, "resolvedby": true,

		// Timestamp fields (stored as BIGINT Unix timestamps in milliseconds)
		"updatedtime": true, "createdtime": true, "removedtime": true, "dueby": true,
		"firstresponsetime": true, "lastclosedtime": true, "lastopenedtime": true, "lastresolvedtime": true,
		"lastviolationtime": true, "olddueby": true, "oldresponsedue": true, "resolutionescalationtime": true,
		"responsedue": true, "responseescalationtime": true, "statuschangedtime": true, "groupchangedtime": true,
		"lastolaviolationtime": true, "oladueby": true, "oldoladueby": true, "askfeedbackdate": true,
		"firstfeedbackdate": true, "olaescalationtime": true, "lastucviolationtime": true, "olducdueby": true,
		"ucdueby": true, "ucescalationtime": true, "lastapproveddate": true,

		// Text fields
		"name": true, "oobtype": true, "description": true, "originaldescription": true,
		"subject": true, "callfrom": true, "emailreadconfigemail": true,

		// Boolean fields
		"removed": true, "duetimemanuallyupdated": true, "reopened": true, "responsedueviolated": true,
		"slaviolated": true, "purchaserequest": true, "spam": true, "viprequest": true,
		"olaviolated": true, "ucviolated": true, "migrated": true,

		// Category and classification fields
		"categoryid": true, "departmentid": true, "groupid": true, "impactid": true, "locationid": true,
		"priorityid": true, "statusid": true, "urgencyid": true, "violatedslaid": true, "servicecatalogid": true,
		"sourceid": true, "requesttype": true, "suggestedcategoryid": true, "suggestedgroupid": true,
		"companyid": true, "vendorid": true, "violateducid": true, "transitionmodelid": true, "messengerconfigid": true,

		// Approval and workflow fields
		"approvalstatus": true, "approvaltype": true, "resolutionduelevel": true, "responseduelevel": true,
		"supportlevel": true, "oladuelevel": true, "ucduelevel": true,

		// Duration and time tracking fields (in milliseconds)
		"totalonholdduration": true, "totalresolutiontime": true, "totalslapausetime": true, "totalworkingtime": true,
		"totaluconholdduration": true, "totalucpausetime": true, "totalucworkingtime": true, "totalucresolutiontime": true,

		// Configuration and template fields
		"templateid": true, "emailreadconfigid": true,
	}

	for fieldName := range staticFields {
		if value, exists := row[fieldName]; exists && value != nil {
			switch v := value.(type) {
			case string:
				ticketData.Fields[fieldName] = &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_StringValue{StringValue: v},
				}
			case int64:
				ticketData.Fields[fieldName] = &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_IntValue{IntValue: v},
				}
			case int32:
				ticketData.Fields[fieldName] = &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
				}
			case int:
				ticketData.Fields[fieldName] = &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
				}
			case float64:
				ticketData.Fields[fieldName] = &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: v},
				}
			case float32:
				ticketData.Fields[fieldName] = &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: float64(v)},
				}
			case bool:
				ticketData.Fields[fieldName] = &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_BoolValue{BoolValue: v},
				}
			}
		}
	}

	// Handle dynamic fields using reverse mapping
	p.mappingMutex.RLock()
	categoryKey := fmt.Sprintf("%d", categoryID)
	if categoryMappings, exists := p.fieldMappings[categoryKey]; exists {
		for fieldName, mapping := range categoryMappings {
			if value, exists := row[mapping.ColumnName]; exists && value != nil {
				switch mapping.DataType {
				case "string":
					if strVal, ok := value.(string); ok {
						// Check if it's a comma-separated array
						if strings.Contains(strVal, ",") {
							values := strings.Split(strVal, ",")
							ticketData.Fields[fieldName] = &ticketpb.FieldValue{
								Value: &ticketpb.FieldValue_StringArray{
									StringArray: &ticketpb.StringArray{Values: values},
								},
							}
						} else {
							ticketData.Fields[fieldName] = &ticketpb.FieldValue{
								Value: &ticketpb.FieldValue_StringValue{StringValue: strVal},
							}
						}
					}
				case "numeric":
					if intVal, ok := value.(int64); ok {
						ticketData.Fields[fieldName] = &ticketpb.FieldValue{
							Value: &ticketpb.FieldValue_IntValue{IntValue: intVal},
						}
					}
				}
			}
		}
	}
	p.mappingMutex.RUnlock()

	// Add categoryid back to fields
	ticketData.Fields["categoryid"] = &ticketpb.FieldValue{
		Value: &ticketpb.FieldValue_IntValue{IntValue: categoryID},
	}

	return ticketData
}

// CreateTicket stores a new ticket in the PostgreSQL dynamic table
func (p *PostgreSQLDynamicStorage) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Generate ticket ID if not provided
	if ticketData.Id == "" {
		ticketData.Id = p.generateTicketID()
	}

	// Extract category ID from ticketCatelogid field for field mapping (NOT categoryid which is data)
	categoryID := int64(1) // Default category
	if categoryField, exists := ticketData.Fields["ticketCatelogid"]; exists {
		if intVal, ok := categoryField.Value.(*ticketpb.FieldValue_IntValue); ok {
			categoryID = intVal.IntValue
		} else if intVal, ok := categoryField.Value.(*ticketpb.FieldValue_DoubleValue); ok {
			categoryID = int64(intVal.DoubleValue)
		}
	}

	// Convert protobuf to PostgreSQL row (isUpdate = false for create)
	row, err := p.protobufToDynamicRow(ticketData, categoryID, false)
	if err != nil {
		return fmt.Errorf("failed to convert protobuf to dynamic row: %w", err), nil
	}

	// Build dynamic INSERT query based on available fields
	var columns []string
	var placeholders []string
	var values []interface{}
	paramIndex := 1

	for column, value := range row {
		columns = append(columns, column)
		placeholders = append(placeholders, fmt.Sprintf("$%d", paramIndex))
		values = append(values, value)
		paramIndex++
	}

	insertSQL := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING id",
		p.tableName,
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	)

	var insertedID int64

	// Log query execution time
	queryStart := time.Now()
	err = p.db.QueryRowContext(ctx, insertSQL, values...).Scan(&insertedID)
	queryDuration := time.Since(queryStart)

	if err != nil {
		log.Printf("ERROR: SQL execution failed: %v", err)
		log.Printf("ERROR: SQL was: %s", insertSQL)
		log.Printf("ERROR: Values were: %v", values)
		p.logger.LogQueryExecution("CREATE_TICKET_FAILED", queryDuration, 0)
		return fmt.Errorf("failed to create ticket in table %s: %w", p.tableName, err), nil
	}

	// Log successful query execution
	p.logger.LogQueryExecution("CREATE_TICKET", queryDuration, 1)

	//log.Printf("Created ticket %s in dynamic table %s with ID %d", ticketData.Id, p.tableName, insertedID)

	resultMap := map[string]interface{}{
		"id":        insertedID,
		"ticket_id": ticketData.Id,
	}

	return nil, resultMap
}

// GetTicket retrieves a ticket by ID from the PostgreSQL dynamic table
func (p *PostgreSQLDynamicStorage) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build dynamic SELECT query to get all columns
	selectSQL := fmt.Sprintf("SELECT * FROM %s WHERE ticket_id = $1", p.tableName)

	// Log query execution time
	queryStart := time.Now()
	rows, err := p.db.QueryContext(ctx, selectSQL, id)
	queryDuration := time.Since(queryStart)

	if err != nil {
		log.Printf("Failed to query ticket %s: %v", id, err)
		p.logger.LogQueryExecution("GET_TICKET_FAILED", queryDuration, 0)
		return nil, false
	}
	defer rows.Close()

	p.logger.LogQueryExecution("GET_TICKET", queryDuration, 1)

	if !rows.Next() {
		return nil, false
	}

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		log.Printf("Failed to get columns: %v", err)
		return nil, false
	}

	// Create slice to hold values
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	// Scan the row - log scan time
	scanStart := time.Now()
	if err := rows.Scan(valuePtrs...); err != nil {
		log.Printf("Failed to scan ticket row: %v", err)
		return nil, false
	}
	scanDuration := time.Since(scanStart)
	p.logger.LogRowScan("GET_TICKET", scanDuration, 1)

	// Convert to map
	row := make(map[string]interface{})
	for i, column := range columns {
		row[column] = values[i]
	}

	// Extract category ID for mapping
	var categoryID int64 = 1 // default
	if catVal, exists := row["categoryid"]; exists && catVal != nil {
		if intVal, ok := catVal.(int64); ok {
			categoryID = intVal
		}
	}

	// Convert back to protobuf
	ticketData := p.dynamicRowToProtobuf(row, categoryID)

	log.Printf("Retrieved ticket %s from dynamic table %s", id, p.tableName)
	return ticketData, true
}

// UpdateTicket updates an existing ticket in the PostgreSQL dynamic table
func (p *PostgreSQLDynamicStorage) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Extract category ID from ticketCatelogid field for field mapping (NOT categoryid which is data)
	categoryID := int64(1) // Default category
	if categoryField, exists := ticketData.Fields["ticketCatelogid"]; exists {
		if intVal, ok := categoryField.Value.(*ticketpb.FieldValue_IntValue); ok {
			categoryID = intVal.IntValue
		} else if intVal, ok := categoryField.Value.(*ticketpb.FieldValue_DoubleValue); ok {
			categoryID = int64(intVal.DoubleValue)
		}
	}

	// Convert protobuf to PostgreSQL row (isUpdate = true for update)
	row, err := p.protobufToDynamicRow(ticketData, categoryID, true)
	if err != nil {
		log.Printf("Failed to convert protobuf to dynamic row: %v", err)
		return false
	}

	// Build dynamic UPDATE query
	var setParts []string
	var values []interface{}
	paramIndex := 1

	for column, value := range row {
		if column != "ticket_id" && column != "created_at" { // don't update these fields
			setParts = append(setParts, fmt.Sprintf("%s = $%d", column, paramIndex))
			values = append(values, value)
			paramIndex++
		}
	}

	if len(setParts) == 0 {
		log.Printf("No fields to update for ticket %s", ticketData.Id)
		return false
	}

	// Add ticket_id as the WHERE condition
	values = append(values, ticketData.Id)
	updateSQL := fmt.Sprintf(
		"UPDATE %s SET %s WHERE ticket_id = $%d",
		p.tableName,
		strings.Join(setParts, ", "),
		paramIndex,
	)

	result, err := p.db.ExecContext(ctx, updateSQL, values...)
	if err != nil {
		log.Printf("Failed to update ticket %s: %v", ticketData.Id, err)
		return false
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("Failed to get rows affected for ticket %s: %v", ticketData.Id, err)
		return false
	}

	if rowsAffected == 0 {
		log.Printf("No rows updated for ticket %s", ticketData.Id)
		return false
	}

	log.Printf("Updated ticket %s in dynamic table %s", ticketData.Id, p.tableName)
	return true
}

// DeleteTicket removes a ticket from the PostgreSQL dynamic table
func (p *PostgreSQLDynamicStorage) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First, get the ticket to return it
	ticketData, exists := p.GetTicket(id, nil)
	if !exists {
		return nil, false
	}

	// Delete the ticket
	deleteSQL := fmt.Sprintf("DELETE FROM %s WHERE ticket_id = $1", p.tableName)
	result, err := p.db.ExecContext(ctx, deleteSQL, id)
	if err != nil {
		log.Printf("Failed to delete ticket %s: %v", id, err)
		return nil, false
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("Failed to get rows affected for delete ticket %s: %v", id, err)
		return nil, false
	}

	if rowsAffected == 0 {
		log.Printf("No rows deleted for ticket %s", id)
		return nil, false
	}

	log.Printf("Deleted ticket %s from dynamic table %s", id, p.tableName)
	return ticketData, true
}

// ListTickets retrieves all tickets from the PostgreSQL dynamic table
func (p *PostgreSQLDynamicStorage) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build dynamic SELECT query to get all tickets
	selectSQL := fmt.Sprintf("SELECT * FROM %s ORDER BY created_at DESC", p.tableName)

	rows, err := p.db.QueryContext(ctx, selectSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to query tickets: %w", err)
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
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
			log.Printf("Failed to scan ticket row: %v", err)
			continue
		}

		// Convert to map
		row := make(map[string]interface{})
		for i, column := range columns {
			row[column] = values[i]
		}

		// Extract category ID for mapping
		var categoryID int64 = 1 // default
		if catVal, exists := row["categoryid"]; exists && catVal != nil {
			if intVal, ok := catVal.(int64); ok {
				categoryID = intVal
			}
		}

		// Convert back to protobuf
		ticketData := p.dynamicRowToProtobuf(row, categoryID)
		tickets = append(tickets, ticketData)
	}

	log.Printf("Listed %d tickets from dynamic table %s", len(tickets), p.tableName)
	return tickets, nil
}

// SearchTickets searches for tickets based on conditions
func (p *PostgreSQLDynamicStorage) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start timing the search operation
	searchStart := time.Now()

	// Build WHERE clause from conditions
	var whereParts []string
	var values []interface{}
	paramIndex := 1
	var mappedColumns []string // Track mapped columns for logging

	// Get category ID from the dedicated CategoryFilter field
	categoryID := int64(-1) // Default to cross-category search
	if request.CategoryFilter != nil {
		categoryID = *request.CategoryFilter
	}

	for _, condition := range request.Conditions {
		// Map field names to actual column names using category-aware mapping
		columnName := p.mapFieldToColumnWithCategory(condition.Operand, categoryID)

		// Check if the field mapping exists - handle both category-specific and cross-category searches
		if !p.isStaticField(condition.Operand) {
			if categoryID != -1 {
				// Category-specific search
				if !p.fieldMappingExists(condition.Operand, categoryID) {
					continue
				}
			} else {
				// Cross-category search - check if field exists in any category
				if !p.fieldMappingExistsInAnyCategory(condition.Operand) {
					continue
				}
			}
		}

		// Track mapped column for logging
		mappedColumns = append(mappedColumns, fmt.Sprintf("%s->%s", condition.Operand, columnName))

		switch condition.Operator {
		case "eq":
			whereParts = append(whereParts, fmt.Sprintf("%s = $%d", columnName, paramIndex))
			values = append(values, condition.Value)
			paramIndex++
		case "ne":
			whereParts = append(whereParts, fmt.Sprintf("%s != $%d", columnName, paramIndex))
			values = append(values, condition.Value)
			paramIndex++
		case "gt":
			whereParts = append(whereParts, fmt.Sprintf("%s > $%d", columnName, paramIndex))
			values = append(values, condition.Value)
			paramIndex++
		case "lt":
			whereParts = append(whereParts, fmt.Sprintf("%s < $%d", columnName, paramIndex))
			values = append(values, condition.Value)
			paramIndex++
		case "gte":
			whereParts = append(whereParts, fmt.Sprintf("%s >= $%d", columnName, paramIndex))
			values = append(values, condition.Value)
			paramIndex++
		case "lte":
			whereParts = append(whereParts, fmt.Sprintf("%s <= $%d", columnName, paramIndex))
			values = append(values, condition.Value)
			paramIndex++
		case "contains":
			whereParts = append(whereParts, fmt.Sprintf("%s ILIKE $%d", columnName, paramIndex))
			values = append(values, fmt.Sprintf("%%%s%%", condition.Value))
			paramIndex++
		case "begins_with":
			whereParts = append(whereParts, fmt.Sprintf("%s ILIKE $%d", columnName, paramIndex))
			values = append(values, fmt.Sprintf("%s%%", condition.Value))
			paramIndex++
		}
	}

	// Build ORDER BY clause
	var orderParts []string
	for _, sortField := range request.SortFields {
		// Check if the field mapping exists before adding to ORDER BY
		if !p.isStaticField(sortField.Field) {
			if categoryID != -1 {
				// Category-specific search
				if !p.fieldMappingExists(sortField.Field, categoryID) {
					log.Printf("WARNING: Skipping sort field for unmapped field: %s in category %d", sortField.Field, categoryID)
					continue
				}
			} else {
				// Cross-category search
				if !p.fieldMappingExistsInAnyCategory(sortField.Field) {
					log.Printf("WARNING: Skipping sort field for unmapped field: %s (not found in any category)", sortField.Field)
					continue
				}
			}
		}

		columnName := p.mapFieldToColumnWithCategory(sortField.Field, categoryID)
		direction := "ASC"
		if strings.ToUpper(sortField.Order) == "DESC" {
			direction = "DESC"
		}
		orderParts = append(orderParts, fmt.Sprintf("%s %s", columnName, direction))
	}

	// Build complete query
	selectSQL := fmt.Sprintf("SELECT * FROM %s", p.tableName)
	if len(whereParts) > 0 {
		selectSQL += " WHERE " + strings.Join(whereParts, " AND ")
	}
	if len(orderParts) > 0 {
		selectSQL += " ORDER BY " + strings.Join(orderParts, ", ")
	} else {
		selectSQL += " ORDER BY created_at DESC"
	}

	// Log query execution time
	queryStart := time.Now()
	rows, err := p.db.QueryContext(ctx, selectSQL, values...)
	queryDuration := time.Since(queryStart)

	if err != nil {
		p.logger.LogQueryExecution("SEARCH_TICKETS_FAILED", queryDuration, 0)
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}
	defer rows.Close()

	p.logger.LogQueryExecution("SEARCH_TICKETS", queryDuration, 1)

	// Log comprehensive search information
	categoryInfo := "cross-category"
	if categoryID != -1 {
		categoryInfo = fmt.Sprintf("category-%d", categoryID)
	}

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	var tickets []*ticketpb.TicketData
	rowCount := 0

	// Track row scanning time
	scanStart := time.Now()

	for rows.Next() {
		rowCount++
		// Create slice to hold values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan the row
		if err := rows.Scan(valuePtrs...); err != nil {
			log.Printf("Failed to scan ticket row: %v", err)
			continue
		}

		// Convert to map
		row := make(map[string]interface{})
		for i, column := range columns {
			row[column] = values[i]
		}

		// Extract category ID for mapping
		var categoryID int64 = 1 // default
		if catVal, exists := row["categoryid"]; exists && catVal != nil {
			if intVal, ok := catVal.(int64); ok {
				categoryID = intVal
			}
		}

		// Convert back to protobuf
		ticketData := p.dynamicRowToProtobuf(row, categoryID)
		tickets = append(tickets, ticketData)
	}

	scanDuration := time.Since(scanStart)
	p.logger.LogRowScan("SEARCH_TICKETS", scanDuration, rowCount)

	searchDuration := time.Since(searchStart)

	// Log comprehensive search results
	p.logger.LogSearchResults("SEARCH_TICKETS", len(tickets), searchDuration, false)

	log.Printf("SEARCH: %s | mapped_columns=[%s] | query_time=%v",
		categoryInfo,
		strings.Join(mappedColumns, ", "),
		searchDuration)

	log.Printf("Found %d tickets matching search criteria in dynamic table %s", len(tickets), p.tableName)
	return tickets, nil
}

// SearchTicketsWithProjection searches for tickets with field projection
func (p *PostgreSQLDynamicStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	// For simplicity, use the same implementation as SearchTickets
	// In a production system, you might optimize this to only select required columns
	tickets, err := p.SearchTickets(request)
	if err != nil {
		return nil, err
	}

	// If no projected fields specified, return all tickets
	if len(request.ProjectedFields) == 0 {
		return tickets, nil
	}

	// Filter tickets to only include projected fields
	var filteredTickets []*ticketpb.TicketData
	for _, ticket := range tickets {
		filteredTicket := &ticketpb.TicketData{
			Id:        ticket.Id,
			CreatedAt: ticket.CreatedAt,
			UpdatedAt: ticket.UpdatedAt,
			Fields:    make(map[string]*ticketpb.FieldValue),
		}

		// Include only projected fields
		for _, fieldName := range request.ProjectedFields {
			if fieldValue, exists := ticket.Fields[fieldName]; exists {
				filteredTicket.Fields[fieldName] = fieldValue
			}
		}

		filteredTickets = append(filteredTickets, filteredTicket)
	}

	return filteredTickets, nil
}

// mapFieldToColumn maps a field name to the appropriate database column for a specific category
// This handles both static fields and dynamic field mappings (all in-memory)
func (p *PostgreSQLDynamicStorage) mapFieldToColumn(fieldName string, categoryID int64) string {
	// Static field mappings - must match the staticFields map in protobufToDynamicRow
	// Note: timestamp fields (createdtime, updatedtime) are excluded and treated as dynamic fields
	staticFields := map[string]string{
		"id":          "ticket_id",
		"ticket_id":   "ticket_id",
		"created_at":  "created_at",
		"updated_at":  "updated_at",
		"name":        "name",
		"createdbyid": "createdbyid",
		"updatedbyid": "updatedbyid",
		"statusid":    "statusid",
		"priorityid":  "priorityid",
		"requesterid": "requesterid",
		"subject":     "subject",
		// Timestamp fields are now handled as dynamic fields:
		// "createdtime": "createdtime",  // ← Removed - now dynamic
		// "updatedtime": "updatedtime",  // ← Removed - now dynamic
	}

	if column, exists := staticFields[fieldName]; exists {
		return column
	}

	// For dynamic fields, search through in-memory mappings for the specific category
	// This ensures category isolation - each category has independent field mappings
	p.mappingMutex.RLock()
	defer p.mappingMutex.RUnlock()

	categoryKey := fmt.Sprintf("%d", categoryID)
	if categoryMappings, exists := p.fieldMappings[categoryKey]; exists {
		if mapping, exists := categoryMappings[fieldName]; exists {
			log.Printf("DEBUG: Mapped field %s to column %s (category %d)", fieldName, mapping.ColumnName, categoryID)
			return mapping.ColumnName
		}
	}

	// If no mapping found for this specific category, this field doesn't exist in this category yet
	log.Printf("WARNING: No mapping found for field %s in category %d. This field may not exist in this category yet.", fieldName, categoryID)

	// Return a safe fallback - we'll handle this in the search function
	return fieldName
}

// fieldMappingExists checks if a field has been mapped to a column in a specific category
func (p *PostgreSQLDynamicStorage) fieldMappingExists(fieldName string, categoryID int64) bool {
	p.mappingMutex.RLock()
	defer p.mappingMutex.RUnlock()

	categoryKey := fmt.Sprintf("%d", categoryID)
	if categoryMappings, exists := p.fieldMappings[categoryKey]; exists {
		_, exists := categoryMappings[fieldName]
		return exists
	}
	return false
}

// fieldMappingExistsInAnyCategory checks if a field has been mapped in any category
func (p *PostgreSQLDynamicStorage) fieldMappingExistsInAnyCategory(fieldName string) bool {
	p.mappingMutex.RLock()
	defer p.mappingMutex.RUnlock()

	for _, categoryMappings := range p.fieldMappings {
		if _, exists := categoryMappings[fieldName]; exists {
			return true
		}
	}
	return false
}

// isStaticField checks if a field is a static field
func (p *PostgreSQLDynamicStorage) isStaticField(fieldName string) bool {
	staticFields := map[string]bool{
		"id": true, "ticket_id": true, "created_at": true, "updated_at": true,
		"name": true, "createdbyid": true, "updatedbyid": true,
		"statusid": true, "priorityid": true, "requesterid": true, "subject": true,
	}
	return staticFields[fieldName]
}

// mapFieldToColumnWithCategory maps a field to column with category awareness
// If categoryID is -1, it searches across all categories (for cross-category searches)
func (p *PostgreSQLDynamicStorage) mapFieldToColumnWithCategory(fieldName string, categoryID int64) string {
	// Static field mappings - same for all categories
	staticFields := map[string]string{
		"id":          "ticket_id",
		"ticket_id":   "ticket_id",
		"created_at":  "created_at",
		"updated_at":  "updated_at",
		"name":        "name",
		"createdbyid": "createdbyid",
		"updatedbyid": "updatedbyid",
		"statusid":    "statusid",
		"priorityid":  "priorityid",
		"requesterid": "requesterid",
		"subject":     "subject",
	}

	if column, exists := staticFields[fieldName]; exists {
		return column
	}

	if categoryID == -1 {
		// Cross-category search - find the field in any category
		p.mappingMutex.RLock()
		defer p.mappingMutex.RUnlock()

		for categoryKey, categoryMappings := range p.fieldMappings {
			if mapping, exists := categoryMappings[fieldName]; exists {
				log.Printf("DEBUG: Cross-category mapped field %s to column %s (found in category %s)", fieldName, mapping.ColumnName, categoryKey)
				return mapping.ColumnName
			}
		}

		log.Printf("WARNING: No mapping found for field %s in any category", fieldName)
		return fieldName
	}

	// Category-specific search
	return p.mapFieldToColumn(fieldName, categoryID)
}

// Close closes the database connection after flushing any pending field mappings
func (p *PostgreSQLDynamicStorage) Close() error {
	// Flush any pending field mappings before closing
	if err := p.FlushFieldMappings(); err != nil {
		log.Printf("Warning: Failed to flush field mappings during close: %v", err)
	}

	if p.db != nil {
		err := p.db.Close()
		if err != nil {
			return fmt.Errorf("failed to close database connection: %w", err)
		}
		log.Printf("Closed PostgreSQL dynamic storage connection")
	}
	return nil
}

// GetFieldMappings returns the current field mappings for debugging/monitoring
func (p *PostgreSQLDynamicStorage) GetFieldMappings() map[string]map[string]FieldMapping {
	p.mappingMutex.RLock()
	defer p.mappingMutex.RUnlock()

	// Create a deep copy to avoid race conditions
	result := make(map[string]map[string]FieldMapping)
	for categoryKey, categoryMappings := range p.fieldMappings {
		result[categoryKey] = make(map[string]FieldMapping)
		for fieldName, mapping := range categoryMappings {
			result[categoryKey][fieldName] = mapping
		}
	}

	return result
}

// GetAvailableColumns returns the number of available columns for each category
func (p *PostgreSQLDynamicStorage) GetAvailableColumns(categoryID int64) (stringColumns, numericColumns int) {
	p.mappingMutex.RLock()
	defer p.mappingMutex.RUnlock()

	stringUsed := p.nextStringColumn[categoryID]
	numericUsed := p.nextNumericColumn[categoryID]

	return 50 - stringUsed, 50 - numericUsed
}

// FlushFieldMappings forces all pending field mappings to be persisted to database
// This can be called periodically or during graceful shutdown
func (p *PostgreSQLDynamicStorage) FlushFieldMappings() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p.mappingMutex.RLock()
	var allMappings []FieldMapping
	for _, categoryMappings := range p.fieldMappings {
		for _, mapping := range categoryMappings {
			allMappings = append(allMappings, mapping)
		}
	}
	p.mappingMutex.RUnlock()

	if len(allMappings) == 0 {
		return nil
	}

	// Batch insert/update all mappings
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO field_mappings (category_id, field_name, column_name, data_type)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (category_id, field_name) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, mapping := range allMappings {
		_, err := stmt.ExecContext(ctx, mapping.CategoryID, mapping.FieldName, mapping.ColumnName, mapping.DataType)
		if err != nil {
			return fmt.Errorf("failed to insert mapping: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("Flushed %d field mappings to database", len(allMappings))
	return nil
}

// ReloadFieldMappings reloads field mappings from database into memory
// This can be used to sync mappings across multiple application instances
func (p *PostgreSQLDynamicStorage) ReloadFieldMappings() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return p.loadFieldMappings(ctx)
}
