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
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
	"go.mongodb.org/mongo-driver/bson"
)

// PostgreSQLDocumentDBStorage implements ticket storage using PostgreSQL with hybrid approach
// Combines fixed schema for common fields with BSON for dynamic fields
type PostgreSQLDocumentDBStorage struct {
	db        *sql.DB
	tableName string
}

// FieldMapping defines which fields go to fixed schema vs dynamic BSON
type FieldMapping struct {
	// Fixed schema fields (most commonly queried)
	FixedFields map[string]string // protobuf_field -> db_column
	// Dynamic fields (stored in BSON)
	DynamicFields map[string]BSONFieldInfo // protobuf_field -> BSON field info
}

// BSONFieldInfo defines where and how a field is stored in BSON
type BSONFieldInfo struct {
	BSONColumn string // Which BSON column (dynamic_fields, user_fields, etc.)
	FieldPath  string // Path within the BSON document
	DataType   string // string, number, boolean
}

// NewPostgreSQLDocumentDBStorage creates a new PostgreSQL DocumentDB storage instance
func NewPostgreSQLDocumentDBStorage(ctx context.Context, tableName, connectionString string) (*PostgreSQLDocumentDBStorage, error) {
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

	log.Printf("PostgreSQL DocumentDB connection established successfully")

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

// getFieldMapping returns the mapping of fields to fixed schema vs dynamic BSON
func (p *PostgreSQLDocumentDBStorage) getFieldMapping() *FieldMapping {
	return &FieldMapping{
		// Fixed schema fields (commonly queried/filtered)
		FixedFields: map[string]string{
			"requesterid":  "requesterid",
			"technicianid": "technicianid",
			"createdbyid":  "createdbyid",
			"statusid":     "statusid",
			"priorityid":   "priorityid",
			"urgencyid":    "urgencyid",
			"createdtime":  "createdtime",
			"updatedtime":  "updatedtime",
			"dueby":        "dueby",
			"companyid":    "companyid",
			"groupid":      "groupid",
			"departmentid": "departmentid",
			"categoryid":   "categoryid",
			"subject":      "subject",
			"description":  "description",
			"removed":      "removed",
			"spam":         "spam",
		},
		// All other fields go to BSON (less commonly queried)
		DynamicFields: map[string]BSONFieldInfo{
			// User fields -> user_fields BSON column
			"updatedbyid": {BSONColumn: "user_fields", FieldPath: "updatedbyid", DataType: "number"},
			"removedbyid": {BSONColumn: "user_fields", FieldPath: "removedbyid", DataType: "number"},
			"closedby":    {BSONColumn: "user_fields", FieldPath: "closedby", DataType: "number"},
			"resolvedby":  {BSONColumn: "user_fields", FieldPath: "resolvedby", DataType: "number"},

			// Timing fields -> timing_fields BSON column
			"removedtime":              {BSONColumn: "timing_fields", FieldPath: "removedtime", DataType: "number"},
			"firstresponsetime":        {BSONColumn: "timing_fields", FieldPath: "firstresponsetime", DataType: "number"},
			"lastclosedtime":           {BSONColumn: "timing_fields", FieldPath: "lastclosedtime", DataType: "number"},
			"lastopenedtime":           {BSONColumn: "timing_fields", FieldPath: "lastopenedtime", DataType: "number"},
			"lastresolvedtime":         {BSONColumn: "timing_fields", FieldPath: "lastresolvedtime", DataType: "number"},
			"lastviolationtime":        {BSONColumn: "timing_fields", FieldPath: "lastviolationtime", DataType: "number"},
			"olddueby":                 {BSONColumn: "timing_fields", FieldPath: "olddueby", DataType: "number"},
			"oldresponsedue":           {BSONColumn: "timing_fields", FieldPath: "oldresponsedue", DataType: "number"},
			"resolutionescalationtime": {BSONColumn: "timing_fields", FieldPath: "resolutionescalationtime", DataType: "number"},
			"responsedue":              {BSONColumn: "timing_fields", FieldPath: "responsedue", DataType: "number"},
			"responseescalationtime":   {BSONColumn: "timing_fields", FieldPath: "responseescalationtime", DataType: "number"},
			"statuschangedtime":        {BSONColumn: "timing_fields", FieldPath: "statuschangedtime", DataType: "number"},
			"groupchangedtime":         {BSONColumn: "timing_fields", FieldPath: "groupchangedtime", DataType: "number"},
			"lastolaviolationtime":     {BSONColumn: "timing_fields", FieldPath: "lastolaviolationtime", DataType: "number"},
			"oladueby":                 {BSONColumn: "timing_fields", FieldPath: "oladueby", DataType: "number"},
			"oldoladueby":              {BSONColumn: "timing_fields", FieldPath: "oldoladueby", DataType: "number"},
			"askfeedbackdate":          {BSONColumn: "timing_fields", FieldPath: "askfeedbackdate", DataType: "number"},
			"firstfeedbackdate":        {BSONColumn: "timing_fields", FieldPath: "firstfeedbackdate", DataType: "number"},
			"olaescalationtime":        {BSONColumn: "timing_fields", FieldPath: "olaescalationtime", DataType: "number"},
			"lastucviolationtime":      {BSONColumn: "timing_fields", FieldPath: "lastucviolationtime", DataType: "number"},
			"olducdueby":               {BSONColumn: "timing_fields", FieldPath: "olducdueby", DataType: "number"},
			"ucdueby":                  {BSONColumn: "timing_fields", FieldPath: "ucdueby", DataType: "number"},
			"ucescalationtime":         {BSONColumn: "timing_fields", FieldPath: "ucescalationtime", DataType: "number"},
			"lastapproveddate":         {BSONColumn: "timing_fields", FieldPath: "lastapproveddate", DataType: "number"},
			"totalonholdduration":      {BSONColumn: "timing_fields", FieldPath: "totalonholdduration", DataType: "number"},
			"totalresolutiontime":      {BSONColumn: "timing_fields", FieldPath: "totalresolutiontime", DataType: "number"},
			"totalslapausetime":        {BSONColumn: "timing_fields", FieldPath: "totalslapausetime", DataType: "number"},
			"totalworkingtime":         {BSONColumn: "timing_fields", FieldPath: "totalworkingtime", DataType: "number"},
			"totaluconholdduration":    {BSONColumn: "timing_fields", FieldPath: "totaluconholdduration", DataType: "number"},
			"totalucpausetime":         {BSONColumn: "timing_fields", FieldPath: "totalucpausetime", DataType: "number"},
			"totalucworkingtime":       {BSONColumn: "timing_fields", FieldPath: "totalucworkingtime", DataType: "number"},
			"totalucresolutiontime":    {BSONColumn: "timing_fields", FieldPath: "totalucresolutiontime", DataType: "number"},

			// Text fields -> dynamic_fields BSON column
			"name":                 {BSONColumn: "dynamic_fields", FieldPath: "name", DataType: "string"},
			"oobtype":              {BSONColumn: "dynamic_fields", FieldPath: "oobtype", DataType: "string"},
			"originaldescription":  {BSONColumn: "dynamic_fields", FieldPath: "originaldescription", DataType: "string"},
			"callfrom":             {BSONColumn: "dynamic_fields", FieldPath: "callfrom", DataType: "string"},
			"emailreadconfigemail": {BSONColumn: "dynamic_fields", FieldPath: "emailreadconfigemail", DataType: "string"},

			// Boolean flags -> dynamic_fields BSON column
			"duetimemanuallyupdated": {BSONColumn: "dynamic_fields", FieldPath: "duetimemanuallyupdated", DataType: "boolean"},
			"reopened":               {BSONColumn: "dynamic_fields", FieldPath: "reopened", DataType: "boolean"},
			"responsedueviolated":    {BSONColumn: "dynamic_fields", FieldPath: "responsedueviolated", DataType: "boolean"},
			"slaviolated":            {BSONColumn: "dynamic_fields", FieldPath: "slaviolated", DataType: "boolean"},
			"purchaserequest":        {BSONColumn: "dynamic_fields", FieldPath: "purchaserequest", DataType: "boolean"},
			"viprequest":             {BSONColumn: "dynamic_fields", FieldPath: "viprequest", DataType: "boolean"},
			"olaviolated":            {BSONColumn: "dynamic_fields", FieldPath: "olaviolated", DataType: "boolean"},
			"ucviolated":             {BSONColumn: "dynamic_fields", FieldPath: "ucviolated", DataType: "boolean"},
			"migrated":               {BSONColumn: "dynamic_fields", FieldPath: "migrated", DataType: "boolean"},

			// ID/Reference fields -> dynamic_fields BSON column
			"impactid":            {BSONColumn: "dynamic_fields", FieldPath: "impactid", DataType: "number"},
			"locationid":          {BSONColumn: "dynamic_fields", FieldPath: "locationid", DataType: "number"},
			"violatedslaid":       {BSONColumn: "dynamic_fields", FieldPath: "violatedslaid", DataType: "number"},
			"servicecatalogid":    {BSONColumn: "dynamic_fields", FieldPath: "servicecatalogid", DataType: "number"},
			"sourceid":            {BSONColumn: "dynamic_fields", FieldPath: "sourceid", DataType: "number"},
			"requesttype":         {BSONColumn: "dynamic_fields", FieldPath: "requesttype", DataType: "number"},
			"suggestedcategoryid": {BSONColumn: "dynamic_fields", FieldPath: "suggestedcategoryid", DataType: "number"},
			"suggestedgroupid":    {BSONColumn: "dynamic_fields", FieldPath: "suggestedgroupid", DataType: "number"},
			"vendorid":            {BSONColumn: "dynamic_fields", FieldPath: "vendorid", DataType: "number"},
			"violateducid":        {BSONColumn: "dynamic_fields", FieldPath: "violateducid", DataType: "number"},
			"transitionmodelid":   {BSONColumn: "dynamic_fields", FieldPath: "transitionmodelid", DataType: "number"},
			"messengerconfigid":   {BSONColumn: "dynamic_fields", FieldPath: "messengerconfigid", DataType: "number"},
			"templateid":          {BSONColumn: "dynamic_fields", FieldPath: "templateid", DataType: "number"},
			"emailreadconfigid":   {BSONColumn: "dynamic_fields", FieldPath: "emailreadconfigid", DataType: "number"},

			// Workflow fields -> workflow_fields BSON column
			"approvalstatus":     {BSONColumn: "workflow_fields", FieldPath: "approvalstatus", DataType: "number"},
			"approvaltype":       {BSONColumn: "workflow_fields", FieldPath: "approvaltype", DataType: "number"},
			"resolutionduelevel": {BSONColumn: "workflow_fields", FieldPath: "resolutionduelevel", DataType: "number"},
			"responseduelevel":   {BSONColumn: "workflow_fields", FieldPath: "responseduelevel", DataType: "number"},
			"supportlevel":       {BSONColumn: "workflow_fields", FieldPath: "supportlevel", DataType: "number"},
			"oladuelevel":        {BSONColumn: "workflow_fields", FieldPath: "oladuelevel", DataType: "number"},
			"ucduelevel":         {BSONColumn: "workflow_fields", FieldPath: "ucduelevel", DataType: "number"},
		},
	}
}

// generateTicketID generates a unique ticket ID if not provided
func (p *PostgreSQLDocumentDBStorage) generateTicketID() string {
	// Generate a simple ticket ID with timestamp
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
		// Create the table
		if err := p.createTableIfNotExists(ctx); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
		log.Printf("Created new PostgreSQL DocumentDB hybrid table: %s", p.tableName)
	} else {
		log.Printf("Found existing PostgreSQL DocumentDB hybrid table: %s", p.tableName)
	}

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
	}

	var schemaContent string

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			content, readErr := ioutil.ReadFile(path)
			if readErr == nil {
				schemaContent = string(content)
				log.Printf("Loaded PostgreSQL DocumentDB hybrid schema from: %s", path)
				break
			}
		}
	}

	if schemaContent == "" {
		return "", fmt.Errorf("could not find schema_hybrid.sql file in any of the expected locations: %v", possiblePaths)
	}

	return schemaContent, nil
}

// createTableIfNotExists creates the PostgreSQL table using the hybrid schema from file
func (p *PostgreSQLDocumentDBStorage) createTableIfNotExists(ctx context.Context) error {
	// Load schema from file
	schemaContent, err := p.loadSchemaFromFile()
	if err != nil {
		return fmt.Errorf("failed to load schema file: %w", err)
	}

	// Replace table name in schema
	adaptedSchema := strings.ReplaceAll(schemaContent, "ticket_hybrid", p.tableName)

	// Execute the schema
	_, err = p.db.ExecContext(ctx, adaptedSchema)
	if err != nil {
		return fmt.Errorf("failed to create hybrid table %s: %w", p.tableName, err)
	}

	log.Printf("Created PostgreSQL DocumentDB hybrid table %s", p.tableName)
	return nil
}

// protobufToHybridRow converts a TicketData protobuf to hybrid row data (fixed + dynamic BSON fields)
func (p *PostgreSQLDocumentDBStorage) protobufToHybridRow(ticketData *ticketpb.TicketData, isUpdate bool) (map[string]interface{}, map[string]bson.M, error) {
	fixedRow := make(map[string]interface{})
	// Initialize BSON documents for each column
	bsonFields := map[string]bson.M{
		"dynamic_fields":  bson.M{},
		"user_fields":     bson.M{},
		"timing_fields":   bson.M{},
		"workflow_fields": bson.M{},
		"custom_fields":   bson.M{},
	}

	mapping := p.getFieldMapping()

	// Core fields - these are managed by the application
	fixedRow["ticket_id"] = ticketData.Id

	// Handle timestamps
	if !isUpdate {
		// For new tickets, set created_at to current time
		fixedRow["created_at"] = time.Now()
		fixedRow["createdtime"] = time.Now().UnixNano() / 1000000 // milliseconds
	}
	// Always update updated_at for both create and update
	fixedRow["updated_at"] = time.Now()
	fixedRow["updatedtime"] = time.Now().UnixNano() / 1000000 // milliseconds

	// Process all fields from the protobuf Fields map
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

		// Check if this field goes to fixed schema
		if dbColumn, isFixed := mapping.FixedFields[fieldName]; isFixed {
			// Handle type conversions for fixed schema fields
			switch fieldName {
			case "requesterid", "technicianid", "createdbyid", "statusid", "priorityid",
				"urgencyid", "createdtime", "updatedtime", "dueby", "companyid",
				"groupid", "departmentid", "categoryid":
				if intVal, ok := value.(int64); ok {
					fixedRow[dbColumn] = intVal
				} else if floatVal, ok := value.(float64); ok {
					fixedRow[dbColumn] = int64(floatVal)
				} else if strVal, ok := value.(string); ok {
					if intVal, err := strconv.ParseInt(strVal, 10, 64); err == nil {
						fixedRow[dbColumn] = intVal
					}
				}
			case "subject", "description":
				if strVal, ok := value.(string); ok {
					fixedRow[dbColumn] = strVal
				}
			case "removed", "spam":
				if boolVal, ok := value.(bool); ok {
					fixedRow[dbColumn] = boolVal
				} else if strVal, ok := value.(string); ok {
					fixedRow[dbColumn] = strings.ToLower(strVal) == "true"
				}
			}
		} else if bsonInfo, isDynamic := mapping.DynamicFields[fieldName]; isDynamic {
			// Store in appropriate BSON column based on mapping
			if value != nil {
				switch bsonInfo.DataType {
				case "string":
					if strVal, ok := value.(string); ok && strVal != "" {
						bsonFields[bsonInfo.BSONColumn][bsonInfo.FieldPath] = strVal
					}
				case "number":
					if intVal, ok := value.(int64); ok {
						bsonFields[bsonInfo.BSONColumn][bsonInfo.FieldPath] = intVal
					} else if floatVal, ok := value.(float64); ok {
						bsonFields[bsonInfo.BSONColumn][bsonInfo.FieldPath] = int64(floatVal)
					} else if strVal, ok := value.(string); ok {
						if intVal, err := strconv.ParseInt(strVal, 10, 64); err == nil {
							bsonFields[bsonInfo.BSONColumn][bsonInfo.FieldPath] = intVal
						}
					}
				case "boolean":
					if boolVal, ok := value.(bool); ok {
						bsonFields[bsonInfo.BSONColumn][bsonInfo.FieldPath] = boolVal
					} else if strVal, ok := value.(string); ok {
						bsonFields[bsonInfo.BSONColumn][bsonInfo.FieldPath] = strings.ToLower(strVal) == "true"
					}
				}
			}
		} else {
			// Unknown field - store in custom_fields BSON column
			log.Printf("WARNING: Unknown field %s, storing in custom_fields BSON", fieldName)
			bsonFields["custom_fields"][fieldName] = value
		}
	}

	return fixedRow, bsonFields, nil
}

// hybridRowToProtobuf converts hybrid row data back to a TicketData protobuf
func (p *PostgreSQLDocumentDBStorage) hybridRowToProtobuf(fixedRow map[string]interface{}, bsonFields map[string][]byte) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	mapping := p.getFieldMapping()

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

	// Process fixed schema fields
	for protoField, dbColumn := range mapping.FixedFields {
		if value, exists := fixedRow[dbColumn]; exists && value != nil {
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
			default:
				// Convert to string as fallback
				fieldValue.Value = &ticketpb.FieldValue_StringValue{StringValue: fmt.Sprintf("%v", v)}
			}

			ticketData.Fields[protoField] = fieldValue
		}
	}

	// Process dynamic fields from BSON columns
	for protoField, bsonInfo := range mapping.DynamicFields {
		if bsonData, exists := bsonFields[bsonInfo.BSONColumn]; exists && len(bsonData) > 0 {
			// Parse BSON data
			var bsonDoc bson.M
			if err := bson.Unmarshal(bsonData, &bsonDoc); err == nil {
				// Extract field value from BSON document
				if value, exists := bsonDoc[bsonInfo.FieldPath]; exists && value != nil {
					fieldValue := &ticketpb.FieldValue{}

					switch bsonInfo.DataType {
					case "string":
						if strVal, ok := value.(string); ok {
							fieldValue.Value = &ticketpb.FieldValue_StringValue{StringValue: strVal}
						}
					case "number":
						switch v := value.(type) {
						case int32:
							fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: int64(v)}
						case int64:
							fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: v}
						case float64:
							fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: int64(v)}
						}
					case "boolean":
						if boolVal, ok := value.(bool); ok {
							fieldValue.Value = &ticketpb.FieldValue_BoolValue{BoolValue: boolVal}
						}
					default:
						// Convert to string as fallback
						fieldValue.Value = &ticketpb.FieldValue_StringValue{StringValue: fmt.Sprintf("%v", value)}
					}

					if fieldValue.Value != nil {
						ticketData.Fields[protoField] = fieldValue
					}
				}
			}
		}
	}

	// Process any custom fields from custom_fields BSON column
	if customBsonData, exists := bsonFields["custom_fields"]; exists && len(customBsonData) > 0 {
		var customDoc bson.M
		if err := bson.Unmarshal(customBsonData, &customDoc); err == nil {
			for field, value := range customDoc {
				if value != nil {
					fieldValue := &ticketpb.FieldValue{}
					switch v := value.(type) {
					case string:
						fieldValue.Value = &ticketpb.FieldValue_StringValue{StringValue: v}
					case int32:
						fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: int64(v)}
					case int64:
						fieldValue.Value = &ticketpb.FieldValue_IntValue{IntValue: v}
					case float64:
						fieldValue.Value = &ticketpb.FieldValue_DoubleValue{DoubleValue: v}
					case bool:
						fieldValue.Value = &ticketpb.FieldValue_BoolValue{BoolValue: v}
					default:
						fieldValue.Value = &ticketpb.FieldValue_StringValue{StringValue: fmt.Sprintf("%v", v)}
					}
					ticketData.Fields[field] = fieldValue
				}
			}
		}
	}

	return ticketData
}

// Helper function to check if slice contains string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// CreateTicket stores a new ticket in the PostgreSQL hybrid table
func (p *PostgreSQLDocumentDBStorage) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Generate ticket ID if not provided
	if ticketData.Id == "" {
		ticketData.Id = p.generateTicketID()
	}

	// Convert protobuf to hybrid row (isUpdate = false for create)
	fixedRow, bsonFields, err := p.protobufToHybridRow(ticketData, false)
	if err != nil {
		return fmt.Errorf("failed to convert protobuf to hybrid row: %w", err), nil
	}

	// Convert BSON fields to binary format for PostgreSQL BSON storage
	for columnName, bsonDoc := range bsonFields {
		if len(bsonDoc) > 0 {
			bsonBytes, err := bson.Marshal(bsonDoc)
			if err != nil {
				return fmt.Errorf("failed to marshal BSON for %s: %w", columnName, err), nil
			}
			fixedRow[columnName] = bsonBytes
		} else {
			// Set empty BSON document
			emptyBson, _ := bson.Marshal(bson.M{})
			fixedRow[columnName] = emptyBson
		}
	}
	fixedRow["schema_version"] = 1

	// Build INSERT query dynamically
	columns := make([]string, 0, len(fixedRow))
	placeholders := make([]string, 0, len(fixedRow))
	values := make([]interface{}, 0, len(fixedRow))

	i := 1
	for column, value := range fixedRow {
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
		return fmt.Errorf("failed to create ticket in hybrid table %s: %w", p.tableName, err), nil
	}

	log.Printf("Created ticket %s in hybrid table %s with ID %d", ticketData.Id, p.tableName, generatedID)

	result := map[string]interface{}{
		"id":        generatedID,
		"ticket_id": ticketData.Id,
	}

	return nil, result
}

// GetTicket retrieves a ticket by ID from the PostgreSQL hybrid table
func (p *PostgreSQLDocumentDBStorage) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query for the ticket
	query := fmt.Sprintf("SELECT * FROM %s WHERE ticket_id = $1", p.tableName)

	rows, err := p.db.QueryContext(ctx, query, id)
	if err != nil {
		log.Printf("ERROR: Failed to query ticket %s from hybrid table %s: %v", id, p.tableName, err)
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

	// Convert to map and extract BSON fields
	rowMap := make(map[string]interface{})
	bsonFields := make(map[string][]byte)
	bsonColumns := []string{"dynamic_fields", "user_fields", "timing_fields", "workflow_fields", "custom_fields"}

	for i, column := range columns {
		if contains(bsonColumns, column) {
			if values[i] != nil {
				if bsonBytes, ok := values[i].([]byte); ok {
					bsonFields[column] = bsonBytes
				}
			}
		} else {
			rowMap[column] = values[i]
		}
	}

	// Convert to protobuf
	ticketData := p.hybridRowToProtobuf(rowMap, bsonFields)

	log.Printf("Retrieved ticket %s from hybrid table %s", id, p.tableName)
	return ticketData, true
}

// UpdateTicket updates an existing ticket in the PostgreSQL hybrid table
func (p *PostgreSQLDocumentDBStorage) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Convert protobuf to hybrid row (isUpdate = true for update)
	fixedRow, bsonFields, err := p.protobufToHybridRow(ticketData, true)
	if err != nil {
		log.Printf("ERROR: Failed to convert protobuf to hybrid row: %v", err)
		return false
	}

	// Convert BSON fields to binary format for PostgreSQL BSON storage
	for columnName, bsonDoc := range bsonFields {
		if len(bsonDoc) > 0 {
			bsonBytes, err := bson.Marshal(bsonDoc)
			if err != nil {
				log.Printf("ERROR: Failed to marshal BSON for %s: %v", columnName, err)
				return false
			}
			fixedRow[columnName] = bsonBytes
		} else {
			// Set empty BSON document
			emptyBson, _ := bson.Marshal(bson.M{})
			fixedRow[columnName] = emptyBson
		}
	}

	// Build UPDATE query dynamically
	setParts := make([]string, 0, len(fixedRow))
	values := make([]interface{}, 0, len(fixedRow)+1)

	i := 1
	for column, value := range fixedRow {
		if column == "ticket_id" || column == "created_at" || column == "createdtime" {
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
		p.tableName,
		strings.Join(setParts, ", "),
		i,
	)

	result, err := p.db.ExecContext(ctx, updateSQL, values...)
	if err != nil {
		log.Printf("ERROR: Failed to update ticket in hybrid table %s: %v", p.tableName, err)
		return false
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("ERROR: Failed to get rows affected: %v", err)
		return false
	}

	if rowsAffected == 0 {
		log.Printf("No ticket found with ID %s in hybrid table %s", ticketData.Id, p.tableName)
		return false
	}

	log.Printf("Updated ticket %s in hybrid table %s", ticketData.Id, p.tableName)
	return true
}

// DeleteTicket removes a ticket from the PostgreSQL hybrid table
func (p *PostgreSQLDocumentDBStorage) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
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
		log.Printf("ERROR: Failed to delete ticket %s from hybrid table %s: %v", id, p.tableName, err)
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

	log.Printf("Deleted ticket %s from hybrid table %s", id, p.tableName)
	return ticketData, true
}

// ListTickets retrieves all tickets from the PostgreSQL hybrid table
func (p *PostgreSQLDocumentDBStorage) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Query all tickets
	query := fmt.Sprintf("SELECT * FROM %s ORDER BY created_at DESC", p.tableName)

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tickets from hybrid table %s: %w", p.tableName, err)
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

		// Convert to map and extract BSON fields
		rowMap := make(map[string]interface{})
		bsonFields := make(map[string][]byte)
		bsonColumns := []string{"dynamic_fields", "user_fields", "timing_fields", "workflow_fields", "custom_fields"}

		for i, column := range columns {
			if contains(bsonColumns, column) {
				if values[i] != nil {
					if bsonBytes, ok := values[i].([]byte); ok {
						bsonFields[column] = bsonBytes
					}
				}
			} else {
				rowMap[column] = values[i]
			}
		}

		// Convert to protobuf
		ticketData := p.hybridRowToProtobuf(rowMap, bsonFields)
		tickets = append(tickets, ticketData)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	log.Printf("Retrieved %d tickets from hybrid table %s", len(tickets), p.tableName)
	return tickets, nil
}

// SearchTickets searches for tickets based on conditions
func (p *PostgreSQLDocumentDBStorage) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	return p.SearchTicketsWithProjection(request)
}

// SearchTicketsWithProjection searches for tickets with optional field projection
func (p *PostgreSQLDocumentDBStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Build WHERE clause for both fixed and dynamic fields
	whereClause, values, err := p.buildHybridWhereClause(request.Conditions)
	if err != nil {
		return nil, fmt.Errorf("failed to build WHERE clause: %w", err)
	}

	// Build ORDER BY clause
	orderByClause := p.buildOrderByClause(request.SortFields)

	// Build complete query
	var query string
	if whereClause != "" {
		query = fmt.Sprintf("SELECT * FROM %s WHERE %s %s", p.tableName, whereClause, orderByClause)
	} else {
		query = fmt.Sprintf("SELECT * FROM %s %s", p.tableName, orderByClause)
	}

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

		// Convert to map and extract BSON fields
		rowMap := make(map[string]interface{})
		bsonFields := make(map[string][]byte)
		bsonColumns := []string{"dynamic_fields", "user_fields", "timing_fields", "workflow_fields", "custom_fields"}

		for i, column := range columns {
			if contains(bsonColumns, column) {
				if values[i] != nil {
					if bsonBytes, ok := values[i].([]byte); ok {
						bsonFields[column] = bsonBytes
					}
				}
			} else {
				rowMap[column] = values[i]
			}
		}

		// Convert to protobuf
		ticketData := p.hybridRowToProtobuf(rowMap, bsonFields)

		// Apply field projection if requested
		if len(request.ProjectedFields) > 0 {
			ticketData = p.applyFieldProjection(ticketData, request.ProjectedFields)
		}

		tickets = append(tickets, ticketData)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	log.Printf("Found %d tickets matching search criteria in hybrid table", len(tickets))
	return tickets, nil
}

// buildHybridWhereClause builds a WHERE clause for hybrid schema (fixed + dynamic fields)
func (p *PostgreSQLDocumentDBStorage) buildHybridWhereClause(conditions []SearchCondition) (string, []interface{}, error) {
	var whereParts []string
	var values []interface{}
	paramIndex := 1

	if len(conditions) == 0 {
		return "", values, nil
	}

	mapping := p.getFieldMapping()

	for _, condition := range conditions {
		var clause string

		// Check if field is in fixed schema
		if dbColumn, isFixed := mapping.FixedFields[condition.Operand]; isFixed {
			// Handle fixed schema field
			switch strings.ToLower(condition.Operator) {
			case "eq", "=":
				clause = fmt.Sprintf("%s = $%d", dbColumn, paramIndex)
				values = append(values, condition.Value)
			case "ne", "!=":
				clause = fmt.Sprintf("%s != $%d", dbColumn, paramIndex)
				values = append(values, condition.Value)
			case "gt", ">":
				clause = fmt.Sprintf("%s > $%d", dbColumn, paramIndex)
				values = append(values, condition.Value)
			case "gte", ">=":
				clause = fmt.Sprintf("%s >= $%d", dbColumn, paramIndex)
				values = append(values, condition.Value)
			case "lt", "<":
				clause = fmt.Sprintf("%s < $%d", dbColumn, paramIndex)
				values = append(values, condition.Value)
			case "lte", "<=":
				clause = fmt.Sprintf("%s <= $%d", dbColumn, paramIndex)
				values = append(values, condition.Value)
			case "contains":
				clause = fmt.Sprintf("%s ILIKE $%d", dbColumn, paramIndex)
				values = append(values, fmt.Sprintf("%%%v%%", condition.Value))
			case "begins_with":
				clause = fmt.Sprintf("%s ILIKE $%d", dbColumn, paramIndex)
				values = append(values, fmt.Sprintf("%v%%", condition.Value))
			default:
				return "", nil, fmt.Errorf("unsupported operator for fixed field: %s", condition.Operator)
			}
		} else if bsonInfo, isDynamic := mapping.DynamicFields[condition.Operand]; isDynamic {
			// Handle dynamic field in BSON
			bsonField := fmt.Sprintf("%s->>'%s'", bsonInfo.BSONColumn, bsonInfo.FieldPath)

			switch strings.ToLower(condition.Operator) {
			case "eq", "=":
				if bsonInfo.DataType == "string" {
					clause = fmt.Sprintf("%s = $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				} else if bsonInfo.DataType == "number" {
					clause = fmt.Sprintf("(%s)::bigint = $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				} else if bsonInfo.DataType == "boolean" {
					clause = fmt.Sprintf("(%s)::boolean = $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				}
			case "ne", "!=":
				if bsonInfo.DataType == "string" {
					clause = fmt.Sprintf("%s != $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				} else if bsonInfo.DataType == "number" {
					clause = fmt.Sprintf("(%s)::bigint != $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				} else if bsonInfo.DataType == "boolean" {
					clause = fmt.Sprintf("(%s)::boolean != $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				}
			case "gt", ">":
				if bsonInfo.DataType == "number" {
					clause = fmt.Sprintf("(%s)::bigint > $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				} else {
					return "", nil, fmt.Errorf("gt operator only supported for number fields")
				}
			case "gte", ">=":
				if bsonInfo.DataType == "number" {
					clause = fmt.Sprintf("(%s)::bigint >= $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				} else {
					return "", nil, fmt.Errorf("gte operator only supported for number fields")
				}
			case "lt", "<":
				if bsonInfo.DataType == "number" {
					clause = fmt.Sprintf("(%s)::bigint < $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				} else {
					return "", nil, fmt.Errorf("lt operator only supported for number fields")
				}
			case "lte", "<=":
				if bsonInfo.DataType == "number" {
					clause = fmt.Sprintf("(%s)::bigint <= $%d", bsonField, paramIndex)
					values = append(values, condition.Value)
				} else {
					return "", nil, fmt.Errorf("lte operator only supported for number fields")
				}
			case "contains":
				if bsonInfo.DataType == "string" {
					clause = fmt.Sprintf("%s ILIKE $%d", bsonField, paramIndex)
					values = append(values, fmt.Sprintf("%%%v%%", condition.Value))
				} else {
					return "", nil, fmt.Errorf("contains operator only supported for string fields")
				}
			default:
				return "", nil, fmt.Errorf("unsupported operator for dynamic field: %s", condition.Operator)
			}
		} else {
			// Unknown field - search in all BSON columns
			clause = fmt.Sprintf("(dynamic_fields::text ILIKE $%d OR user_fields::text ILIKE $%d OR timing_fields::text ILIKE $%d OR workflow_fields::text ILIKE $%d OR custom_fields::text ILIKE $%d)",
				paramIndex, paramIndex+1, paramIndex+2, paramIndex+3, paramIndex+4)
			searchTerm := fmt.Sprintf("%%%s%%", condition.Value)
			values = append(values, searchTerm, searchTerm, searchTerm, searchTerm, searchTerm)
			paramIndex += 4 // We added 5 parameters, but the loop will increment by 1
		}

		whereParts = append(whereParts, clause)
		paramIndex++
	}

	whereClause := strings.Join(whereParts, " AND ")
	return whereClause, values, nil
}

// buildOrderByClause builds an ORDER BY clause from sort fields
func (p *PostgreSQLDocumentDBStorage) buildOrderByClause(sortFields []SortField) string {
	if len(sortFields) == 0 {
		return "ORDER BY created_at DESC" // Default sort
	}

	mapping := p.getFieldMapping()
	var orderParts []string

	for _, sortField := range sortFields {
		direction := "ASC"
		if strings.ToLower(sortField.Order) == "desc" {
			direction = "DESC"
		}

		// Check if field is in fixed schema
		if dbColumn, isFixed := mapping.FixedFields[sortField.Field]; isFixed {
			orderParts = append(orderParts, fmt.Sprintf("%s %s", dbColumn, direction))
		} else if bsonInfo, isDynamic := mapping.DynamicFields[sortField.Field]; isDynamic {
			// Handle dynamic field sorting in BSON
			if bsonInfo.DataType == "number" {
				bsonField := fmt.Sprintf("(%s->>'%s')::bigint", bsonInfo.BSONColumn, bsonInfo.FieldPath)
				orderParts = append(orderParts, fmt.Sprintf("%s %s", bsonField, direction))
			} else {
				bsonField := fmt.Sprintf("%s->>'%s'", bsonInfo.BSONColumn, bsonInfo.FieldPath)
				orderParts = append(orderParts, fmt.Sprintf("%s %s", bsonField, direction))
			}
		}
	}

	if len(orderParts) == 0 {
		return "ORDER BY created_at DESC" // Fallback
	}

	return "ORDER BY " + strings.Join(orderParts, ", ")
}

// applyFieldProjection filters ticket fields based on projected fields
func (p *PostgreSQLDocumentDBStorage) applyFieldProjection(ticketData *ticketpb.TicketData, projectedFields []string) *ticketpb.TicketData {
	if len(projectedFields) == 0 {
		return ticketData
	}

	projectedTicket := &ticketpb.TicketData{
		Id:        ticketData.Id,
		CreatedAt: ticketData.CreatedAt,
		UpdatedAt: ticketData.UpdatedAt,
		Fields:    make(map[string]*ticketpb.FieldValue),
	}

	// Create map for quick lookup
	projectedFieldsMap := make(map[string]bool)
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
func (p *PostgreSQLDocumentDBStorage) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}
