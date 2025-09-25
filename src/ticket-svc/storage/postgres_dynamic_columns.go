package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/platform/ticket-svc/logger"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
	"github.com/redis/go-redis/v9"
)

// FieldMapping represents a mapping between a field name and a database column
type FieldMapping struct {
	CategoryID int64  `json:"category_id"`
	FieldName  string `json:"field_name"`
	ColumnName string `json:"column_name"`
	DataType   string `json:"data_type"` // "string", "numeric", "array_string", "array_numeric", or "geolocation"
}

// PostgreSQLDynamicStorage implements ticket storage using PostgreSQL with dynamic column mapping
// Uses static base columns + 50 string columns + 50 numeric columns + array columns + geolocation columns
// Custom fields are mapped to available columns per category
// Integrated with Dragonfly for indexing and caching
type PostgreSQLDynamicStorage struct {
	db                     *sql.DB
	tableName              string
	mappingTableName       string
	fieldMappings          map[string]map[string]FieldMapping // categoryID -> fieldName -> mapping
	mappingMutex           sync.RWMutex                       // protects fieldMappings
	nextStringColumn       map[int64]int                      // categoryID -> next available string column number
	nextNumericColumn      map[int64]int                      // categoryID -> next available numeric column number
	nextArrayStringColumn  map[int64]int                      // categoryID -> next available array string column number
	nextArrayNumericColumn map[int64]int                      // categoryID -> next available array numeric column number
	nextGeolocationColumn  map[int64]int                      // categoryID -> next available geolocation column number

	// Dragonfly integration for indexing
	dragonflyClient  *redis.Client   // Dragonfly client for indexing
	dragonflyEnabled bool            // Whether Dragonfly indexing is enabled
	staticIndexes    map[string]bool // tracks created static field indexes in Dragonfly

	// Dragonfly cache for ticket data (separate connection)
	cacheClient    *redis.Client   // Dragonfly client for ticket caching (port 6380)
	cacheEnabled   bool            // Whether ticket caching is enabled
	dynamicIndexes map[string]bool // tracks created dynamic column indexes in Dragonfly
	indexMutex     sync.RWMutex    // protects index tracking

	logger logger.Logger // logger for performance metrics
}

// NewPostgreSQLDynamicStorage creates a new PostgreSQL storage instance with dynamic column mapping and Dragonfly integration
func NewPostgreSQLDynamicStorage(ctx context.Context, tableName, connectionString string) (*PostgreSQLDynamicStorage, error) {
	return NewPostgreSQLDynamicStorageWithDragonflyAndCache(ctx, tableName, connectionString, "localhost:6379", "", 0, true, "localhost:6380", "", 0, true)
}

// NewPostgreSQLDynamicStorageWithDragonfly creates a new PostgreSQL storage instance with optional Dragonfly integration
func NewPostgreSQLDynamicStorageWithDragonfly(ctx context.Context, tableName, connectionString, dragonflyAddr, dragonflyPassword string, dragonflyDB int, enableDragonfly bool) (*PostgreSQLDynamicStorage, error) {
	return NewPostgreSQLDynamicStorageWithDragonflyAndCache(ctx, tableName, connectionString, dragonflyAddr, dragonflyPassword, dragonflyDB, enableDragonfly, "localhost:6380", "", 0, true)
}

// NewPostgreSQLDynamicStorageWithDragonflyAndCache creates a new PostgreSQL storage instance with Dragonfly indexing and caching
func NewPostgreSQLDynamicStorageWithDragonflyAndCache(ctx context.Context, tableName, connectionString, dragonflyAddr, dragonflyPassword string, dragonflyDB int, enableDragonfly bool, cacheAddr, cachePassword string, cacheDB int, enableCache bool) (*PostgreSQLDynamicStorage, error) {
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

	// Initialize Dragonfly client if enabled
	var dragonflyClient *redis.Client
	if enableDragonfly && dragonflyAddr != "" {
		dragonflyClient = redis.NewClient(&redis.Options{
			Addr:         dragonflyAddr,
			Password:     dragonflyPassword,
			DB:           dragonflyDB,
			PoolSize:     20,
			MinIdleConns: 5,
			DialTimeout:  5 * time.Second,
			ReadTimeout:  3 * time.Second,
			WriteTimeout: 3 * time.Second,
		})

		// Test Dragonfly connection
		_, err = dragonflyClient.Ping(ctx).Result()
		if err != nil {
			log.Printf("Warning: Failed to connect to Dragonfly at %s: %v. Continuing without Dragonfly indexing.", dragonflyAddr, err)
			dragonflyClient = nil
			enableDragonfly = false
		} else {
			log.Printf("Dragonfly connection established successfully at %s", dragonflyAddr)
		}
	}

	// Initialize Cache client if enabled
	var cacheClient *redis.Client
	if enableCache && cacheAddr != "" {
		cacheClient = redis.NewClient(&redis.Options{
			Addr:         cacheAddr,
			Password:     cachePassword,
			DB:           cacheDB,
			PoolSize:     20,
			MinIdleConns: 5,
			DialTimeout:  5 * time.Second,
			ReadTimeout:  3 * time.Second,
			WriteTimeout: 3 * time.Second,
		})

		// Test Cache connection
		_, err = cacheClient.Ping(ctx).Result()
		if err != nil {
			log.Printf("Warning: Failed to connect to Cache at %s: %v. Continuing without ticket caching.", cacheAddr, err)
			cacheClient = nil
			enableCache = false
		} else {
			log.Printf("Cache connection established successfully at %s", cacheAddr)
		}
	}

	storage := &PostgreSQLDynamicStorage{
		db:                     db,
		tableName:              tableName,
		mappingTableName:       "field_mappings",
		fieldMappings:          make(map[string]map[string]FieldMapping),
		nextStringColumn:       make(map[int64]int),
		nextNumericColumn:      make(map[int64]int),
		nextArrayStringColumn:  make(map[int64]int),
		nextArrayNumericColumn: make(map[int64]int),
		nextGeolocationColumn:  make(map[int64]int),
		dragonflyClient:        dragonflyClient,
		dragonflyEnabled:       enableDragonfly,
		staticIndexes:          make(map[string]bool),
		dynamicIndexes:         make(map[string]bool),
		cacheClient:            cacheClient,
		cacheEnabled:           enableCache,
		logger:                 logger.NewLogger("postgresql-storage", "ticket-svc"),
	}

	// Ensure the tables exist
	if err := storage.ensureTablesExist(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure tables exist: %w", err)
	}

	// Load existing field mappings into memory
	if err := storage.loadFieldMappings(ctx); err != nil {
		return nil, fmt.Errorf("failed to load field mappings: %w", err)
	}

	// Create static field indexes in Dragonfly if enabled
	if storage.dragonflyEnabled {
		if err := storage.createStaticFieldIndexes(ctx); err != nil {
			log.Printf("Warning: Failed to create static field indexes in Dragonfly: %v", err)
		}
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
	p.nextArrayStringColumn = make(map[int64]int)
	p.nextArrayNumericColumn = make(map[int64]int)
	p.nextGeolocationColumn = make(map[int64]int)

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
		switch mapping.DataType {
		case "string":
			columnNum := extractColumnNumber(mapping.ColumnName, "string")
			if columnNum >= p.nextStringColumn[mapping.CategoryID] {
				p.nextStringColumn[mapping.CategoryID] = columnNum + 1
			}
		case "numeric":
			columnNum := extractColumnNumber(mapping.ColumnName, "numeric")
			if columnNum >= p.nextNumericColumn[mapping.CategoryID] {
				p.nextNumericColumn[mapping.CategoryID] = columnNum + 1
			}
		case "array_string":
			columnNum := extractColumnNumber(mapping.ColumnName, "array_string")
			if columnNum >= p.nextArrayStringColumn[mapping.CategoryID] {
				p.nextArrayStringColumn[mapping.CategoryID] = columnNum + 1
			}
		case "array_numeric":
			columnNum := extractColumnNumber(mapping.ColumnName, "array_numeric")
			if columnNum >= p.nextArrayNumericColumn[mapping.CategoryID] {
				p.nextArrayNumericColumn[mapping.CategoryID] = columnNum + 1
			}
		case "geolocation":
			columnNum := extractColumnNumber(mapping.ColumnName, "geolocation")
			if columnNum >= p.nextGeolocationColumn[mapping.CategoryID] {
				p.nextGeolocationColumn[mapping.CategoryID] = columnNum + 1
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
	var isNewColumn bool = true

	switch dataType {
	case "string":
		nextColumn = p.nextStringColumn[categoryID]
		if nextColumn == 0 {
			nextColumn = 1 // Start from c1_string
		}
		if nextColumn > 50 {
			return FieldMapping{}, fmt.Errorf("no more string columns available for category %d (max 50)", categoryID)
		}
		columnName = fmt.Sprintf("c%d_string", nextColumn)
		p.nextStringColumn[categoryID] = nextColumn + 1

	case "numeric":
		nextColumn = p.nextNumericColumn[categoryID]
		if nextColumn == 0 {
			nextColumn = 1 // Start from c1_numeric
		}
		if nextColumn > 50 {
			return FieldMapping{}, fmt.Errorf("no more numeric columns available for category %d (max 50)", categoryID)
		}
		columnName = fmt.Sprintf("c%d_numeric", nextColumn)
		p.nextNumericColumn[categoryID] = nextColumn + 1

	case "array_string":
		nextColumn = p.nextArrayStringColumn[categoryID]
		if nextColumn == 0 {
			nextColumn = 1 // Start from c1_array_string
		}
		if nextColumn > 2 {
			return FieldMapping{}, fmt.Errorf("no more array string columns available for category %d (max 2)", categoryID)
		}
		columnName = fmt.Sprintf("c%d_array_string", nextColumn)
		p.nextArrayStringColumn[categoryID] = nextColumn + 1

	case "array_numeric":
		nextColumn = p.nextArrayNumericColumn[categoryID]
		if nextColumn == 0 {
			nextColumn = 1 // Start from c1_array_numeric
		}
		if nextColumn > 2 {
			return FieldMapping{}, fmt.Errorf("no more array numeric columns available for category %d (max 2)", categoryID)
		}
		columnName = fmt.Sprintf("c%d_array_numeric", nextColumn)
		p.nextArrayNumericColumn[categoryID] = nextColumn + 1

	case "geolocation":
		nextColumn = p.nextGeolocationColumn[categoryID]
		if nextColumn == 0 {
			nextColumn = 1 // Start from c1_geolocation
		}
		if nextColumn > 2 {
			return FieldMapping{}, fmt.Errorf("no more geolocation columns available for category %d (max 2)", categoryID)
		}
		columnName = fmt.Sprintf("c%d_geolocation", nextColumn)
		p.nextGeolocationColumn[categoryID] = nextColumn + 1

	default:
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

	// Create Dragonfly index for the new dynamic column if enabled and it's a new column
	if p.dragonflyEnabled && isNewColumn {
		go p.createDynamicColumnIndex(columnName, dataType)
	}

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
	switch v := fieldValue.Value.(type) {
	case *ticketpb.FieldValue_StringValue:
		// Check if it's a geolocation (POINT format)
		if strings.HasPrefix(v.StringValue, "POINT(") {
			return "geolocation"
		}
		return "string"
	case *ticketpb.FieldValue_BytesValue:
		return "string"
	case *ticketpb.FieldValue_StringArray:
		return "array_string"
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
				if mapping.DataType == "geolocation" {
					// Store geolocation as POINT type
					value = v.StringValue
				} else {
					value = v.StringValue
				}
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
				if mapping.DataType == "array_string" {
					// Store as PostgreSQL TEXT[] array
					value = v.StringArray.Values
				} else {
					// Fallback: join array as comma-separated string
					value = strings.Join(v.StringArray.Values, ",")
				}
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

// CacheableTicket represents a simplified ticket structure for caching
type CacheableTicket struct {
	Id     string                 `json:"id"`
	Fields map[string]interface{} `json:"fields"`
}

// createCacheableTicket creates a ticket data object suitable for caching (without descriptions)
func (p *PostgreSQLDynamicStorage) createCacheableTicket(ticketData *ticketpb.TicketData) *CacheableTicket {
	// Create a simplified ticket structure for caching
	cacheableTicket := &CacheableTicket{
		Id:     ticketData.Id,
		Fields: make(map[string]interface{}),
	}

	// Create a snapshot of the fields map to avoid concurrent access issues
	// This prevents "concurrent map iteration and map write" errors
	fieldsCopy := make(map[string]*ticketpb.FieldValue)
	if ticketData.Fields != nil {
		for k, v := range ticketData.Fields {
			fieldsCopy[k] = v
		}
	}

	// Copy all fields except description and originaldescription
	for fieldName, fieldValue := range fieldsCopy {
		if fieldName != "description" && fieldName != "originaldescription" {
			// Convert protobuf FieldValue to simple interface{}
			switch v := fieldValue.Value.(type) {
			case *ticketpb.FieldValue_StringValue:
				cacheableTicket.Fields[fieldName] = v.StringValue
			case *ticketpb.FieldValue_IntValue:
				cacheableTicket.Fields[fieldName] = v.IntValue
			case *ticketpb.FieldValue_DoubleValue:
				cacheableTicket.Fields[fieldName] = v.DoubleValue
			case *ticketpb.FieldValue_BoolValue:
				cacheableTicket.Fields[fieldName] = v.BoolValue
			case *ticketpb.FieldValue_BytesValue:
				cacheableTicket.Fields[fieldName] = v.BytesValue
			case *ticketpb.FieldValue_StringArray:
				cacheableTicket.Fields[fieldName] = v.StringArray.Values
			}
		}
	}

	return cacheableTicket
}

// convertCacheableToProtobuf converts a CacheableTicket back to protobuf TicketData
func (p *PostgreSQLDynamicStorage) convertCacheableToProtobuf(cacheable *CacheableTicket) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Id:     cacheable.Id,
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Convert simple interface{} values back to protobuf FieldValue
	for fieldName, value := range cacheable.Fields {
		switch v := value.(type) {
		case string:
			ticketData.Fields[fieldName] = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_StringValue{StringValue: v},
			}
		case float64:
			// JSON unmarshaling converts all numbers to float64
			// Check if it's actually an integer
			if v == float64(int64(v)) {
				ticketData.Fields[fieldName] = &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
				}
			} else {
				ticketData.Fields[fieldName] = &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: v},
				}
			}
		case bool:
			ticketData.Fields[fieldName] = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_BoolValue{BoolValue: v},
			}
		case []interface{}:
			// Handle string arrays
			stringArray := make([]string, len(v))
			for i, item := range v {
				if str, ok := item.(string); ok {
					stringArray[i] = str
				}
			}
			ticketData.Fields[fieldName] = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_StringArray{
					StringArray: &ticketpb.StringArray{Values: stringArray},
				},
			}
		}
	}

	return ticketData
}

// getCachedTickets retrieves tickets from cache by IDs
func (p *PostgreSQLDynamicStorage) getCachedTickets(ctx context.Context, ticketIDs []string) (map[string]*ticketpb.TicketData, []string, error) {
	if !p.cacheEnabled || p.cacheClient == nil {
		return nil, ticketIDs, nil // Return all IDs as uncached
	}

	// Start timing cache operation
	cacheStart := time.Now()

	cachedTickets := make(map[string]*ticketpb.TicketData)
	var uncachedIDs []string

	// Batch get from cache
	cacheKeys := make([]string, len(ticketIDs))
	for i, id := range ticketIDs {
		cacheKeys[i] = fmt.Sprintf("ticket:%s", id)
	}

	// Execute cache lookup and measure time
	lookupStart := time.Now()
	results, err := p.cacheClient.MGet(ctx, cacheKeys...).Result()
	lookupDuration := time.Since(lookupStart)

	if err != nil {
		log.Printf("Warning: Failed to get tickets from cache: %v", err)
		p.logger.Error(fmt.Sprintf("Cache lookup failed: requested=%d, duration=%dms, error=%s",
			len(ticketIDs), lookupDuration.Milliseconds(), err.Error()))
		return nil, ticketIDs, nil // Return all IDs as uncached on error
	}

	// Process cache results and measure deserialization time
	deserializationStart := time.Now()
	var deserializationErrors int

	for i, result := range results {
		if result != nil {
			// Cache hit - deserialize ticket data using standard JSON
			if ticketJSON, ok := result.(string); ok {
				var cacheableTicket CacheableTicket
				if err := json.Unmarshal([]byte(ticketJSON), &cacheableTicket); err == nil {
					// Convert back to protobuf format
					ticketData := p.convertCacheableToProtobuf(&cacheableTicket)
					cachedTickets[ticketIDs[i]] = ticketData
				} else {
					log.Printf("Warning: Failed to unmarshal cached ticket %s: %v", ticketIDs[i], err)
					uncachedIDs = append(uncachedIDs, ticketIDs[i])
					deserializationErrors++
				}
			} else {
				uncachedIDs = append(uncachedIDs, ticketIDs[i])
			}
		} else {
			// Cache miss
			uncachedIDs = append(uncachedIDs, ticketIDs[i])
		}
	}

	deserializationDuration := time.Since(deserializationStart)
	totalCacheDuration := time.Since(cacheStart)

	// Calculate cache metrics
	cacheHits := len(cachedTickets)
	cacheMisses := len(uncachedIDs)
	hitRate := float64(cacheHits) / float64(len(ticketIDs)) * 100

	// Log detailed cache performance metrics
	log.Printf("Cache performance: %d hits, %d misses for %d tickets (%.1f%% hit rate)",
		cacheHits, cacheMisses, len(ticketIDs), hitRate)

	// Log cache performance metrics
	p.logger.Info(fmt.Sprintf("Cache lookup performance: requested=%d, hits=%d, misses=%d, hit_rate=%.1f%%, lookup=%dms, deserial=%dms, total=%dms, errors=%d",
		len(ticketIDs), cacheHits, cacheMisses, hitRate,
		lookupDuration.Milliseconds(), deserializationDuration.Milliseconds(),
		totalCacheDuration.Milliseconds(), deserializationErrors))

	return cachedTickets, uncachedIDs, nil
}

// cacheTickets stores tickets in cache (without descriptions)
func (p *PostgreSQLDynamicStorage) cacheTickets(ctx context.Context, tickets []*ticketpb.TicketData) {
	if !p.cacheEnabled || p.cacheClient == nil {
		return
	}

	// Start timing cache storage operation
	cacheStart := time.Now()

	// Prepare batch cache operations
	pipe := p.cacheClient.Pipeline()
	var serializationErrors int
	var successfulTickets int

	// Measure serialization time
	serializationStart := time.Now()

	for _, ticket := range tickets {
		// Create cacheable version (without descriptions)
		cacheableTicket := p.createCacheableTicket(ticket)

		// Serialize to standard JSON
		ticketJSON, err := json.Marshal(cacheableTicket)
		if err != nil {
			log.Printf("Warning: Failed to marshal ticket %s for caching: %v", ticket.Id, err)
			serializationErrors++
			continue
		}

		// Set in cache with TTL (e.g., 1 hour)
		cacheKey := fmt.Sprintf("ticket:%s", ticket.Id)
		pipe.Set(ctx, cacheKey, string(ticketJSON), time.Hour)
		successfulTickets++
	}

	serializationDuration := time.Since(serializationStart)

	// Execute batch operation and measure time
	storageStart := time.Now()
	_, err := pipe.Exec(ctx)
	storageDuration := time.Since(storageStart)
	totalCacheDuration := time.Since(cacheStart)

	if err != nil {
		log.Printf("Warning: Failed to cache tickets: %v", err)

		// Log cache storage failure
		p.logger.Error(fmt.Sprintf("Cache storage failed: requested=%d, successful=%d, errors=%d, serial=%dms, total=%dms, error=%s",
			len(tickets), successfulTickets, serializationErrors,
			serializationDuration.Milliseconds(), totalCacheDuration.Milliseconds(), err.Error()))
	} else {
		log.Printf("Successfully cached %d tickets", successfulTickets)

		// Log successful cache storage performance
		p.logger.Info(fmt.Sprintf("Cache storage success: cached=%d, errors=%d, serial=%dms, storage=%dms, total=%dms",
			successfulTickets, serializationErrors, serializationDuration.Milliseconds(),
			storageDuration.Milliseconds(), totalCacheDuration.Milliseconds()))
	}
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

	// Update Dragonfly indexes asynchronously if enabled
	if p.dragonflyEnabled {
		go p.updateDragonflyIndexes(ticketData.Id, row)
	}

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

	// Check cache first
	if p.cacheEnabled && p.cacheClient != nil {
		cacheStart := time.Now()
		cacheKey := fmt.Sprintf("ticket:%s", id)

		// Execute cache lookup
		lookupStart := time.Now()
		cachedData, err := p.cacheClient.Get(ctx, cacheKey).Result()
		lookupDuration := time.Since(lookupStart)

		if err == nil && cachedData != "" {
			// Cache hit - deserialize and return
			deserializationStart := time.Now()
			var cacheableTicket CacheableTicket
			if err := json.Unmarshal([]byte(cachedData), &cacheableTicket); err == nil {
				// Convert back to protobuf format
				ticketData := p.convertCacheableToProtobuf(&cacheableTicket)
				deserializationDuration := time.Since(deserializationStart)
				totalCacheDuration := time.Since(cacheStart)

				log.Printf("Cache hit: Retrieved ticket %s from cache", id)

				// Log cache hit performance
				p.logger.Info(fmt.Sprintf("Single ticket cache hit: id=%s, lookup=%dms, deserial=%dms, total=%dms",
					id, lookupDuration.Milliseconds(), deserializationDuration.Milliseconds(), totalCacheDuration.Milliseconds()))

				return ticketData, true
			} else {
				log.Printf("Warning: Failed to unmarshal cached ticket %s: %v", id, err)

				// Log deserialization error
				p.logger.Error(fmt.Sprintf("Single ticket cache deserialization error: id=%s, lookup=%dms, error=%s",
					id, lookupDuration.Milliseconds(), err.Error()))
			}
		} else {
			// Cache miss
			totalCacheDuration := time.Since(cacheStart)

			// Log cache miss
			p.logger.Info(fmt.Sprintf("Single ticket cache miss: id=%s, lookup=%dms, total=%dms, error=%t",
				id, lookupDuration.Milliseconds(), totalCacheDuration.Milliseconds(), err != nil))
		}
	}

	// Cache miss or cache disabled - fetch from PostgreSQL
	log.Printf("Cache miss: Fetching ticket %s from PostgreSQL", id)

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

	// Cache the retrieved ticket synchronously to avoid race conditions
	if p.cacheEnabled {
		p.cacheTickets(context.Background(), []*ticketpb.TicketData{ticketData})
	}

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

	// Invalidate cache for updated ticket (will be re-cached on next access)
	if p.cacheEnabled && p.cacheClient != nil {
		go func() {
			cacheKey := fmt.Sprintf("ticket:%s", ticketData.Id)
			err := p.cacheClient.Del(context.Background(), cacheKey).Err()
			if err != nil {
				log.Printf("Warning: Failed to invalidate cache for ticket %s: %v", ticketData.Id, err)
			}
		}()
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

	// Invalidate cache for deleted ticket
	if p.cacheEnabled && p.cacheClient != nil {
		go func() {
			cacheKey := fmt.Sprintf("ticket:%s", id)
			err := p.cacheClient.Del(context.Background(), cacheKey).Err()
			if err != nil {
				log.Printf("Warning: Failed to invalidate cache for deleted ticket %s: %v", id, err)
			}
		}()
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

// SearchTickets searches for tickets using Dragonfly index-first approach
func (p *PostgreSQLDynamicStorage) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	// Convert SearchRequest to ExtendedSearchRequest for Dragonfly index-first search
	extendedRequest := ExtendedSearchRequest{
		SearchRequest: request,
		Logic:         "AND", // Default to AND logic
		Limit:         0,     // No limit by default
		Offset:        0,     // No offset by default
	}

	// Use the Dragonfly index-first search method
	return p.SearchTicketsWithDragonflyIndex(extendedRequest)
}

// SearchTicketsWithProjection searches for tickets with field projection using Dragonfly index-first approach
func (p *PostgreSQLDynamicStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	// Use the Dragonfly index-first search method (projection is handled in the protobuf conversion)
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

// Close closes the database connection and Dragonfly client after flushing any pending field mappings
func (p *PostgreSQLDynamicStorage) Close() error {
	// Flush any pending field mappings before closing
	if err := p.FlushFieldMappings(); err != nil {
		log.Printf("Warning: Failed to flush field mappings during close: %v", err)
	}

	// Close Dragonfly client if enabled
	if p.dragonflyClient != nil {
		if err := p.dragonflyClient.Close(); err != nil {
			log.Printf("Warning: Failed to close Dragonfly connection: %v", err)
		} else {
			log.Printf("Closed Dragonfly connection")
		}
	}

	// Close Cache client if enabled
	if p.cacheClient != nil {
		if err := p.cacheClient.Close(); err != nil {
			log.Printf("Warning: Failed to close Cache connection: %v", err)
		} else {
			log.Printf("Closed Cache connection")
		}
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

// ===== DRAGONFLY INTEGRATION METHODS =====

// createStaticFieldIndexes creates indexes for all static fields in Dragonfly
func (p *PostgreSQLDynamicStorage) createStaticFieldIndexes(ctx context.Context) error {
	if !p.dragonflyEnabled || p.dragonflyClient == nil {
		return nil
	}

	staticFields := []string{
		// Core fields
		"ticket_id", "created_at", "updated_at", "id",

		// User and assignment fields
		"updatedbyid", "createdbyid", "removedbyid", "requesterid",
		"technicianid", "closedby", "resolvedby",

		// Timestamp fields
		"updatedtime", "createdtime", "removedtime", "dueby",
		"firstresponsetime", "lastclosedtime", "lastopenedtime",
		"lastresolvedtime", "lastviolationtime", "olddueby",
		"oldresponsedue", "resolutionescalationtime", "responsedue",
		"responseescalationtime", "statuschangedtime", "groupchangedtime",
		"lastolaviolationtime", "oladueby", "oldoladueby",
		"askfeedbackdate", "firstfeedbackdate", "olaescalationtime",
		"lastucviolationtime", "olducdueby", "ucdueby",
		"ucescalationtime", "lastapproveddate",

		// Text fields
		"name", "oobtype", "description", "originaldescription",
		"subject", "callfrom", "emailreadconfigemail",

		// Boolean fields
		"removed", "duetimemanuallyupdated", "reopened",
		"responsedueviolated", "slaviolated", "purchaserequest",
		"spam", "viprequest", "olaviolated", "ucviolated", "migrated",

		// Category and classification fields
		"categoryid", "departmentid", "groupid", "impactid",
		"locationid", "priorityid", "statusid", "urgencyid",
		"violatedslaid", "servicecatalogid", "sourceid", "requesttype",
		"suggestedcategoryid", "suggestedgroupid", "companyid",
		"vendorid", "violateducid", "transitionmodelid", "messengerconfigid",

		// Approval and workflow fields
		"approvalstatus", "approvaltype", "resolutionduelevel",
		"responseduelevel", "supportlevel", "oladuelevel", "ucduelevel",

		// Duration and time tracking fields
		"totalonholdduration", "totalresolutiontime", "totalslapausetime",
		"totalworkingtime", "totaluconholdduration", "totalucpausetime",
		"totalucworkingtime", "totalucresolutiontime",

		// Configuration and template fields
		"templateid", "emailreadconfigid",
	}

	p.indexMutex.Lock()
	defer p.indexMutex.Unlock()

	for _, fieldName := range staticFields {
		// Skip fields that should not be indexed
		if p.shouldSkipIndexing(fieldName) {
			continue
		}

		indexKey := fmt.Sprintf("static_field_index:%s:%s", p.tableName, fieldName)

		// Check if index already exists
		if p.staticIndexes[indexKey] {
			continue
		}

		// Create index metadata in Dragonfly
		indexMetadata := map[string]interface{}{
			"field_name": fieldName,
			"index_type": "static",
			"table_name": p.tableName,
			"created_at": time.Now().Unix(),
		}

		metadataJSON, _ := json.Marshal(indexMetadata)
		err := p.dragonflyClient.Set(ctx, indexKey, metadataJSON, 0).Err() // No TTL - persist indefinitely
		if err != nil {
			log.Printf("Warning: Failed to create static field index for %s: %v", fieldName, err)
			continue
		}

		p.staticIndexes[indexKey] = true
	}

	log.Printf("Created static field indexes for %d fields in Dragonfly", len(staticFields))
	return nil
}

// createDynamicColumnIndex creates an index for a dynamic column in Dragonfly
func (p *PostgreSQLDynamicStorage) createDynamicColumnIndex(columnName, dataType string) {
	if !p.dragonflyEnabled || p.dragonflyClient == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p.indexMutex.Lock()
	defer p.indexMutex.Unlock()

	indexKey := fmt.Sprintf("dynamic_column_index:%s:%s", p.tableName, columnName)

	// Check if index already exists
	if p.dynamicIndexes[indexKey] {
		return
	}

	// Create index metadata in Dragonfly
	indexMetadata := map[string]interface{}{
		"column_name": columnName,
		"data_type":   dataType,
		"index_type":  "dynamic",
		"table_name":  p.tableName,
		"created_at":  time.Now().Unix(),
	}

	metadataJSON, _ := json.Marshal(indexMetadata)
	err := p.dragonflyClient.Set(ctx, indexKey, metadataJSON, 0).Err() // No TTL - persist indefinitely
	if err != nil {
		log.Printf("Warning: Failed to create dynamic column index for %s: %v", columnName, err)
		return
	}

	p.dynamicIndexes[indexKey] = true
	log.Printf("Created dynamic column index for %s (type: %s) in Dragonfly", columnName, dataType)
}

// updateDragonflyIndexes updates field value indexes in Dragonfly for a ticket using ZSET for range queries
func (p *PostgreSQLDynamicStorage) updateDragonflyIndexes(ticketID string, row map[string]interface{}) {
	if !p.dragonflyEnabled || p.dragonflyClient == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Update indexes for each field value
	for fieldName, value := range row {
		if value == nil {
			continue
		}

		// Skip fields that should not be indexed
		if p.shouldSkipIndexing(fieldName) {
			continue
		}

		// Determine the data type and score for ZSET
		var score float64
		var valueStr string
		var isNumeric bool

		switch v := value.(type) {
		case string:
			valueStr = v
			score = 0 // String values get score 0 for lexicographical ordering
			isNumeric = false
		case int:
			valueStr = fmt.Sprintf("%d", v)
			score = float64(v) // Numeric values get their actual value as score
			isNumeric = true
		case int32:
			valueStr = fmt.Sprintf("%d", v)
			score = float64(v)
			isNumeric = true
		case int64:
			valueStr = fmt.Sprintf("%d", v)
			score = float64(v)
			isNumeric = true
		case float32:
			valueStr = fmt.Sprintf("%.6f", v)
			score = float64(v)
			isNumeric = true
		case float64:
			valueStr = fmt.Sprintf("%.6f", v)
			score = v
			isNumeric = true
		case bool:
			valueStr = fmt.Sprintf("%t", v)
			if v {
				score = 1 // true = 1
			} else {
				score = 0 // false = 0
			}
			isNumeric = true
		default:
			// For complex types (arrays, etc.), convert to JSON
			if jsonBytes, err := json.Marshal(v); err == nil {
				valueStr = string(jsonBytes)
			} else {
				valueStr = fmt.Sprintf("%v", v)
			}
			score = 0 // Complex types get score 0
			isNumeric = false
		}

		// Create field value index key using ZSET with ticket ID lists
		var indexKey string
		if isNumeric {
			// For numeric values, use separate index to enable numeric range queries
			indexKey = fmt.Sprintf("field_numeric_index:%s:%s", p.tableName, fieldName)
		} else {
			// For string values, use separate index for lexicographical queries
			indexKey = fmt.Sprintf("field_string_index:%s:%s", p.tableName, fieldName)
		}

		// Create a key for storing the list of ticket IDs for this specific value
		valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, valueStr)

		// Add ticket ID to the list of tickets with this value
		err := p.dragonflyClient.SAdd(ctx, valueListKey, ticketID).Err()
		if err != nil {
			log.Printf("Warning: Failed to update ticket ID list for %s=%s: %v", fieldName, valueStr, err)
			continue
		}

		// Add entry to ZSET with appropriate score, storing the value as member
		// The ZSET member is just the value, and we use the valueListKey to get ticket IDs
		err = p.dragonflyClient.ZAdd(ctx, indexKey, redis.Z{
			Score:  score,
			Member: valueStr, // Store just the value, not ticket ID
		}).Err()

		if err != nil {
			log.Printf("Warning: Failed to update ZSET field index for %s=%s: %v", fieldName, valueStr, err)
			continue
		}

		// No TTL set - indexes persist indefinitely
	}
}

// ===== DRAGONFLY QUERY METHODS =====

// QueryNumericRange queries tickets by numeric field range using ZSET indexes
func (p *PostgreSQLDynamicStorage) QueryNumericRange(fieldName string, minValue, maxValue float64) ([]string, error) {
	if !p.dragonflyEnabled || p.dragonflyClient == nil {
		return nil, fmt.Errorf("dragonfly not enabled")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	indexKey := fmt.Sprintf("field_numeric_index:%s:%s", p.tableName, fieldName)

	// Query ZSET by score range (numeric values) - returns values, not ticket IDs
	values, err := p.dragonflyClient.ZRangeByScore(ctx, indexKey, &redis.ZRangeBy{
		Min: fmt.Sprintf("%f", minValue),
		Max: fmt.Sprintf("%f", maxValue),
	}).Result()

	if err != nil {
		return nil, fmt.Errorf("failed to query numeric range for %s: %w", fieldName, err)
	}

	// For each value, get the list of ticket IDs
	var allTicketIDs []string
	for _, value := range values {
		valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, value)
		ticketIDs, err := p.dragonflyClient.SMembers(ctx, valueListKey).Result()
		if err != nil {
			log.Printf("Warning: Failed to get ticket IDs for %s=%s: %v", fieldName, value, err)
			continue
		}
		allTicketIDs = append(allTicketIDs, ticketIDs...)
	}

	// Remove duplicates (in case a ticket appears in multiple value ranges)
	uniqueTicketIDs := removeDuplicates(allTicketIDs)
	return uniqueTicketIDs, nil
}

// QueryStringRange queries tickets by string field lexicographical range using ZSET indexes
func (p *PostgreSQLDynamicStorage) QueryStringRange(fieldName string, minValue, maxValue string) ([]string, error) {
	if !p.dragonflyEnabled || p.dragonflyClient == nil {
		return nil, fmt.Errorf("dragonfly not enabled")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	indexKey := fmt.Sprintf("field_string_index:%s:%s", p.tableName, fieldName)

	// Query ZSET by lexicographical range (string values with score 0) - returns values, not ticket IDs
	values, err := p.dragonflyClient.ZRangeByLex(ctx, indexKey, &redis.ZRangeBy{
		Min: fmt.Sprintf("[%s", minValue), // Include minValue
		Max: fmt.Sprintf("[%s", maxValue), // Include maxValue
	}).Result()

	if err != nil {
		return nil, fmt.Errorf("failed to query string range for %s: %w", fieldName, err)
	}

	// For each value, get the list of ticket IDs
	var allTicketIDs []string
	for _, value := range values {
		valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, value)
		ticketIDs, err := p.dragonflyClient.SMembers(ctx, valueListKey).Result()
		if err != nil {
			log.Printf("Warning: Failed to get ticket IDs for %s=%s: %v", fieldName, value, err)
			continue
		}
		allTicketIDs = append(allTicketIDs, ticketIDs...)
	}

	// Remove duplicates
	uniqueTicketIDs := removeDuplicates(allTicketIDs)
	return uniqueTicketIDs, nil
}

// QueryExactMatch queries tickets by exact field value using value list indexes
func (p *PostgreSQLDynamicStorage) QueryExactMatch(fieldName string, value string) ([]string, error) {
	if !p.dragonflyEnabled || p.dragonflyClient == nil {
		return nil, fmt.Errorf("dragonfly not enabled")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use the value list key to get all ticket IDs for this exact value
	valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, value)

	// Query SET for exact matches
	results, err := p.dragonflyClient.SMembers(ctx, valueListKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to query exact match for %s=%s: %w", fieldName, value, err)
	}

	return results, nil
}

// QueryNumericGreaterThan queries tickets where numeric field > value
func (p *PostgreSQLDynamicStorage) QueryNumericGreaterThan(fieldName string, value float64) ([]string, error) {
	return p.QueryNumericRange(fieldName, value+0.000001, math.Inf(1)) // Exclude the value itself
}

// QueryNumericLessThan queries tickets where numeric field < value
func (p *PostgreSQLDynamicStorage) QueryNumericLessThan(fieldName string, value float64) ([]string, error) {
	return p.QueryNumericRange(fieldName, math.Inf(-1), value-0.000001) // Exclude the value itself
}

// QueryStringPrefix queries tickets where string field starts with prefix
func (p *PostgreSQLDynamicStorage) QueryStringPrefix(fieldName string, prefix string) ([]string, error) {
	if !p.dragonflyEnabled || p.dragonflyClient == nil {
		return nil, fmt.Errorf("dragonfly not enabled")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	indexKey := fmt.Sprintf("field_string_index:%s:%s", p.tableName, fieldName)

	// Query ZSET by lexicographical range for prefix matching - returns values, not ticket IDs
	values, err := p.dragonflyClient.ZRangeByLex(ctx, indexKey, &redis.ZRangeBy{
		Min: fmt.Sprintf("[%s", prefix),
		Max: fmt.Sprintf("(%s~", prefix), // Use ~ as upper bound for prefix
	}).Result()

	if err != nil {
		return nil, fmt.Errorf("failed to query string prefix for %s: %w", fieldName, err)
	}

	// For each value that matches the prefix, get the list of ticket IDs
	var allTicketIDs []string
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, value)
			ticketIDs, err := p.dragonflyClient.SMembers(ctx, valueListKey).Result()
			if err != nil {
				log.Printf("Warning: Failed to get ticket IDs for %s=%s: %v", fieldName, value, err)
				continue
			}
			allTicketIDs = append(allTicketIDs, ticketIDs...)
		}
	}

	// Remove duplicates
	uniqueTicketIDs := removeDuplicates(allTicketIDs)
	return uniqueTicketIDs, nil
}

// removeDuplicates removes duplicate strings from a slice
func removeDuplicates(slice []string) []string {
	keys := make(map[string]bool)
	var result []string

	for _, item := range slice {
		if !keys[item] {
			keys[item] = true
			result = append(result, item)
		}
	}

	return result
}

// shouldSkipIndexing determines if a field should be excluded from Dragonfly indexing
func (p *PostgreSQLDynamicStorage) shouldSkipIndexing(fieldName string) bool {
	// Convert to lowercase for case-insensitive comparison
	fieldLower := strings.ToLower(fieldName)

	// Skip description fields (large text fields)
	if fieldLower == "description" || fieldLower == "originaldescription" {
		return true
	}

	// Skip timestamp fields
	if fieldLower == "created_at" || fieldLower == "createdat" ||
		fieldLower == "updated_at" || fieldLower == "updatedat" ||
		fieldLower == "createdtime" || fieldLower == "updatedtime" {
		return true
	}

	// Skip ID fields
	if fieldLower == "id" || fieldLower == "ticket_id" || fieldLower == "ticketid" {
		return true
	}

	return false
}

// ===== ENHANCED SEARCH WITH DRAGONFLY INDEX QUERIES =====

// ExtendedSearchCondition represents an extended search condition with additional fields for range and IN queries
type ExtendedSearchCondition struct {
	SearchCondition             // Embed the existing SearchCondition
	MinValue        interface{} `json:"min_value,omitempty"` // For range queries
	MaxValue        interface{} `json:"max_value,omitempty"` // For range queries
	Values          []string    `json:"values,omitempty"`    // For "in" operator
}

// ExtendedSearchRequest represents an extended search request with logic and pagination
type ExtendedSearchRequest struct {
	SearchRequest        // Embed the existing SearchRequest
	Logic         string `json:"logic,omitempty"` // "AND" or "OR" (default: "AND")
	Limit         int    `json:"limit,omitempty"`
	Offset        int    `json:"offset,omitempty"`
}

// SearchTicketsWithDragonflyIndex performs two-phase search: Dragonfly indexes first, then PostgreSQL data retrieval
func (p *PostgreSQLDynamicStorage) SearchTicketsWithDragonflyIndex(request ExtendedSearchRequest) ([]*ticketpb.TicketData, error) {
	if !p.dragonflyEnabled || p.dragonflyClient == nil {
		// Fallback to regular PostgreSQL search if Dragonfly not available
		return p.searchTicketsPostgreSQLOnly(request)
	}

	// Handle empty conditions - get all tickets with projection
	if len(request.Conditions) == 0 {
		log.Printf("No search conditions provided - retrieving all tickets with field projection")
		return p.getAllTicketsWithProjection(request)
	}

	// PHASE 1: Index-First Search in Dragonfly
	log.Printf("Phase 1: Querying Dragonfly indexes for %d conditions", len(request.Conditions))
	ticketIDLists, err := p.queryDragonflyIndexes(request.Conditions)
	if err != nil {
		return nil, fmt.Errorf("failed to query Dragonfly indexes: %w", err)
	}

	if len(ticketIDLists) == 0 {
		return []*ticketpb.TicketData{}, nil // No conditions to process
	}

	// PHASE 2: Logical Operations on Ticket ID Lists
	log.Printf("Phase 2: Applying %s logic on %d ticket ID lists", request.Logic, len(ticketIDLists))
	finalTicketIDs, err := p.applyLogicalOperations(ticketIDLists, request.Logic)
	if err != nil {
		return nil, fmt.Errorf("failed to apply logical operations: %w", err)
	}

	if len(finalTicketIDs) == 0 {
		return []*ticketpb.TicketData{}, nil // No tickets match the conditions
	}

	log.Printf("Phase 2 Result: %d tickets match the search criteria", len(finalTicketIDs))

	// Apply pagination to ticket IDs (before PostgreSQL query)
	paginatedTicketIDs := p.applyPaginationToTicketIDs(finalTicketIDs, request.Limit, request.Offset)
	if len(paginatedTicketIDs) == 0 {
		return []*ticketpb.TicketData{}, nil
	}

	// PHASE 3: PostgreSQL Data Retrieval
	log.Printf("Phase 3: Retrieving %d tickets from PostgreSQL with field projection", len(paginatedTicketIDs))
	if len(request.ProjectedFields) > 0 {
		log.Printf("Projected fields: %v", request.ProjectedFields)
	} else {
		log.Printf("No field projection - retrieving all fields")
	}
	tickets, err := p.retrieveTicketDataFromPostgreSQL(paginatedTicketIDs, request.ProjectedFields)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve ticket data from PostgreSQL: %w", err)
	}

	log.Printf("Search completed: returning %d tickets", len(tickets))
	return tickets, nil
}

// ===== PHASE 1: INDEX-FIRST SEARCH IN DRAGONFLY =====

// queryDragonflyIndexes queries Dragonfly indexes for each search condition and returns ticket ID lists
func (p *PostgreSQLDynamicStorage) queryDragonflyIndexes(conditions []SearchCondition) ([][]string, error) {
	var ticketIDLists [][]string

	for i, condition := range conditions {
		// Check if field should be indexed (skip excluded fields)
		if p.shouldSkipIndexing(condition.Operand) {
			return nil, fmt.Errorf("field '%s' is not indexed in Dragonfly (excluded: description/timestamp/ID fields)", condition.Operand)
		}

		log.Printf("Querying index for condition %d: %s %s %v", i+1, condition.Operand, condition.Operator, condition.Value)

		ticketIDs, err := p.queryIndexForCondition(condition)
		if err != nil {
			return nil, fmt.Errorf("failed to query index for field %s: %w", condition.Operand, err)
		}

		log.Printf("Condition %d result: %d tickets found", i+1, len(ticketIDs))
		ticketIDLists = append(ticketIDLists, ticketIDs)
	}

	return ticketIDLists, nil
}

// queryIndexForCondition queries the appropriate Dragonfly index based on the search operator
func (p *PostgreSQLDynamicStorage) queryIndexForCondition(condition SearchCondition) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch condition.Operator {
	case "eq":
		// Exact match: Query field_value_list:table:field:value SET directly
		return p.queryExactMatchIndex(ctx, condition.Operand, fmt.Sprintf("%v", condition.Value))

	case "ne":
		// Not equal: Get all values for field except the specified value
		return p.queryNotEqualIndex(ctx, condition.Operand, fmt.Sprintf("%v", condition.Value))

	case "gt":
		// Greater than: Query numeric ZSET with ZRANGEBYSCORE
		if numValue, ok := toFloat64(condition.Value); ok {
			return p.queryNumericRangeIndex(ctx, condition.Operand, numValue, math.Inf(1), false, true)
		}
		return nil, fmt.Errorf("gt operator requires numeric value for field %s", condition.Operand)

	case "gte":
		// Greater than or equal: Query numeric ZSET with ZRANGEBYSCORE
		if numValue, ok := toFloat64(condition.Value); ok {
			return p.queryNumericRangeIndex(ctx, condition.Operand, numValue, math.Inf(1), true, true)
		}
		return nil, fmt.Errorf("gte operator requires numeric value for field %s", condition.Operand)

	case "lt":
		// Less than: Query numeric ZSET with ZRANGEBYSCORE
		if numValue, ok := toFloat64(condition.Value); ok {
			return p.queryNumericRangeIndex(ctx, condition.Operand, math.Inf(-1), numValue, true, false)
		}
		return nil, fmt.Errorf("lt operator requires numeric value for field %s", condition.Operand)

	case "lte":
		// Less than or equal: Query numeric ZSET with ZRANGEBYSCORE
		if numValue, ok := toFloat64(condition.Value); ok {
			return p.queryNumericRangeIndex(ctx, condition.Operand, math.Inf(-1), numValue, true, true)
		}
		return nil, fmt.Errorf("lte operator requires numeric value for field %s", condition.Operand)

	case "begins_with", "prefix":
		// Prefix search: Use ZRANGEBYLEX on string indexes
		return p.queryStringPrefixIndex(ctx, condition.Operand, fmt.Sprintf("%v", condition.Value))

	case "contains":
		// Contains search: Fallback to prefix search (simplified implementation)
		return p.queryStringPrefixIndex(ctx, condition.Operand, fmt.Sprintf("%v", condition.Value))

	case "in":
		// IN operator: Union of exact matches for multiple values
		return p.queryInIndex(ctx, condition.Operand, condition.Value)

	default:
		return nil, fmt.Errorf("unsupported operator: %s", condition.Operator)
	}
}

// queryExactMatchIndex queries the field_value_list SET for exact matches
func (p *PostgreSQLDynamicStorage) queryExactMatchIndex(ctx context.Context, fieldName, value string) ([]string, error) {
	valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, value)

	ticketIDs, err := p.dragonflyClient.SMembers(ctx, valueListKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to query exact match index for %s=%s: %w", fieldName, value, err)
	}

	return ticketIDs, nil
}

// queryNumericRangeIndex queries the numeric ZSET and retrieves ticket IDs for matching values
func (p *PostgreSQLDynamicStorage) queryNumericRangeIndex(ctx context.Context, fieldName string, minValue, maxValue float64, includeMin, includeMax bool) ([]string, error) {
	indexKey := fmt.Sprintf("field_numeric_index:%s:%s", p.tableName, fieldName)

	// Build range query parameters
	minStr := fmt.Sprintf("%.6f", minValue)
	maxStr := fmt.Sprintf("%.6f", maxValue)

	if !includeMin {
		minStr = "(" + minStr // Exclusive minimum
	}
	if !includeMax {
		maxStr = "(" + maxStr // Exclusive maximum
	}

	// Query ZSET to get matching values
	values, err := p.dragonflyClient.ZRangeByScore(ctx, indexKey, &redis.ZRangeBy{
		Min: minStr,
		Max: maxStr,
	}).Result()

	if err != nil {
		return nil, fmt.Errorf("failed to query numeric range index for %s: %w", fieldName, err)
	}

	// For each value, get the ticket IDs from the value list
	var allTicketIDs []string
	for _, value := range values {
		valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, value)
		ticketIDs, err := p.dragonflyClient.SMembers(ctx, valueListKey).Result()
		if err != nil {
			log.Printf("Warning: Failed to get ticket IDs for %s=%s: %v", fieldName, value, err)
			continue
		}
		allTicketIDs = append(allTicketIDs, ticketIDs...)
	}

	// Remove duplicates
	return removeDuplicates(allTicketIDs), nil
}

// queryStringPrefixIndex queries the string ZSET using ZRANGEBYLEX for prefix matching
func (p *PostgreSQLDynamicStorage) queryStringPrefixIndex(ctx context.Context, fieldName, prefix string) ([]string, error) {
	indexKey := fmt.Sprintf("field_string_index:%s:%s", p.tableName, fieldName)

	// Query ZSET by lexicographical range for prefix matching
	values, err := p.dragonflyClient.ZRangeByLex(ctx, indexKey, &redis.ZRangeBy{
		Min: fmt.Sprintf("[%s", prefix),
		Max: fmt.Sprintf("(%s~", prefix), // Use ~ as upper bound for prefix
	}).Result()

	if err != nil {
		return nil, fmt.Errorf("failed to query string prefix index for %s: %w", fieldName, err)
	}

	// For each matching value, get the ticket IDs
	var allTicketIDs []string
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, value)
			ticketIDs, err := p.dragonflyClient.SMembers(ctx, valueListKey).Result()
			if err != nil {
				log.Printf("Warning: Failed to get ticket IDs for %s=%s: %v", fieldName, value, err)
				continue
			}
			allTicketIDs = append(allTicketIDs, ticketIDs...)
		}
	}

	// Remove duplicates
	return removeDuplicates(allTicketIDs), nil
}

// queryNotEqualIndex queries all values for a field except the specified value (NOT EQUAL operator)
func (p *PostgreSQLDynamicStorage) queryNotEqualIndex(ctx context.Context, fieldName, excludeValue string) ([]string, error) {
	// Strategy: Get all unique values for the field, then get ticket IDs for all values except the excluded one

	// First, try to get all values from the string index
	stringIndexKey := fmt.Sprintf("field_string_index:%s:%s", p.tableName, fieldName)
	allStringValues, err := p.dragonflyClient.ZRange(ctx, stringIndexKey, 0, -1).Result()
	if err != nil && err.Error() != "redis: nil" {
		log.Printf("Warning: Failed to get string values for field %s: %v", fieldName, err)
	}

	// Also try to get all values from the numeric index
	numericIndexKey := fmt.Sprintf("field_numeric_index:%s:%s", p.tableName, fieldName)
	allNumericValues, err := p.dragonflyClient.ZRange(ctx, numericIndexKey, 0, -1).Result()
	if err != nil && err.Error() != "redis: nil" {
		log.Printf("Warning: Failed to get numeric values for field %s: %v", fieldName, err)
	}

	// Combine all values and exclude the specified value
	var allValues []string
	allValues = append(allValues, allStringValues...)
	allValues = append(allValues, allNumericValues...)

	// Remove duplicates and exclude the specified value
	uniqueValues := make(map[string]bool)
	var filteredValues []string
	for _, value := range allValues {
		if value != excludeValue && !uniqueValues[value] {
			uniqueValues[value] = true
			filteredValues = append(filteredValues, value)
		}
	}

	// Get ticket IDs for all filtered values
	var allTicketIDs []string
	for _, value := range filteredValues {
		valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, value)
		ticketIDs, err := p.dragonflyClient.SMembers(ctx, valueListKey).Result()
		if err != nil {
			log.Printf("Warning: Failed to get ticket IDs for %s=%s: %v", fieldName, value, err)
			continue
		}
		allTicketIDs = append(allTicketIDs, ticketIDs...)
	}

	log.Printf("NOT EQUAL query for %s != %s: found %d values, %d total tickets",
		fieldName, excludeValue, len(filteredValues), len(allTicketIDs))

	// Remove duplicates
	return removeDuplicates(allTicketIDs), nil
}

// queryInIndex queries for tickets where field value is IN a list of values (IN operator)
func (p *PostgreSQLDynamicStorage) queryInIndex(ctx context.Context, fieldName string, values interface{}) ([]string, error) {
	// Convert values to string slice
	var valueList []string

	switch v := values.(type) {
	case []interface{}:
		for _, val := range v {
			valueList = append(valueList, fmt.Sprintf("%v", val))
		}
	case []string:
		valueList = v
	case []int:
		for _, val := range v {
			valueList = append(valueList, fmt.Sprintf("%d", val))
		}
	case []int64:
		for _, val := range v {
			valueList = append(valueList, fmt.Sprintf("%d", val))
		}
	case []float64:
		for _, val := range v {
			valueList = append(valueList, fmt.Sprintf("%.6f", val))
		}
	default:
		// Try to convert single value to slice
		valueList = []string{fmt.Sprintf("%v", values)}
	}

	if len(valueList) == 0 {
		return []string{}, nil
	}

	// Get ticket IDs for each value and combine them (union operation)
	var allTicketIDs []string
	for _, value := range valueList {
		valueListKey := fmt.Sprintf("field_value_list:%s:%s:%s", p.tableName, fieldName, value)
		ticketIDs, err := p.dragonflyClient.SMembers(ctx, valueListKey).Result()
		if err != nil {
			log.Printf("Warning: Failed to get ticket IDs for %s=%s: %v", fieldName, value, err)
			continue
		}
		allTicketIDs = append(allTicketIDs, ticketIDs...)
	}

	log.Printf("IN query for %s IN %v: found %d total tickets", fieldName, valueList, len(allTicketIDs))

	// Remove duplicates
	return removeDuplicates(allTicketIDs), nil
}

// ===== PHASE 2: LOGICAL OPERATIONS ON TICKET ID LISTS =====

// applyLogicalOperations applies AND/OR logic operations on ticket ID lists
func (p *PostgreSQLDynamicStorage) applyLogicalOperations(ticketIDLists [][]string, logic string) ([]string, error) {
	if len(ticketIDLists) == 0 {
		return []string{}, nil
	}

	if len(ticketIDLists) == 1 {
		return ticketIDLists[0], nil
	}

	// Default to AND if logic is not specified
	if logic == "" {
		logic = "AND"
	}

	var result []string
	switch strings.ToUpper(logic) {
	case "OR":
		// Union: tickets that appear in ANY condition result
		result = p.performUnionOperation(ticketIDLists)
		log.Printf("OR operation: %d lists combined into %d unique tickets", len(ticketIDLists), len(result))

	case "AND":
		// Intersection: tickets that appear in ALL condition results
		result = p.performIntersectionOperation(ticketIDLists)
		log.Printf("AND operation: %d lists intersected into %d common tickets", len(ticketIDLists), len(result))

	default:
		return nil, fmt.Errorf("unsupported logic operator: %s (supported: AND, OR)", logic)
	}

	return result, nil
}

// performUnionOperation finds the union of all ticket ID lists (OR logic)
func (p *PostgreSQLDynamicStorage) performUnionOperation(ticketIDLists [][]string) []string {
	unionMap := make(map[string]bool)

	for listIndex, ticketIDs := range ticketIDLists {
		log.Printf("Union: Processing list %d with %d tickets", listIndex+1, len(ticketIDs))
		for _, ticketID := range ticketIDs {
			unionMap[ticketID] = true
		}
	}

	result := make([]string, 0, len(unionMap))
	for ticketID := range unionMap {
		result = append(result, ticketID)
	}

	return result
}

// performIntersectionOperation finds the intersection of all ticket ID lists (AND logic)
func (p *PostgreSQLDynamicStorage) performIntersectionOperation(ticketIDLists [][]string) []string {
	if len(ticketIDLists) == 0 {
		return []string{}
	}

	// Count occurrences of each ticket ID across all lists
	ticketCounts := make(map[string]int)
	requiredCount := len(ticketIDLists)

	for listIndex, ticketIDs := range ticketIDLists {
		log.Printf("Intersection: Processing list %d with %d tickets", listIndex+1, len(ticketIDs))

		// Use a set to avoid counting duplicates within the same list
		uniqueInList := make(map[string]bool)
		for _, ticketID := range ticketIDs {
			if !uniqueInList[ticketID] {
				uniqueInList[ticketID] = true
				ticketCounts[ticketID]++
			}
		}
	}

	// Only include ticket IDs that appear in all lists
	var result []string
	for ticketID, count := range ticketCounts {
		if count == requiredCount {
			result = append(result, ticketID)
		}
	}

	return result
}

// ===== PHASE 3: POSTGRESQL DATA RETRIEVAL =====

// applyPaginationToTicketIDs applies limit and offset to the ticket ID list (before PostgreSQL query)
func (p *PostgreSQLDynamicStorage) applyPaginationToTicketIDs(ticketIDs []string, limit, offset int) []string {
	totalTickets := len(ticketIDs)

	// Apply offset
	if offset > 0 {
		if offset >= totalTickets {
			log.Printf("Pagination: Offset %d >= total tickets %d, returning empty result", offset, totalTickets)
			return []string{}
		}
		ticketIDs = ticketIDs[offset:]
		log.Printf("Pagination: Applied offset %d, %d tickets remaining", offset, len(ticketIDs))
	}

	// Apply limit
	if limit > 0 && limit < len(ticketIDs) {
		ticketIDs = ticketIDs[:limit]
		log.Printf("Pagination: Applied limit %d, final result: %d tickets", limit, len(ticketIDs))
	}

	return ticketIDs
}

// retrieveTicketDataFromPostgreSQL fetches ticket data using ticket IDs with optional field projection
func (p *PostgreSQLDynamicStorage) retrieveTicketDataFromPostgreSQL(ticketIDs []string, projectedFields []string) ([]*ticketpb.TicketData, error) {
	if len(ticketIDs) == 0 {
		return []*ticketpb.TicketData{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// PHASE 1: Check cache first
	cacheStart := time.Now()
	cachedTickets, uncachedIDs, err := p.getCachedTickets(ctx, ticketIDs)
	if err != nil {
		log.Printf("Warning: Cache lookup failed: %v", err)
		uncachedIDs = ticketIDs // Fallback to fetch all from PostgreSQL
	}
	cacheDuration := time.Since(cacheStart)

	var allTickets []*ticketpb.TicketData

	// Add cached tickets to result
	for _, ticket := range cachedTickets {
		allTickets = append(allTickets, ticket)
	}

	log.Printf("Cache lookup: %d cached, %d uncached in %v", len(cachedTickets), len(uncachedIDs), cacheDuration)

	// PHASE 2: Fetch uncached tickets from PostgreSQL (if any)
	var pgTickets []*ticketpb.TicketData
	if len(uncachedIDs) > 0 {
		// Build SELECT clause with field projection
		selectClause := p.buildSelectClause(projectedFields)

		// Build IN clause for uncached ticket IDs only
		placeholders := make([]string, len(uncachedIDs))
		values := make([]interface{}, len(uncachedIDs))
		for i, ticketID := range uncachedIDs {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			values[i] = ticketID
		}

		// SELECT with field projection and IN clause - no WHERE conditions, no filtering
		selectSQL := fmt.Sprintf(
			"SELECT %s FROM %s WHERE ticket_id IN (%s) ORDER BY created_at DESC",
			selectClause,
			p.tableName,
			strings.Join(placeholders, ", "),
		)

		log.Printf("PostgreSQL Query: Retrieving %d uncached tickets by ID", len(uncachedIDs))

		// Execute query and measure performance
		queryStart := time.Now()
		rows, err := p.db.QueryContext(ctx, selectSQL, values...)
		queryDuration := time.Since(queryStart)

		if err != nil {
			log.Printf("Failed to retrieve uncached tickets by IDs: %v", err)
			p.logger.LogQueryExecution("RETRIEVE_TICKETS_BY_IDS_FAILED", queryDuration, 0)
			return allTickets, fmt.Errorf("failed to retrieve uncached tickets by IDs: %w", err)
		}
		defer rows.Close()

		p.logger.LogQueryExecution("RETRIEVE_TICKETS_BY_IDS", queryDuration, int64(len(uncachedIDs)))

		// Process PostgreSQL results
		pgTickets, err = p.processTicketRows(rows)
		if err != nil {
			return allTickets, fmt.Errorf("failed to process ticket rows: %w", err)
		}

		log.Printf("PostgreSQL Result: Successfully retrieved %d uncached tickets", len(pgTickets))

		// PHASE 3: Cache the newly fetched tickets (synchronous to avoid race conditions)
		if len(pgTickets) > 0 {
			p.cacheTickets(context.Background(), pgTickets)
		}

		// Add PostgreSQL tickets to result
		allTickets = append(allTickets, pgTickets...)
	}

	log.Printf("Total Result: %d tickets (%d cached + %d from PostgreSQL)",
		len(allTickets), len(cachedTickets), len(pgTickets))
	return allTickets, nil
}

// processTicketRows processes the SQL rows and converts them to TicketData objects
func (p *PostgreSQLDynamicStorage) processTicketRows(rows *sql.Rows) ([]*ticketpb.TicketData, error) {
	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get column names: %w", err)
	}

	// PHASE 1: Scan all rows into memory (measure scanning latency)
	scanStart := time.Now()
	var rowMaps []map[string]interface{}

	for rows.Next() {
		// Create a slice to hold the row values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan the row
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Convert row to map
		rowMap := make(map[string]interface{})
		for i, col := range columns {
			rowMap[col] = values[i]
		}

		rowMaps = append(rowMaps, rowMap)
	}

	scanDuration := time.Since(scanStart)
	log.Printf("PostgreSQL Row Scanning: %d rows scanned in %v", len(rowMaps), scanDuration)

	// PHASE 2: Convert all scanned data to protobuf objects
	conversionStart := time.Now()
	var tickets []*ticketpb.TicketData

	for _, rowMap := range rowMaps {
		// Convert to TicketData (use default category 1 if not specified)
		categoryID := int64(1)
		if catID, exists := rowMap["categoryid"]; exists {
			if catIDInt, ok := catID.(int64); ok {
				categoryID = catIDInt
			}
		}
		ticketData := p.dynamicRowToProtobuf(rowMap, categoryID)
		tickets = append(tickets, ticketData)
	}

	conversionDuration := time.Since(conversionStart)
	log.Printf("Protobuf Conversion: %d tickets converted in %v", len(tickets), conversionDuration)
	log.Printf("Total Processing: scanning=%v, conversion=%v, total=%v",
		scanDuration, conversionDuration, scanDuration+conversionDuration)

	return tickets, nil
}

// buildSelectClause builds the SELECT clause with field projection support
func (p *PostgreSQLDynamicStorage) buildSelectClause(projectedFields []string) string {
	// If no projected fields specified, return all fields
	if len(projectedFields) == 0 {
		return "*"
	}

	// Always include essential fields for protobuf conversion
	essentialFields := []string{
		"ticket_id",
		"created_at",
		"updated_at",
		"categoryid", // Needed for category mapping
	}

	// Create a map to track included fields and avoid duplicates
	fieldMap := make(map[string]bool)
	var selectFields []string

	// Add essential fields first
	for _, field := range essentialFields {
		if !fieldMap[field] {
			fieldMap[field] = true
			selectFields = append(selectFields, field)
		}
	}

	// Add projected fields, mapping them to actual column names
	for _, field := range projectedFields {
		// Map field name to actual column name (handle both static and dynamic fields)
		// Use default category 1 for field mapping
		columnName := p.mapFieldToColumn(field, 1)

		if !fieldMap[columnName] {
			fieldMap[columnName] = true
			selectFields = append(selectFields, columnName)
		}
	}

	selectClause := strings.Join(selectFields, ", ")
	log.Printf("PostgreSQL SELECT clause: %s", selectClause)

	return selectClause
}

// ===== LEGACY METHODS (kept for backward compatibility) =====

// searchTicketsPostgreSQLOnly fallback method for when Dragonfly is not available
func (p *PostgreSQLDynamicStorage) searchTicketsPostgreSQLOnly(request ExtendedSearchRequest) ([]*ticketpb.TicketData, error) {
	// Build WHERE clause from conditions
	var whereClauses []string
	var values []interface{}
	paramIndex := 1

	for _, condition := range request.Conditions {
		clause, conditionValues := p.buildPostgreSQLCondition(condition, &paramIndex)
		if clause != "" {
			whereClauses = append(whereClauses, clause)
			values = append(values, conditionValues...)
		}
	}

	// Handle empty conditions - get all tickets with projection
	if len(request.Conditions) == 0 {
		log.Printf("No search conditions provided - retrieving all tickets with field projection (PostgreSQL-only mode)")
		return p.getAllTicketsWithProjection(request)
	}

	if len(whereClauses) == 0 {
		return []*ticketpb.TicketData{}, nil
	}

	// Join conditions with AND/OR
	logicOperator := "AND"
	if request.Logic == "OR" {
		logicOperator = "OR"
	}
	whereClause := strings.Join(whereClauses, fmt.Sprintf(" %s ", logicOperator))

	// Build complete query with field projection
	selectClause := p.buildSelectClause(request.ProjectedFields)
	selectSQL := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY created_at DESC", selectClause, p.tableName, whereClause)

	// Add LIMIT and OFFSET
	if request.Limit > 0 {
		selectSQL += fmt.Sprintf(" LIMIT $%d", paramIndex)
		values = append(values, request.Limit)
		paramIndex++
	}
	if request.Offset > 0 {
		selectSQL += fmt.Sprintf(" OFFSET $%d", paramIndex)
		values = append(values, request.Offset)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Execute query
	queryStart := time.Now()
	rows, err := p.db.QueryContext(ctx, selectSQL, values...)
	queryDuration := time.Since(queryStart)

	if err != nil {
		log.Printf("Failed to execute PostgreSQL search: %v", err)
		p.logger.LogQueryExecution("SEARCH_TICKETS_POSTGRESQL_FAILED", queryDuration, 0)
		return nil, fmt.Errorf("failed to execute search: %w", err)
	}
	defer rows.Close()

	p.logger.LogQueryExecution("SEARCH_TICKETS_POSTGRESQL", queryDuration, 1)

	// Process results (similar to getTicketsByIDs)
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get column names: %w", err)
	}

	var tickets []*ticketpb.TicketData
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		rowMap := make(map[string]interface{})
		for i, col := range columns {
			rowMap[col] = values[i]
		}

		// Convert to TicketData (use default category 1 if not specified)
		categoryID := int64(1)
		if catID, exists := rowMap["categoryid"]; exists {
			if catIDInt, ok := catID.(int64); ok {
				categoryID = catIDInt
			}
		}
		ticketData := p.dynamicRowToProtobuf(rowMap, categoryID)

		tickets = append(tickets, ticketData)
	}

	return tickets, nil
}

// buildPostgreSQLCondition builds a PostgreSQL WHERE clause for a search condition
func (p *PostgreSQLDynamicStorage) buildPostgreSQLCondition(condition SearchCondition, paramIndex *int) (string, []interface{}) {
	var values []interface{}

	switch condition.Operator {
	case "eq":
		clause := fmt.Sprintf("%s = $%d", condition.Operand, *paramIndex)
		values = append(values, condition.Value)
		*paramIndex++
		return clause, values

	case "gt":
		clause := fmt.Sprintf("%s > $%d", condition.Operand, *paramIndex)
		values = append(values, condition.Value)
		*paramIndex++
		return clause, values

	case "lt":
		clause := fmt.Sprintf("%s < $%d", condition.Operand, *paramIndex)
		values = append(values, condition.Value)
		*paramIndex++
		return clause, values

	case "gte":
		clause := fmt.Sprintf("%s >= $%d", condition.Operand, *paramIndex)
		values = append(values, condition.Value)
		*paramIndex++
		return clause, values

	case "lte":
		clause := fmt.Sprintf("%s <= $%d", condition.Operand, *paramIndex)
		values = append(values, condition.Value)
		*paramIndex++
		return clause, values

	case "range":
		// Note: Range queries need MinValue/MaxValue from ExtendedSearchCondition
		// This is a simplified fallback for basic SearchCondition
		return "", nil

	case "prefix", "begins_with":
		clause := fmt.Sprintf("%s LIKE $%d", condition.Operand, *paramIndex)
		values = append(values, fmt.Sprintf("%s%%", condition.Value))
		*paramIndex++
		return clause, values

	case "contains":
		clause := fmt.Sprintf("%s LIKE $%d", condition.Operand, *paramIndex)
		values = append(values, fmt.Sprintf("%%%s%%", condition.Value))
		*paramIndex++
		return clause, values

	default:
		return "", nil
	}
}

// toFloat64 converts various numeric types to float64
func toFloat64(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// getAllTicketsWithProjection retrieves all tickets from PostgreSQL with optional field projection
func (p *PostgreSQLDynamicStorage) getAllTicketsWithProjection(request ExtendedSearchRequest) ([]*ticketpb.TicketData, error) {
	// Build SELECT clause with field projection
	selectClause := p.buildSelectClause(request.ProjectedFields)

	// Build query without WHERE clause (get all tickets)
	selectSQL := fmt.Sprintf("SELECT %s FROM %s ORDER BY created_at DESC", selectClause, p.tableName)

	// Add LIMIT and OFFSET for pagination
	var values []interface{}
	paramIndex := 1

	if request.Limit > 0 {
		selectSQL += fmt.Sprintf(" LIMIT $%d", paramIndex)
		values = append(values, request.Limit)
		paramIndex++
	}
	if request.Offset > 0 {
		selectSQL += fmt.Sprintf(" OFFSET $%d", paramIndex)
		values = append(values, request.Offset)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Execute query
	queryStart := time.Now()
	rows, err := p.db.QueryContext(ctx, selectSQL, values...)
	queryDuration := time.Since(queryStart)

	if err != nil {
		log.Printf("Failed to execute get all tickets query: %v", err)
		p.logger.LogQueryExecution("GET_ALL_TICKETS_FAILED", queryDuration, 0)
		return nil, fmt.Errorf("failed to execute get all tickets query: %w", err)
	}
	defer rows.Close()

	// Get column information for dynamic scanning
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get column information: %w", err)
	}

	var tickets []*ticketpb.TicketData
	scanStart := time.Now()

	for rows.Next() {
		// Create a slice to hold the column values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan the row
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Convert scanned values to row map
		rowMap := make(map[string]interface{})
		for i, column := range columns {
			rowMap[column] = values[i]
		}

		// Extract category ID for dynamic field mapping
		var categoryID int64 = 1 // Default category
		if catID, exists := rowMap["categoryid"]; exists {
			if catIDInt, ok := catID.(int64); ok {
				categoryID = catIDInt
			}
		}

		// Convert row to protobuf ticket data
		ticketData := p.dynamicRowToProtobuf(rowMap, categoryID)
		tickets = append(tickets, ticketData)
	}

	scanDuration := time.Since(scanStart)
	totalDuration := time.Since(queryStart)

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	// Log performance metrics
	p.logger.LogQueryExecution("GET_ALL_TICKETS_SUCCESS", queryDuration, int64(len(tickets)))
	p.logger.LogRowScan("GET_ALL_TICKETS_SCAN", scanDuration, len(tickets))

	log.Printf("Retrieved %d tickets from table %s (query: %v, scan: %v, total: %v)",
		len(tickets), p.tableName, queryDuration, scanDuration, totalDuration)

	if len(request.ProjectedFields) > 0 {
		log.Printf("Applied field projection: %v", request.ProjectedFields)
	}

	return tickets, nil
}
