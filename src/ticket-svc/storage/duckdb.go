package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// DuckDBStorage implements TicketStorage interface using DuckDB
type DuckDBStorage struct {
	db               *sql.DB
	tableName        string
	localStoragePath string
	ctx              context.Context
}

// DuckDBConfig holds configuration for DuckDB storage
type DuckDBConfig struct {
	LocalStoragePath string // Local storage path instead of EBS (for now)
	TableName        string
}

// NewDuckDBStorage creates a new DuckDB storage instance
func NewDuckDBStorage(ctx context.Context, config DuckDBConfig) (*DuckDBStorage, error) {
	if config.TableName == "" {
		config.TableName = "tickets"
	}

	if config.LocalStoragePath == "" {
		config.LocalStoragePath = "./data"
	}

	// Create the database file path
	dbPath := filepath.Join(config.LocalStoragePath, "tickets.db")

	// Open DuckDB connection
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open DuckDB connection: %w", err)
	}

	storage := &DuckDBStorage{
		db:               db,
		tableName:        config.TableName,
		localStoragePath: config.LocalStoragePath,
		ctx:              ctx,
	}

	// Initialize the database schema
	if err := storage.initializeSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	log.Printf("DuckDB storage initialized with database: %s, table: %s", dbPath, config.TableName)
	return storage, nil
}

// initializeSchema creates the necessary tables and indexes
func (d *DuckDBStorage) initializeSchema() error {
	// Create core table following the architecture document structure
	coreTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s_core (
			ticket_id VARCHAR PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			status VARCHAR(50) NOT NULL,
			priority VARCHAR(20) NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)
	`, d.tableName)

	if _, err := d.db.ExecContext(d.ctx, coreTableSQL); err != nil {
		return fmt.Errorf("failed to create core table: %w", err)
	}

	// Create details table for content fields
	detailsTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s_details (
			ticket_id VARCHAR PRIMARY KEY,
			title TEXT,
			description TEXT,
			resolution TEXT
		)
	`, d.tableName)

	if _, err := d.db.ExecContext(d.ctx, detailsTableSQL); err != nil {
		return fmt.Errorf("failed to create details table: %w", err)
	}

	// Create assignment table for assignment fields
	assignmentTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s_assignment (
			ticket_id VARCHAR PRIMARY KEY,
			assigned_to INTEGER,
			assigned_group VARCHAR(100),
			assigned_at TIMESTAMP,
			assignee_name VARCHAR(200)
		)
	`, d.tableName)

	if _, err := d.db.ExecContext(d.ctx, assignmentTableSQL); err != nil {
		return fmt.Errorf("failed to create assignment table: %w", err)
	}

	// Create metadata table for categorization fields
	metadataTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s_metadata (
			ticket_id VARCHAR PRIMARY KEY,
			category VARCHAR(100),
			subcategory VARCHAR(100),
			tags TEXT, -- JSON string for array of strings
			custom_fields TEXT -- JSON string for additional custom fields
		)
	`, d.tableName)

	if _, err := d.db.ExecContext(d.ctx, metadataTableSQL); err != nil {
		return fmt.Errorf("failed to create metadata table: %w", err)
	}

	// Create SLA table for SLA-related fields
	slaTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s_sla (
			ticket_id VARCHAR PRIMARY KEY,
			sla_breach BOOLEAN DEFAULT FALSE,
			due_date TIMESTAMP,
			response_due_at TIMESTAMP,
			resolution_due_at TIMESTAMP
		)
	`, d.tableName)

	if _, err := d.db.ExecContext(d.ctx, slaTableSQL); err != nil {
		return fmt.Errorf("failed to create SLA table: %w", err)
	}

	// Create indexes for performance optimization
	indexes := []string{
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_core_tenant_status ON %s_core(tenant_id, status)", d.tableName, d.tableName),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_core_created_at ON %s_core(created_at)", d.tableName, d.tableName),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_core_updated_at ON %s_core(updated_at)", d.tableName, d.tableName),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_assignment_assigned_to ON %s_assignment(assigned_to)", d.tableName, d.tableName),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_metadata_category ON %s_metadata(category)", d.tableName, d.tableName),
	}

	for _, indexSQL := range indexes {
		if _, err := d.db.ExecContext(d.ctx, indexSQL); err != nil {
			log.Printf("Warning: Failed to create index: %v", err)
		}
	}

	log.Printf("DuckDB schema initialized with column family architecture")
	return nil
}

// CreateTicket creates a new ticket in the database
func (d *DuckDBStorage) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	tx, err := d.db.BeginTx(d.ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err), nil
	}
	defer tx.Rollback()

	// Extract core fields with defaults
	coreFields := d.extractCoreFields(ticketData)
	detailsFields := d.extractDetailsFields(ticketData)
	assignmentFields := d.extractAssignmentFields(ticketData)
	metadataFields := d.extractMetadataFields(ticketData)
	slaFields := d.extractSLAFields(ticketData)

	// Insert into core table
	coreSQL := fmt.Sprintf(`
		INSERT INTO %s_core (ticket_id, tenant_id, status, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, coreSQL,
		ticketData.Id,
		coreFields["tenant_id"],
		coreFields["status"],
		coreFields["priority"],
		ticketData.CreatedAt,
		ticketData.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert into core table: %w", err), nil
	}

	// Insert into details table
	detailsSQL := fmt.Sprintf(`
		INSERT INTO %s_details (ticket_id, title, description, resolution)
		VALUES (?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, detailsSQL,
		ticketData.Id,
		detailsFields["title"],
		detailsFields["description"],
		detailsFields["resolution"],
	)
	if err != nil {
		return fmt.Errorf("failed to insert into details table: %w", err), nil
	}

	// Insert into assignment table
	assignmentSQL := fmt.Sprintf(`
		INSERT INTO %s_assignment (ticket_id, assigned_to, assigned_group, assigned_at, assignee_name)
		VALUES (?, ?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, assignmentSQL,
		ticketData.Id,
		assignmentFields["assigned_to"],
		assignmentFields["assigned_group"],
		assignmentFields["assigned_at"],
		assignmentFields["assignee_name"],
	)
	if err != nil {
		return fmt.Errorf("failed to insert into assignment table: %w", err), nil
	}

	// Insert into metadata table
	customFieldsJSON, _ := json.Marshal(metadataFields["custom_fields"])
	tagsJSON, _ := json.Marshal(metadataFields["tags"])
	metadataSQL := fmt.Sprintf(`
		INSERT INTO %s_metadata (ticket_id, category, subcategory, tags, custom_fields)
		VALUES (?, ?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, metadataSQL,
		ticketData.Id,
		metadataFields["category"],
		metadataFields["subcategory"],
		string(tagsJSON),
		string(customFieldsJSON),
	)
	if err != nil {
		return fmt.Errorf("failed to insert into metadata table: %w", err), nil
	}

	// Insert into SLA table
	slaSQL := fmt.Sprintf(`
		INSERT INTO %s_sla (ticket_id, sla_breach, due_date, response_due_at, resolution_due_at)
		VALUES (?, ?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, slaSQL,
		ticketData.Id,
		slaFields["sla_breach"],
		slaFields["due_date"],
		slaFields["response_due_at"],
		slaFields["resolution_due_at"],
	)
	if err != nil {
		return fmt.Errorf("failed to insert into SLA table: %w", err), nil
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err), nil
	}

	// Return the ticket document
	ticketDoc := d.combineFields(coreFields, detailsFields, assignmentFields, metadataFields, slaFields, ticketData)
	return nil, ticketDoc
}

// GetTicket retrieves a ticket by ID
func (d *DuckDBStorage) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	query := fmt.Sprintf(`
		SELECT 
			c.ticket_id, c.tenant_id, c.status, c.priority, c.created_at, c.updated_at,
			dt.title, dt.description, dt.resolution,
			a.assigned_to, a.assigned_group, a.assigned_at, a.assignee_name,
			m.category, m.subcategory, m.tags, m.custom_fields,
			s.sla_breach, s.due_date, s.response_due_at, s.resolution_due_at
		FROM %s_core c
		LEFT JOIN %s_details dt ON c.ticket_id = dt.ticket_id
		LEFT JOIN %s_assignment a ON c.ticket_id = a.ticket_id
		LEFT JOIN %s_metadata m ON c.ticket_id = m.ticket_id
		LEFT JOIN %s_sla s ON c.ticket_id = s.ticket_id
		WHERE c.ticket_id = ?
	`, d.tableName, d.tableName, d.tableName, d.tableName, d.tableName)

	row := d.db.QueryRowContext(d.ctx, query, id)

	ticketData, err := d.scanTicketRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false
		}
		log.Printf("Error scanning ticket row: %v", err)
		return nil, false
	}

	return ticketData, true
}

// UpdateTicket updates an existing ticket
func (d *DuckDBStorage) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	tx, err := d.db.BeginTx(d.ctx, nil)
	if err != nil {
		log.Printf("Failed to begin transaction: %v", err)
		return false
	}
	defer tx.Rollback()

	// Extract fields
	coreFields := d.extractCoreFields(ticketData)
	detailsFields := d.extractDetailsFields(ticketData)
	assignmentFields := d.extractAssignmentFields(ticketData)
	metadataFields := d.extractMetadataFields(ticketData)
	slaFields := d.extractSLAFields(ticketData)

	// Update core table
	coreSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s_core (ticket_id, tenant_id, status, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, coreSQL,
		ticketData.Id,
		coreFields["tenant_id"],
		coreFields["status"],
		coreFields["priority"],
		ticketData.CreatedAt,
		ticketData.UpdatedAt,
	)
	if err != nil {
		log.Printf("Failed to update core table: %v", err)
		return false
	}

	// Update details table
	detailsSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s_details (ticket_id, title, description, resolution)
		VALUES (?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, detailsSQL,
		ticketData.Id,
		detailsFields["title"],
		detailsFields["description"],
		detailsFields["resolution"],
	)
	if err != nil {
		log.Printf("Failed to update details table: %v", err)
		return false
	}

	// Update assignment table
	assignmentSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s_assignment (ticket_id, assigned_to, assigned_group, assigned_at, assignee_name)
		VALUES (?, ?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, assignmentSQL,
		ticketData.Id,
		assignmentFields["assigned_to"],
		assignmentFields["assigned_group"],
		assignmentFields["assigned_at"],
		assignmentFields["assignee_name"],
	)
	if err != nil {
		log.Printf("Failed to update assignment table: %v", err)
		return false
	}

	// Update metadata table
	customFieldsJSON, _ := json.Marshal(metadataFields["custom_fields"])
	tagsJSON, _ := json.Marshal(metadataFields["tags"])
	metadataSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s_metadata (ticket_id, category, subcategory, tags, custom_fields)
		VALUES (?, ?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, metadataSQL,
		ticketData.Id,
		metadataFields["category"],
		metadataFields["subcategory"],
		string(tagsJSON),
		string(customFieldsJSON),
	)
	if err != nil {
		log.Printf("Failed to update metadata table: %v", err)
		return false
	}

	// Update SLA table
	slaSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s_sla (ticket_id, sla_breach, due_date, response_due_at, resolution_due_at)
		VALUES (?, ?, ?, ?, ?)
	`, d.tableName)

	_, err = tx.ExecContext(d.ctx, slaSQL,
		ticketData.Id,
		slaFields["sla_breach"],
		slaFields["due_date"],
		slaFields["response_due_at"],
		slaFields["resolution_due_at"],
	)
	if err != nil {
		log.Printf("Failed to update SLA table: %v", err)
		return false
	}

	if err = tx.Commit(); err != nil {
		log.Printf("Failed to commit transaction: %v", err)
		return false
	}

	return true
}

// DeleteTicket deletes a ticket by ID
func (d *DuckDBStorage) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
	// First, get the ticket to return it
	ticketData, found := d.GetTicket(id, nil)
	if !found {
		return nil, false
	}

	tx, err := d.db.BeginTx(d.ctx, nil)
	if err != nil {
		log.Printf("Failed to begin transaction: %v", err)
		return nil, false
	}
	defer tx.Rollback()

	// Delete from all tables in correct order (foreign key dependencies)
	tables := []string{"sla", "metadata", "assignment", "details", "core"}
	for _, table := range tables {
		deleteSQL := fmt.Sprintf("DELETE FROM %s_%s WHERE ticket_id = ?", d.tableName, table)
		_, err = tx.ExecContext(d.ctx, deleteSQL, id)
		if err != nil {
			log.Printf("Failed to delete from %s table: %v", table, err)
			return nil, false
		}
	}

	if err = tx.Commit(); err != nil {
		log.Printf("Failed to commit delete transaction: %v", err)
		return nil, false
	}

	return ticketData, true
}

// ListTickets retrieves all tickets
func (d *DuckDBStorage) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	query := fmt.Sprintf(`
		SELECT 
			c.ticket_id, c.tenant_id, c.status, c.priority, c.created_at, c.updated_at,
			dt.title, dt.description, dt.resolution,
			a.assigned_to, a.assigned_group, a.assigned_at, a.assignee_name,
			m.category, m.subcategory, m.tags, m.custom_fields,
			s.sla_breach, s.due_date, s.response_due_at, s.resolution_due_at
		FROM %s_core c
		LEFT JOIN %s_details dt ON c.ticket_id = dt.ticket_id
		LEFT JOIN %s_assignment a ON c.ticket_id = a.ticket_id
		LEFT JOIN %s_metadata m ON c.ticket_id = m.ticket_id
		LEFT JOIN %s_sla s ON c.ticket_id = s.ticket_id
		ORDER BY c.created_at DESC
	`, d.tableName, d.tableName, d.tableName, d.tableName, d.tableName)

	rows, err := d.db.QueryContext(d.ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list tickets: %w", err)
	}
	defer rows.Close()

	var tickets []*ticketpb.TicketData
	for rows.Next() {
		ticketData, err := d.scanTicketRow(rows)
		if err != nil {
			log.Printf("Error scanning ticket row: %v", err)
			continue
		}
		tickets = append(tickets, ticketData)
	}

	return tickets, nil
}

// SearchTickets searches tickets based on conditions
func (d *DuckDBStorage) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	return d.SearchTicketsWithProjection(request)
}

// SearchTicketsWithProjection searches tickets with field projection
func (d *DuckDBStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	// Build the query based on projection requirements
	selectClause := d.buildSelectClause(request.ProjectedFields)
	whereClause, args := d.buildWhereClause(request.Conditions)
	orderClause := d.buildOrderClause(request.SortFields)

	query := fmt.Sprintf(`
		%s
		%s
		%s
	`, selectClause, whereClause, orderClause)

	rows, err := d.db.QueryContext(d.ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}
	defer rows.Close()

	var tickets []*ticketpb.TicketData
	for rows.Next() {
		ticketData, err := d.scanTicketRow(rows)
		if err != nil {
			log.Printf("Error scanning ticket row: %v", err)
			continue
		}
		tickets = append(tickets, ticketData)
	}

	return tickets, nil
}

// Close closes the database connection
func (d *DuckDBStorage) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// Helper methods for field extraction and building queries
func (d *DuckDBStorage) extractCoreFields(ticketData *ticketpb.TicketData) map[string]interface{} {
	fields := map[string]interface{}{
		"tenant_id": int64(1), // Default tenant
		"status":    "New",    // Default status
		"priority":  "Medium", // Default priority
	}

	if tenantField, exists := ticketData.Fields["tenant_id"]; exists {
		if intVal := tenantField.GetIntValue(); intVal != 0 {
			fields["tenant_id"] = intVal
		}
	}

	if statusField, exists := ticketData.Fields["status"]; exists {
		strVal := statusField.GetStringValue()
		if strVal != "" {
			fields["status"] = strVal
		}
	}

	if priorityField, exists := ticketData.Fields["priority"]; exists {
		strVal := priorityField.GetStringValue()
		if strVal != "" {
			fields["priority"] = strVal
		}
	}

	return fields
}

func (d *DuckDBStorage) extractDetailsFields(ticketData *ticketpb.TicketData) map[string]interface{} {
	fields := map[string]interface{}{
		"title":       nil,
		"description": nil,
		"resolution":  nil,
	}

	if titleField, exists := ticketData.Fields["title"]; exists {
		fields["title"] = titleField.GetStringValue()
	}

	if descField, exists := ticketData.Fields["description"]; exists {
		fields["description"] = descField.GetStringValue()
	}

	if resField, exists := ticketData.Fields["resolution"]; exists {
		fields["resolution"] = resField.GetStringValue()
	}

	return fields
}

func (d *DuckDBStorage) extractAssignmentFields(ticketData *ticketpb.TicketData) map[string]interface{} {
	fields := map[string]interface{}{
		"assigned_to":    nil,
		"assigned_group": nil,
		"assigned_at":    nil,
		"assignee_name":  nil,
	}

	if assignedToField, exists := ticketData.Fields["assigned_to"]; exists {
		if intVal := assignedToField.GetIntValue(); intVal != 0 {
			fields["assigned_to"] = intVal
		}
	}

	if assignedGroupField, exists := ticketData.Fields["assigned_group"]; exists {
		fields["assigned_group"] = assignedGroupField.GetStringValue()
	}

	if assignedAtField, exists := ticketData.Fields["assigned_at"]; exists {
		fields["assigned_at"] = assignedAtField.GetStringValue()
	}

	if assigneeNameField, exists := ticketData.Fields["assignee_name"]; exists {
		fields["assignee_name"] = assigneeNameField.GetStringValue()
	}

	return fields
}

func (d *DuckDBStorage) extractMetadataFields(ticketData *ticketpb.TicketData) map[string]interface{} {
	fields := map[string]interface{}{
		"category":      nil,
		"subcategory":   nil,
		"tags":          []string{},
		"custom_fields": map[string]interface{}{},
	}

	if categoryField, exists := ticketData.Fields["category"]; exists {
		fields["category"] = categoryField.GetStringValue()
	}

	if subcategoryField, exists := ticketData.Fields["subcategory"]; exists {
		fields["subcategory"] = subcategoryField.GetStringValue()
	}

	if tagsField, exists := ticketData.Fields["tags"]; exists {
		if stringArray := tagsField.GetStringArray(); stringArray != nil {
			fields["tags"] = stringArray.Values
		}
	}

	// Collect any additional custom fields not in core families
	customFields := map[string]interface{}{}
	knownFields := map[string]bool{
		"tenant_id": true, "status": true, "priority": true,
		"title": true, "description": true, "resolution": true,
		"assigned_to": true, "assigned_group": true, "assigned_at": true, "assignee_name": true,
		"category": true, "subcategory": true, "tags": true,
		"sla_breach": true, "due_date": true, "response_due_at": true, "resolution_due_at": true,
	}

	for fieldName, fieldValue := range ticketData.Fields {
		if !knownFields[fieldName] {
			customFields[fieldName] = d.convertFieldValueToInterface(fieldValue)
		}
	}

	if len(customFields) > 0 {
		fields["custom_fields"] = customFields
	}

	return fields
}

func (d *DuckDBStorage) extractSLAFields(ticketData *ticketpb.TicketData) map[string]interface{} {
	fields := map[string]interface{}{
		"sla_breach":        false,
		"due_date":          nil,
		"response_due_at":   nil,
		"resolution_due_at": nil,
	}

	if slaBreachField, exists := ticketData.Fields["sla_breach"]; exists {
		fields["sla_breach"] = slaBreachField.GetBoolValue()
	}

	if dueDateField, exists := ticketData.Fields["due_date"]; exists {
		fields["due_date"] = dueDateField.GetStringValue()
	}

	if responseDueField, exists := ticketData.Fields["response_due_at"]; exists {
		fields["response_due_at"] = responseDueField.GetStringValue()
	}

	if resolutionDueField, exists := ticketData.Fields["resolution_due_at"]; exists {
		fields["resolution_due_at"] = resolutionDueField.GetStringValue()
	}

	return fields
}

func (d *DuckDBStorage) convertFieldValueToInterface(fieldValue *ticketpb.FieldValue) interface{} {
	if fieldValue == nil {
		return nil
	}

	switch v := fieldValue.Value.(type) {
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
		return nil
	}
}

func (d *DuckDBStorage) combineFields(coreFields, detailsFields, assignmentFields, metadataFields, slaFields map[string]interface{}, ticketData *ticketpb.TicketData) map[string]interface{} {
	result := map[string]interface{}{
		"id":         ticketData.Id,
		"created_at": ticketData.CreatedAt,
		"updated_at": ticketData.UpdatedAt,
	}

	// Add all fields from different families
	for k, v := range coreFields {
		if v != nil {
			result[k] = v
		}
	}

	for k, v := range detailsFields {
		if v != nil {
			result[k] = v
		}
	}

	for k, v := range assignmentFields {
		if v != nil {
			result[k] = v
		}
	}

	for k, v := range metadataFields {
		if v != nil {
			result[k] = v
		}
	}

	for k, v := range slaFields {
		if v != nil {
			result[k] = v
		}
	}

	return result
}

func (d *DuckDBStorage) buildSelectClause(projectedFields []string) string {
	if len(projectedFields) == 0 {
		// Return all fields
		return fmt.Sprintf(`
			SELECT 
				c.ticket_id, c.tenant_id, c.status, c.priority, c.created_at, c.updated_at,
				dt.title, dt.description, dt.resolution,
				a.assigned_to, a.assigned_group, a.assigned_at, a.assignee_name,
				m.category, m.subcategory, m.tags, m.custom_fields,
				s.sla_breach, s.due_date, s.response_due_at, s.resolution_due_at
			FROM %s_core c
			LEFT JOIN %s_details dt ON c.ticket_id = dt.ticket_id
			LEFT JOIN %s_assignment a ON c.ticket_id = a.ticket_id
			LEFT JOIN %s_metadata m ON c.ticket_id = m.ticket_id
			LEFT JOIN %s_sla s ON c.ticket_id = s.ticket_id
		`, d.tableName, d.tableName, d.tableName, d.tableName, d.tableName)
	}

	// Determine which tables to join based on projected fields
	needsDetails := false
	needsAssignment := false
	needsMetadata := false
	needsSLA := false

	fieldMap := map[string]string{
		"title": "dt.title", "description": "dt.description", "resolution": "dt.resolution",
		"assigned_to": "a.assigned_to", "assigned_group": "a.assigned_group", "assigned_at": "a.assigned_at", "assignee_name": "a.assignee_name",
		"category": "m.category", "subcategory": "m.subcategory", "tags": "m.tags", "custom_fields": "m.custom_fields",
		"sla_breach": "s.sla_breach", "due_date": "s.due_date", "response_due_at": "s.response_due_at", "resolution_due_at": "s.resolution_due_at",
		"tenant_id": "c.tenant_id", "status": "c.status", "priority": "c.priority", "created_at": "c.created_at", "updated_at": "c.updated_at",
	}

	var selectFields []string
	selectFields = append(selectFields, "c.ticket_id") // Always include ID

	for _, field := range projectedFields {
		if dbField, exists := fieldMap[field]; exists {
			selectFields = append(selectFields, dbField)

			// Determine table joins needed
			if strings.HasPrefix(dbField, "dt.") {
				needsDetails = true
			} else if strings.HasPrefix(dbField, "a.") {
				needsAssignment = true
			} else if strings.HasPrefix(dbField, "m.") {
				needsMetadata = true
			} else if strings.HasPrefix(dbField, "s.") {
				needsSLA = true
			}
		}
	}

	// Build the FROM clause with necessary joins
	fromClause := fmt.Sprintf("FROM %s_core c", d.tableName)

	if needsDetails {
		fromClause += fmt.Sprintf(" LEFT JOIN %s_details dt ON c.ticket_id = dt.ticket_id", d.tableName)
	}
	if needsAssignment {
		fromClause += fmt.Sprintf(" LEFT JOIN %s_assignment a ON c.ticket_id = a.ticket_id", d.tableName)
	}
	if needsMetadata {
		fromClause += fmt.Sprintf(" LEFT JOIN %s_metadata m ON c.ticket_id = m.ticket_id", d.tableName)
	}
	if needsSLA {
		fromClause += fmt.Sprintf(" LEFT JOIN %s_sla s ON c.ticket_id = s.ticket_id", d.tableName)
	}

	return fmt.Sprintf("SELECT %s %s", strings.Join(selectFields, ", "), fromClause)
}

func (d *DuckDBStorage) buildWhereClause(conditions []SearchCondition) (string, []interface{}) {
	if len(conditions) == 0 {
		return "", nil
	}

	var whereParts []string
	var args []interface{}

	// Map field names to table prefixes
	fieldTableMap := map[string]string{
		"title": "dt", "description": "dt", "resolution": "dt",
		"assigned_to": "a", "assigned_group": "a", "assigned_at": "a", "assignee_name": "a",
		"category": "m", "subcategory": "m", "tags": "m",
		"sla_breach": "s", "due_date": "s", "response_due_at": "s", "resolution_due_at": "s",
		"tenant_id": "c", "status": "c", "priority": "c", "created_at": "c", "updated_at": "c",
	}

	for _, condition := range conditions {
		tablePrefix := "c" // Default to core table
		if prefix, exists := fieldTableMap[condition.Operand]; exists {
			tablePrefix = prefix
		}

		fieldName := fmt.Sprintf("%s.%s", tablePrefix, condition.Operand)

		switch condition.Operator {
		case "eq":
			whereParts = append(whereParts, fmt.Sprintf("%s = ?", fieldName))
			args = append(args, condition.Value)
		case "ne":
			whereParts = append(whereParts, fmt.Sprintf("%s != ?", fieldName))
			args = append(args, condition.Value)
		case "gt":
			whereParts = append(whereParts, fmt.Sprintf("%s > ?", fieldName))
			args = append(args, condition.Value)
		case "lt":
			whereParts = append(whereParts, fmt.Sprintf("%s < ?", fieldName))
			args = append(args, condition.Value)
		case "gte":
			whereParts = append(whereParts, fmt.Sprintf("%s >= ?", fieldName))
			args = append(args, condition.Value)
		case "lte":
			whereParts = append(whereParts, fmt.Sprintf("%s <= ?", fieldName))
			args = append(args, condition.Value)
		case "contains":
			whereParts = append(whereParts, fmt.Sprintf("%s LIKE ?", fieldName))
			args = append(args, fmt.Sprintf("%%%v%%", condition.Value))
		case "begins_with":
			whereParts = append(whereParts, fmt.Sprintf("%s LIKE ?", fieldName))
			args = append(args, fmt.Sprintf("%v%%", condition.Value))
		}
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	return whereClause, args
}

func (d *DuckDBStorage) buildOrderClause(sortFields []SortField) string {
	if len(sortFields) == 0 {
		return "ORDER BY c.created_at DESC"
	}

	// Map field names to table prefixes
	fieldTableMap := map[string]string{
		"title": "dt", "description": "dt", "resolution": "dt",
		"assigned_to": "a", "assigned_group": "a", "assigned_at": "a", "assignee_name": "a",
		"category": "m", "subcategory": "m", "tags": "m",
		"sla_breach": "s", "due_date": "s", "response_due_at": "s", "resolution_due_at": "s",
		"tenant_id": "c", "status": "c", "priority": "c", "created_at": "c", "updated_at": "c",
	}

	var orderParts []string
	for _, sortField := range sortFields {
		tablePrefix := "c" // Default to core table
		if prefix, exists := fieldTableMap[sortField.Field]; exists {
			tablePrefix = prefix
		}

		fieldName := fmt.Sprintf("%s.%s", tablePrefix, sortField.Field)
		direction := "ASC"
		if strings.ToUpper(sortField.Order) == "DESC" {
			direction = "DESC"
		}

		orderParts = append(orderParts, fmt.Sprintf("%s %s", fieldName, direction))
	}

	return "ORDER BY " + strings.Join(orderParts, ", ")
}

func (d *DuckDBStorage) scanTicketRow(scanner interface{ Scan(...interface{}) error }) (*ticketpb.TicketData, error) {
	var (
		ticketID, status, priority, createdAt, updatedAt sql.NullString
		tenantID                                         sql.NullInt64
		title, description, resolution                   sql.NullString
		assignedTo                                       sql.NullInt64
		assignedGroup, assignedAt, assigneeName          sql.NullString
		category, subcategory, customFieldsJSON          sql.NullString
		tagsJSON                                         sql.NullString
		slaBreach                                        sql.NullBool
		dueDate, responseDueAt, resolutionDueAt          sql.NullString
	)

	err := scanner.Scan(
		&ticketID, &tenantID, &status, &priority, &createdAt, &updatedAt,
		&title, &description, &resolution,
		&assignedTo, &assignedGroup, &assignedAt, &assigneeName,
		&category, &subcategory, &tagsJSON, &customFieldsJSON,
		&slaBreach, &dueDate, &responseDueAt, &resolutionDueAt,
	)
	if err != nil {
		return nil, err
	}

	// Create TicketData with protobuf fields
	fields := make(map[string]*ticketpb.FieldValue)

	// Core fields
	if tenantID.Valid {
		fields["tenant_id"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_IntValue{IntValue: tenantID.Int64}}
	}
	if status.Valid {
		fields["status"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: status.String}}
	}
	if priority.Valid {
		fields["priority"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: priority.String}}
	}

	// Details fields
	if title.Valid {
		fields["title"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: title.String}}
	}
	if description.Valid {
		fields["description"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: description.String}}
	}
	if resolution.Valid {
		fields["resolution"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: resolution.String}}
	}

	// Assignment fields
	if assignedTo.Valid {
		fields["assigned_to"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_IntValue{IntValue: assignedTo.Int64}}
	}
	if assignedGroup.Valid {
		fields["assigned_group"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: assignedGroup.String}}
	}
	if assignedAt.Valid {
		fields["assigned_at"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: assignedAt.String}}
	}
	if assigneeName.Valid {
		fields["assignee_name"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: assigneeName.String}}
	}

	// Metadata fields
	if category.Valid {
		fields["category"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: category.String}}
	}
	if subcategory.Valid {
		fields["subcategory"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: subcategory.String}}
	}
	if tagsJSON.Valid && tagsJSON.String != "" && tagsJSON.String != "null" {
		var tags []string
		if json.Unmarshal([]byte(tagsJSON.String), &tags) == nil && len(tags) > 0 {
			fields["tags"] = &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_StringArray{
					StringArray: &ticketpb.StringArray{Values: tags},
				},
			}
		}
	}

	// Custom fields
	if customFieldsJSON.Valid && customFieldsJSON.String != "" && customFieldsJSON.String != "null" {
		var customFields map[string]interface{}
		if json.Unmarshal([]byte(customFieldsJSON.String), &customFields) == nil {
			for k, v := range customFields {
				if fieldValue := d.convertToFieldValue(v); fieldValue != nil {
					fields[k] = fieldValue
				}
			}
		}
	}

	// SLA fields
	if slaBreach.Valid {
		fields["sla_breach"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_BoolValue{BoolValue: slaBreach.Bool}}
	}
	if dueDate.Valid {
		fields["due_date"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: dueDate.String}}
	}
	if responseDueAt.Valid {
		fields["response_due_at"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: responseDueAt.String}}
	}
	if resolutionDueAt.Valid {
		fields["resolution_due_at"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: resolutionDueAt.String}}
	}

	return &ticketpb.TicketData{
		Id:        ticketID.String,
		CreatedAt: createdAt.String,
		UpdatedAt: updatedAt.String,
		Fields:    fields,
	}, nil
}

func (d *DuckDBStorage) convertToFieldValue(value interface{}) *ticketpb.FieldValue {
	switch v := value.(type) {
	case string:
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: v}}
	case int:
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)}}
	case int64:
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_IntValue{IntValue: v}}
	case float64:
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: v}}
	case bool:
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_BoolValue{BoolValue: v}}
	case []string:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringArray{
				StringArray: &ticketpb.StringArray{Values: v},
			},
		}
	default:
		return &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: fmt.Sprintf("%v", v)}}
	}
}
