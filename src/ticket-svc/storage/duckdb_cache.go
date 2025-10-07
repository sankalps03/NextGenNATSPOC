package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// DuckDBCache implements a cache layer using DuckDB with PostgreSQL fallback
type DuckDBCache struct {
	ctx              context.Context
	duckDB           *sql.DB
	postgresStorage  *PostgreSQLDynamicStorage // Fallback to PostgreSQL
	localStoragePath string
	tableName        string
	natsConn         *nats.Conn
	mu               sync.RWMutex

	// Parquet and delta file management
	deltaBuffer      []DeltaEntry
	deltaBufferMu    sync.Mutex
	lastCompaction   time.Time
	compactionTicker *time.Ticker

	// Channel for delta flush coordination
	deltaFlushChan chan struct{}
}

// DeltaEntry represents a change to be written to delta files
type DeltaEntry struct {
	TicketID  string
	Operation string // "create", "update", "delete"
	Timestamp time.Time
	Data      *ticketpb.TicketData
}

// DuckDBCacheConfig holds configuration for DuckDB cache
type DuckDBCacheConfig struct {
	LocalStoragePath   string
	TableName          string
	PostgreSQLConfig   *PostgreSQLConfig
	NATSConn           *nats.Conn
	CompactionInterval time.Duration
	DeltaFlushInterval time.Duration
}

// PostgreSQLConfig for fallback
type PostgreSQLConfig struct {
	ConnectionString string
	TableName        string
}

// NewDuckDBCache creates a new DuckDB cache with PostgreSQL fallback
func NewDuckDBCache(ctx context.Context, config DuckDBCacheConfig) (*DuckDBCache, error) {
	if config.TableName == "" {
		config.TableName = "tickets"
	}
	if config.LocalStoragePath == "" {
		config.LocalStoragePath = "./data"
	}
	if config.CompactionInterval == 0 {
		config.CompactionInterval = 1 * time.Hour
	}
	if config.DeltaFlushInterval == 0 {
		config.DeltaFlushInterval = 5 * time.Minute
	}

	// Create directory structure for hot/warm/cold tiers
	if err := os.MkdirAll(filepath.Join(config.LocalStoragePath, "hot"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create hot directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(config.LocalStoragePath, "warm"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create warm directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(config.LocalStoragePath, "cold"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create cold directory: %w", err)
	}

	// Initialize PostgreSQL fallback
	var postgresStorage *PostgreSQLDynamicStorage
	if config.PostgreSQLConfig != nil {
		var err error
		postgresStorage, err = NewPostgreSQLDynamicStorage(ctx, config.PostgreSQLConfig.TableName, config.PostgreSQLConfig.ConnectionString)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize PostgreSQL fallback: %w", err)
		}
	}

	// Open DuckDB in-memory with ability to read/write Parquet files
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("failed to open DuckDB: %w", err)
	}

	cache := &DuckDBCache{
		ctx:              ctx,
		duckDB:           db,
		postgresStorage:  postgresStorage,
		localStoragePath: config.LocalStoragePath,
		tableName:        config.TableName,
		natsConn:         config.NATSConn,
		deltaBuffer:      make([]DeltaEntry, 0, 1000),
		lastCompaction:   time.Now(),
		deltaFlushChan:   make(chan struct{}, 1), // Buffered channel to avoid blocking
	}

	// Initialize DuckDB schema
	if err := cache.initializeSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize DuckDB schema: %w", err)
	}

	// Load hot data from Parquet files if they exist
	if err := cache.loadHotDataFromParquet(); err != nil {
		log.Printf("Warning: Failed to load hot data from Parquet: %v", err)
	}

	// Start background processes
	go cache.startDeltaFlusher(config.DeltaFlushInterval)
	go cache.startCompactionWorker(config.CompactionInterval)

	// Subscribe to NATS events for cache synchronization
	if config.NATSConn != nil {
		if err := cache.subscribeToTicketEvents(); err != nil {
			log.Printf("Warning: Failed to subscribe to NATS events: %v", err)
		}
	}

	log.Printf("DuckDB cache initialized with PostgreSQL fallback")
	log.Printf("Hot/Warm/Cold tiers configured at: %s", config.LocalStoragePath)

	return cache, nil
}

// initializeSchema creates in-memory DuckDB tables following column family architecture
func (c *DuckDBCache) initializeSchema() error {
	// Enable Parquet extension
	if _, err := c.duckDB.ExecContext(c.ctx, "INSTALL parquet"); err != nil {
		log.Printf("Warning: Failed to install parquet extension: %v", err)
	}
	if _, err := c.duckDB.ExecContext(c.ctx, "LOAD parquet"); err != nil {
		log.Printf("Warning: Failed to load parquet extension: %v", err)
	}

	// Create core table (in-memory)
	coreTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s_core (
			ticket_id VARCHAR PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			status VARCHAR(50) NOT NULL,
			priority VARCHAR(20) NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)
	`, c.tableName)

	if _, err := c.duckDB.ExecContext(c.ctx, coreTableSQL); err != nil {
		return fmt.Errorf("failed to create core table: %w", err)
	}

	// Create other column family tables
	tables := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s_details (
			ticket_id VARCHAR PRIMARY KEY,
			title TEXT,
			description TEXT,
			resolution TEXT
		)`, c.tableName),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s_assignment (
			ticket_id VARCHAR PRIMARY KEY,
			assigned_to INTEGER,
			assigned_group VARCHAR(100),
			assigned_at TIMESTAMP,
			assignee_name VARCHAR(200)
		)`, c.tableName),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s_metadata (
			ticket_id VARCHAR PRIMARY KEY,
			category VARCHAR(100),
			subcategory VARCHAR(100),
			tags TEXT,
			custom_fields TEXT
		)`, c.tableName),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s_sla (
			ticket_id VARCHAR PRIMARY KEY,
			sla_breach BOOLEAN DEFAULT FALSE,
			due_date TIMESTAMP,
			response_due_at TIMESTAMP,
			resolution_due_at TIMESTAMP
		)`, c.tableName),
	}

	for _, tableSQL := range tables {
		if _, err := c.duckDB.ExecContext(c.ctx, tableSQL); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
	}

	log.Printf("DuckDB in-memory cache schema initialized")
	return nil
}

// CreateTicket creates a ticket - writes to PostgreSQL first, then updates cache
func (c *DuckDBCache) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	// Step 1: Write to PostgreSQL (source of truth)
	if c.postgresStorage != nil {
		err, result := c.postgresStorage.CreateTicket(ticketData)
		if err != nil {
			return err, nil
		}

		// Step 2: Update local DuckDB cache
		if err := c.updateCacheFromTicket(ticketData); err != nil {
			log.Printf("Warning: Failed to update cache after create: %v", err)
		}

		// Step 3: Add to delta buffer for eventual Parquet write
		c.addToDeltaBuffer("create", ticketData)

		// Step 4: Publish NATS event for other instances
		if c.natsConn != nil {
			c.publishCacheUpdateEvent("ticket.created", ticketData)
		}

		return nil, result
	}

	return fmt.Errorf("PostgreSQL storage not configured"), nil
}

// GetTicket retrieves a ticket - tries cache first, then warm/cold tiers, falls back to PostgreSQL
func (c *DuckDBCache) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	// Step 1: Try to get from DuckDB cache (hot tier)
	ticket, found := c.getFromCache(id)
	if found {
		log.Printf("Cache HIT (hot) for ticket %s", id)
		return ticket, true
	}

	// Step 2: Try warm and cold tiers (Parquet files)
	ticket, found = c.getFromTieredStorage(id)
	if found {
		log.Printf("Cache HIT (warm/cold) for ticket %s", id)
		// Promote to hot tier for faster access
		if err := c.updateCacheFromTicket(ticket); err != nil {
			log.Printf("Warning: Failed to promote ticket to hot tier: %v", err)
		}
		return ticket, true
	}

	log.Printf("Cache MISS for ticket %s", id)

	// Step 3: Fall back to PostgreSQL
	if c.postgresStorage != nil {
		ticket, found = c.postgresStorage.GetTicket(id, store)
		if found {
			// Update cache for next time
			if err := c.updateCacheFromTicket(ticket); err != nil {
				log.Printf("Warning: Failed to update cache after get: %v", err)
			}
		}
		return ticket, found
	}

	return nil, false
}

// UpdateTicket updates a ticket - writes to PostgreSQL and publishes event
func (c *DuckDBCache) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	// Step 1: Update PostgreSQL (source of truth)
	if c.postgresStorage != nil {
		if !c.postgresStorage.UpdateTicket(ticketData) {
			return false
		}

		// Step 2: Update local cache immediately (read-your-writes consistency)
		if err := c.updateCacheFromTicket(ticketData); err != nil {
			log.Printf("Warning: Failed to update cache after update: %v", err)
		}

		// Step 3: Add to delta buffer (not full rewrite of Parquet)
		c.addToDeltaBuffer("update", ticketData)

		// Step 4: Publish NATS event for other instances
		if c.natsConn != nil {
			c.publishCacheUpdateEvent("ticket.updated", ticketData)
		}

		return true
	}

	return false
}

// DeleteTicket deletes a ticket
func (c *DuckDBCache) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
	// Step 1: Delete from PostgreSQL
	if c.postgresStorage != nil {
		ticket, found := c.postgresStorage.DeleteTicket(id)
		if found {
			// Step 2: Remove from cache
			c.removeFromCache(id)

			// Step 3: Add deletion to delta buffer
			c.addToDeltaBuffer("delete", ticket)

			// Step 4: Publish NATS event
			if c.natsConn != nil {
				c.publishCacheUpdateEvent("ticket.deleted", ticket)
			}
		}
		return ticket, found
	}

	return nil, false
}

// ListTickets lists all tickets
func (c *DuckDBCache) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	// Try cache first
	tickets, err := c.listFromCache()
	if err == nil && len(tickets) > 0 {
		log.Printf("Cache HIT for list operation (%d tickets)", len(tickets))
		return tickets, nil
	}

	// Fall back to PostgreSQL
	if c.postgresStorage != nil {
		return c.postgresStorage.ListTickets(store)
	}

	return nil, fmt.Errorf("no data source available")
}

// SearchTickets searches tickets with conditions
func (c *DuckDBCache) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	// Use cache for search
	return c.searchInCache(request)
}

// SearchTicketsWithProjection searches with field projection
func (c *DuckDBCache) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	// Use optimized projection query on cache
	return c.searchInCacheWithProjection(request)
}

// Close closes the cache
func (c *DuckDBCache) Close() error {
	// Flush remaining delta buffer
	c.flushDeltaBuffer()

	// Stop tickers
	if c.compactionTicker != nil {
		c.compactionTicker.Stop()
	}

	// Close delta flush channel
	if c.deltaFlushChan != nil {
		close(c.deltaFlushChan)
	}

	// Close DuckDB
	if c.duckDB != nil {
		c.duckDB.Close()
	}

	// Close PostgreSQL
	if c.postgresStorage != nil {
		c.postgresStorage.Close()
	}

	return nil
}

// loadHotDataFromParquet loads hot tier data from Parquet files into memory
func (c *DuckDBCache) loadHotDataFromParquet() error {
	hotPath := filepath.Join(c.localStoragePath, "hot")

	// Load base Parquet file if exists
	basePath := filepath.Join(hotPath, fmt.Sprintf("%s_base.parquet", c.tableName))
	if _, err := os.Stat(basePath); err == nil {
		// Load core table from Parquet
		loadSQL := fmt.Sprintf(`
			INSERT INTO %s_core 
			SELECT * FROM read_parquet('%s')
		`, c.tableName, basePath)

		if _, err := c.duckDB.ExecContext(c.ctx, loadSQL); err != nil {
			log.Printf("Warning: Failed to load base Parquet: %v", err)
		} else {
			log.Printf("Loaded hot data from Parquet: %s", basePath)
		}
	}

	// Load delta files
	deltaPattern := filepath.Join(hotPath, fmt.Sprintf("%s_delta_*.parquet", c.tableName))
	deltaSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s_core 
		SELECT * FROM read_parquet('%s')
	`, c.tableName, deltaPattern)

	if _, err := c.duckDB.ExecContext(c.ctx, deltaSQL); err != nil {
		// It's okay if no delta files exist yet
		log.Printf("No delta files to load or error: %v", err)
	}

	return nil
}

// updateCacheFromTicket updates the in-memory cache with ticket data
func (c *DuckDBCache) updateCacheFromTicket(ticketData *ticketpb.TicketData) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	tx, err := c.duckDB.BeginTx(c.ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Extract fields for each column family
	coreFields := c.extractCoreFields(ticketData)

	// Insert or replace in core table
	coreSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s_core (ticket_id, tenant_id, status, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, c.tableName)

	_, err = tx.ExecContext(c.ctx, coreSQL,
		ticketData.Id,
		coreFields["tenant_id"],
		coreFields["status"],
		coreFields["priority"],
		ticketData.CreatedAt,
		ticketData.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update cache: %w", err)
	}

	// Update details table
	detailsFields := c.extractDetailsFields(ticketData)
	detailsSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s_details (ticket_id, title, description, resolution)
		VALUES (?, ?, ?, ?)
	`, c.tableName)

	_, err = tx.ExecContext(c.ctx, detailsSQL,
		ticketData.Id,
		detailsFields["title"],
		detailsFields["description"],
		detailsFields["resolution"],
	)
	if err != nil {
		return fmt.Errorf("failed to update details cache: %w", err)
	}

	// Update assignment table
	assignmentFields := c.extractAssignmentFields(ticketData)
	assignmentSQL := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s_assignment (ticket_id, assigned_to, assigned_group, assigned_at, assignee_name)
		VALUES (?, ?, ?, ?, ?)
	`, c.tableName)

	_, err = tx.ExecContext(c.ctx, assignmentSQL,
		ticketData.Id,
		assignmentFields["assigned_to"],
		assignmentFields["assigned_group"],
		assignmentFields["assigned_at"],
		assignmentFields["assignee_name"],
	)
	if err != nil {
		return fmt.Errorf("failed to update assignment cache: %w", err)
	}

	return tx.Commit()
}

// getFromCache retrieves a ticket from the cache
func (c *DuckDBCache) getFromCache(id string) (*ticketpb.TicketData, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	query := fmt.Sprintf(`
		SELECT c.ticket_id, c.tenant_id, c.status, c.priority, c.created_at, c.updated_at
		FROM %s_core c
		WHERE c.ticket_id = ?
	`, c.tableName)

	row := c.duckDB.QueryRowContext(c.ctx, query, id)

	var ticketID, status, priority, createdAt, updatedAt string
	var tenantID int64

	err := row.Scan(&ticketID, &tenantID, &status, &priority, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false
		}
		log.Printf("Error scanning ticket from cache: %v", err)
		return nil, false
	}

	// Construct TicketData
	ticketData := &ticketpb.TicketData{
		Id:        ticketID,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		Fields: map[string]*ticketpb.FieldValue{
			"tenant_id": {Value: &ticketpb.FieldValue_IntValue{IntValue: tenantID}},
			"status":    {Value: &ticketpb.FieldValue_StringValue{StringValue: status}},
			"priority":  {Value: &ticketpb.FieldValue_StringValue{StringValue: priority}},
		},
	}

	return ticketData, true
}

// removeFromCache removes a ticket from the cache
func (c *DuckDBCache) removeFromCache(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tables := []string{"core", "details", "assignment", "metadata", "sla"}
	for _, table := range tables {
		sql := fmt.Sprintf("DELETE FROM %s_%s WHERE ticket_id = ?", c.tableName, table)
		c.duckDB.ExecContext(c.ctx, sql, id)
	}
}

// addToDeltaBuffer adds an operation to the delta buffer
func (c *DuckDBCache) addToDeltaBuffer(operation string, ticketData *ticketpb.TicketData) {
	c.deltaBufferMu.Lock()
	defer c.deltaBufferMu.Unlock()

	entry := DeltaEntry{
		TicketID:  ticketData.Id,
		Operation: operation,
		Timestamp: time.Now(),
		Data:      ticketData,
	}

	c.deltaBuffer = append(c.deltaBuffer, entry)

	// Auto-flush if buffer is getting large
	if len(c.deltaBuffer) >= 1000 {
		// Send signal to delta flusher goroutine (non-blocking)
		select {
		case c.deltaFlushChan <- struct{}{}:
			// Signal sent successfully
		default:
			// Channel is full, flush already in progress
		}
	}
}

// flushDeltaBuffer writes delta buffer to Parquet files
func (c *DuckDBCache) flushDeltaBuffer() {
	c.deltaBufferMu.Lock()
	if len(c.deltaBuffer) == 0 {
		c.deltaBufferMu.Unlock()
		return
	}

	buffer := c.deltaBuffer
	c.deltaBuffer = make([]DeltaEntry, 0, 1000)
	c.deltaBufferMu.Unlock()

	// Write to delta Parquet file
	deltaFile := filepath.Join(c.localStoragePath, "hot",
		fmt.Sprintf("%s_delta_%d.parquet", c.tableName, time.Now().Unix()))

	// Create temporary table for delta entries
	tempTable := fmt.Sprintf("temp_delta_%d", time.Now().UnixNano())

	createTempSQL := fmt.Sprintf(`
		CREATE TEMPORARY TABLE %s (
			ticket_id VARCHAR,
			tenant_id INTEGER,
			status VARCHAR(50),
			priority VARCHAR(20),
			created_at TIMESTAMP,
			updated_at TIMESTAMP,
			operation VARCHAR(20)
		)
	`, tempTable)

	if _, err := c.duckDB.ExecContext(c.ctx, createTempSQL); err != nil {
		log.Printf("Failed to create temp table for delta: %v", err)
		return
	}

	// Insert delta entries
	for _, entry := range buffer {
		if entry.Data != nil {
			coreFields := c.extractCoreFields(entry.Data)
			insertSQL := fmt.Sprintf(`
				INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?)
			`, tempTable)

			c.duckDB.ExecContext(c.ctx, insertSQL,
				entry.Data.Id,
				coreFields["tenant_id"],
				coreFields["status"],
				coreFields["priority"],
				entry.Data.CreatedAt,
				entry.Data.UpdatedAt,
				entry.Operation,
			)
		}
	}

	// Write to Parquet
	exportSQL := fmt.Sprintf(`
		COPY %s TO '%s' (FORMAT PARQUET, COMPRESSION 'ZSTD')
	`, tempTable, deltaFile)

	if _, err := c.duckDB.ExecContext(c.ctx, exportSQL); err != nil {
		log.Printf("Failed to write delta Parquet: %v", err)
	} else {
		log.Printf("Flushed %d delta entries to %s", len(buffer), deltaFile)
	}

	// Drop temp table
	c.duckDB.ExecContext(c.ctx, fmt.Sprintf("DROP TABLE %s", tempTable))
}

// startDeltaFlusher runs periodic delta buffer flushes and responds to immediate flush requests
func (c *DuckDBCache) startDeltaFlusher(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Periodic flush
			c.flushDeltaBuffer()
		case <-c.deltaFlushChan:
			// Immediate flush requested due to buffer overflow
			c.flushDeltaBuffer()
		case <-c.ctx.Done():
			// Final flush before shutdown
			c.flushDeltaBuffer()
			return
		}
	}
}

// startCompactionWorker runs periodic compaction of delta files
func (c *DuckDBCache) startCompactionWorker(interval time.Duration) {
	c.compactionTicker = time.NewTicker(interval)
	defer c.compactionTicker.Stop()

	for {
		select {
		case <-c.compactionTicker.C:
			c.compactDeltaFiles()
		case <-c.ctx.Done():
			return
		}
	}
}

// compactDeltaFiles merges delta files into base Parquet
func (c *DuckDBCache) compactDeltaFiles() {
	log.Printf("Starting delta file compaction and tiering...")

	hotPath := filepath.Join(c.localStoragePath, "hot")
	warmPath := filepath.Join(c.localStoragePath, "warm")
	coldPath := filepath.Join(c.localStoragePath, "cold")

	basePath := filepath.Join(hotPath, fmt.Sprintf("%s_base.parquet", c.tableName))
	deltaPattern := filepath.Join(hotPath, fmt.Sprintf("%s_delta_*.parquet", c.tableName))

	// Step 1: Tier data based on age
	now := time.Now()

	// Move data older than 7 days to warm tier
	warmCutoff := now.AddDate(0, 0, -7)
	warmFile := filepath.Join(warmPath, fmt.Sprintf("%s_warm_%d.parquet", c.tableName, now.Unix()))

	warmSQL := fmt.Sprintf(`
		COPY (
			SELECT * FROM %s_core 
			WHERE created_at < '%s' AND created_at >= '%s'
			ORDER BY tenant_id, created_at
		) TO '%s' (FORMAT PARQUET, COMPRESSION 'ZSTD', ROW_GROUP_SIZE 100000)
	`, c.tableName, warmCutoff.Format(time.RFC3339), warmCutoff.AddDate(0, 0, -23).Format(time.RFC3339), warmFile)

	if _, err := c.duckDB.ExecContext(c.ctx, warmSQL); err == nil {
		// Remove warm data from hot tier
		deleteWarmSQL := fmt.Sprintf(`
			DELETE FROM %s_core WHERE created_at < '%s'
		`, c.tableName, warmCutoff.Format(time.RFC3339))
		c.duckDB.ExecContext(c.ctx, deleteWarmSQL)
		log.Printf("Moved data to warm tier: %s", warmFile)
	}

	// Move data older than 30 days to cold tier
	coldCutoff := now.AddDate(0, 0, -30)

	// Check warm tier files and move old ones to cold
	warmFiles, _ := filepath.Glob(filepath.Join(warmPath, "*.parquet"))
	for _, wf := range warmFiles {
		info, _ := os.Stat(wf)
		if info.ModTime().Before(coldCutoff) {
			// Move to cold storage
			newPath := filepath.Join(coldPath, filepath.Base(wf))
			os.Rename(wf, newPath)
			log.Printf("Moved warm file to cold tier: %s", newPath)
		}
	}

	// Step 2: Compact hot tier delta files
	exportSQL := fmt.Sprintf(`
		COPY (
			SELECT * FROM %s_core 
			WHERE created_at >= '%s'
			ORDER BY tenant_id, created_at
		) TO '%s' (FORMAT PARQUET, COMPRESSION 'ZSTD', ROW_GROUP_SIZE 50000)
	`, c.tableName, warmCutoff.Format(time.RFC3339), basePath)

	if _, err := c.duckDB.ExecContext(c.ctx, exportSQL); err != nil {
		log.Printf("Failed to compact to base Parquet: %v", err)
		return
	}

	// Remove old delta files
	files, _ := filepath.Glob(deltaPattern)
	for _, f := range files {
		os.Remove(f)
	}

	c.lastCompaction = time.Now()
	log.Printf("Compaction completed: merged %d delta files, tiered data across hot/warm/cold", len(files))
}

// subscribeToTicketEvents subscribes to NATS events for cache synchronization
func (c *DuckDBCache) subscribeToTicketEvents() error {
	if c.natsConn == nil {
		return fmt.Errorf("NATS connection not available")
	}

	// Subscribe to broadcast topic for cache updates
	_, err := c.natsConn.Subscribe("ticket.*.broadcast", func(msg *nats.Msg) {
		var ticketData ticketpb.TicketData
		if err := json.Unmarshal(msg.Data, &ticketData); err != nil {
			log.Printf("Failed to unmarshal ticket event: %v", err)
			return
		}

		// Update local cache
		if err := c.updateCacheFromTicket(&ticketData); err != nil {
			log.Printf("Failed to update cache from NATS event: %v", err)
		}
	})

	if err != nil {
		return fmt.Errorf("failed to subscribe to ticket events: %w", err)
	}

	log.Printf("Subscribed to NATS ticket events for cache synchronization")
	return nil
}

// publishCacheUpdateEvent publishes cache update event to NATS
func (c *DuckDBCache) publishCacheUpdateEvent(subject string, ticketData *ticketpb.TicketData) {
	if c.natsConn == nil {
		return
	}

	// Publish to broadcast topic for all instances
	broadcastSubject := subject + ".broadcast"

	data, err := json.Marshal(ticketData)
	if err != nil {
		log.Printf("Failed to marshal ticket for NATS: %v", err)
		return
	}

	if err := c.natsConn.Publish(broadcastSubject, data); err != nil {
		log.Printf("Failed to publish cache update event: %v", err)
	}

	// Also publish to queue group for Parquet writes (only one instance handles)
	queueSubject := subject + ".parquet"
	c.natsConn.Publish(queueSubject, data)
}

// Helper methods for cache operations
func (c *DuckDBCache) listFromCache() ([]*ticketpb.TicketData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	query := fmt.Sprintf(`
		SELECT ticket_id, tenant_id, status, priority, created_at, updated_at
		FROM %s_core
		ORDER BY created_at DESC
	`, c.tableName)

	rows, err := c.duckDB.QueryContext(c.ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tickets []*ticketpb.TicketData
	for rows.Next() {
		var ticketID, status, priority, createdAt, updatedAt string
		var tenantID int64

		if err := rows.Scan(&ticketID, &tenantID, &status, &priority, &createdAt, &updatedAt); err != nil {
			continue
		}

		ticketData := &ticketpb.TicketData{
			Id:        ticketID,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
			Fields: map[string]*ticketpb.FieldValue{
				"tenant_id": {Value: &ticketpb.FieldValue_IntValue{IntValue: tenantID}},
				"status":    {Value: &ticketpb.FieldValue_StringValue{StringValue: status}},
				"priority":  {Value: &ticketpb.FieldValue_StringValue{StringValue: priority}},
			},
		}

		tickets = append(tickets, ticketData)
	}

	return tickets, nil
}

func (c *DuckDBCache) searchInCache(request SearchRequest) ([]*ticketpb.TicketData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Map field names to their column families and build JOINs
	requiredTables := make(map[string]bool)
	requiredTables["core"] = true // Always need core table

	// Build WHERE clause with proper table aliases
	var whereParts []string
	var args []interface{}

	for _, condition := range request.Conditions {
		fieldFamily := c.getFieldFamily(condition.Operand)
		if fieldFamily == "" {
			log.Printf("Warning: Unknown field '%s', skipping condition", condition.Operand)
			continue
		}

		requiredTables[fieldFamily] = true
		tableAlias := fieldFamily[:1] // c, d, a, m, s for core, details, assignment, metadata, sla

		// Handle different field types
		var whereCondition string

		if fieldFamily == "metadata" && c.isCustomField(condition.Operand) {
			// Handle custom fields stored in JSON
			switch condition.Operator {
			case "eq":
				whereCondition = fmt.Sprintf("JSON_EXTRACT(%s.custom_fields, '$.%s') = ?", tableAlias, condition.Operand)
			case "ne":
				whereCondition = fmt.Sprintf("JSON_EXTRACT(%s.custom_fields, '$.%s') != ?", tableAlias, condition.Operand)
			case "like":
				whereCondition = fmt.Sprintf("JSON_EXTRACT(%s.custom_fields, '$.%s') LIKE ?", tableAlias, condition.Operand)
			case "gt":
				whereCondition = fmt.Sprintf("CAST(JSON_EXTRACT(%s.custom_fields, '$.%s') AS DOUBLE) > ?", tableAlias, condition.Operand)
			case "lt":
				whereCondition = fmt.Sprintf("CAST(JSON_EXTRACT(%s.custom_fields, '$.%s') AS DOUBLE) < ?", tableAlias, condition.Operand)
			default:
				whereCondition = fmt.Sprintf("JSON_EXTRACT(%s.custom_fields, '$.%s') = ?", tableAlias, condition.Operand)
			}
		} else {
			// Handle regular columns
			columnName := condition.Operand
			if condition.Operand == "name" && fieldFamily == "details" {
				columnName = "title" // Map 'name' to 'title' in details table
			}

			switch condition.Operator {
			case "eq":
				whereCondition = fmt.Sprintf("%s.%s = ?", tableAlias, columnName)
			case "ne":
				whereCondition = fmt.Sprintf("%s.%s != ?", tableAlias, columnName)
			case "like":
				whereCondition = fmt.Sprintf("%s.%s LIKE ?", tableAlias, columnName)
			case "gt":
				whereCondition = fmt.Sprintf("%s.%s > ?", tableAlias, columnName)
			case "lt":
				whereCondition = fmt.Sprintf("%s.%s < ?", tableAlias, columnName)
			default:
				whereCondition = fmt.Sprintf("%s.%s = ?", tableAlias, columnName)
			}
		}

		whereParts = append(whereParts, whereCondition)
		args = append(args, condition.Value)
	}

	// Build FROM clause with JOINs
	fromClause := fmt.Sprintf("%s_core c", c.tableName)

	if requiredTables["details"] {
		fromClause += fmt.Sprintf(" LEFT JOIN %s_details d ON c.ticket_id = d.ticket_id", c.tableName)
	}
	if requiredTables["assignment"] {
		fromClause += fmt.Sprintf(" LEFT JOIN %s_assignment a ON c.ticket_id = a.ticket_id", c.tableName)
	}
	if requiredTables["metadata"] {
		fromClause += fmt.Sprintf(" LEFT JOIN %s_metadata m ON c.ticket_id = m.ticket_id", c.tableName)
	}
	if requiredTables["sla"] {
		fromClause += fmt.Sprintf(" LEFT JOIN %s_sla s ON c.ticket_id = s.ticket_id", c.tableName)
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT c.ticket_id, c.tenant_id, c.status, c.priority, c.created_at, c.updated_at
		FROM %s
		%s
		ORDER BY c.created_at DESC
	`, fromClause, whereClause)

	rows, err := c.duckDB.QueryContext(c.ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w\nQuery: %s", err, query)
	}
	defer rows.Close()

	var tickets []*ticketpb.TicketData
	for rows.Next() {
		var ticketID, status, priority, createdAt, updatedAt string
		var tenantID int64

		if err := rows.Scan(&ticketID, &tenantID, &status, &priority, &createdAt, &updatedAt); err != nil {
			continue
		}

		ticketData := &ticketpb.TicketData{
			Id:        ticketID,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
			Fields: map[string]*ticketpb.FieldValue{
				"tenant_id": {Value: &ticketpb.FieldValue_IntValue{IntValue: tenantID}},
				"status":    {Value: &ticketpb.FieldValue_StringValue{StringValue: status}},
				"priority":  {Value: &ticketpb.FieldValue_StringValue{StringValue: priority}},
			},
		}

		tickets = append(tickets, ticketData)
	}

	return tickets, nil
}

func (c *DuckDBCache) searchInCacheWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	// Similar to searchInCache but only queries needed column families
	// based on request.ProjectedFields
	return c.searchInCache(request) // Simplified for now
}

// getFromTieredStorage searches warm and cold tier Parquet files
func (c *DuckDBCache) getFromTieredStorage(id string) (*ticketpb.TicketData, bool) {
	tiers := []string{"warm", "cold"}

	for _, tier := range tiers {
		tierPath := filepath.Join(c.localStoragePath, tier)
		pattern := filepath.Join(tierPath, "*.parquet")

		files, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}

		for _, file := range files {
			// Query this Parquet file directly
			query := fmt.Sprintf(`
				SELECT ticket_id, tenant_id, status, priority, created_at, updated_at
				FROM read_parquet('%s')
				WHERE ticket_id = ?
			`, file)

			row := c.duckDB.QueryRowContext(c.ctx, query, id)

			var ticketID, status, priority, createdAt, updatedAt string
			var tenantID int64

			err := row.Scan(&ticketID, &tenantID, &status, &priority, &createdAt, &updatedAt)
			if err == nil {
				// Found in this tier
				ticketData := &ticketpb.TicketData{
					Id:        ticketID,
					CreatedAt: createdAt,
					UpdatedAt: updatedAt,
					Fields: map[string]*ticketpb.FieldValue{
						"tenant_id": {Value: &ticketpb.FieldValue_IntValue{IntValue: tenantID}},
						"status":    {Value: &ticketpb.FieldValue_StringValue{StringValue: status}},
						"priority":  {Value: &ticketpb.FieldValue_StringValue{StringValue: priority}},
					},
				}
				return ticketData, true
			}
		}
	}

	return nil, false
}

func (c *DuckDBCache) extractCoreFields(ticketData *ticketpb.TicketData) map[string]interface{} {
	fields := map[string]interface{}{
		"tenant_id": int64(1),
		"status":    "New",
		"priority":  "Medium",
	}

	if tenantField, exists := ticketData.Fields["tenant_id"]; exists {
		if intVal := tenantField.GetIntValue(); intVal != 0 {
			fields["tenant_id"] = intVal
		}
	}

	if statusField, exists := ticketData.Fields["status"]; exists {
		if strVal := statusField.GetStringValue(); strVal != "" {
			fields["status"] = strVal
		}
	}

	if priorityField, exists := ticketData.Fields["priority"]; exists {
		if strVal := priorityField.GetStringValue(); strVal != "" {
			fields["priority"] = strVal
		}
	}

	return fields
}

func (c *DuckDBCache) extractDetailsFields(ticketData *ticketpb.TicketData) map[string]interface{} {
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

func (c *DuckDBCache) extractAssignmentFields(ticketData *ticketpb.TicketData) map[string]interface{} {
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

// getColumnFamilyMapping returns the mapping of fields to column families
func (c *DuckDBCache) getColumnFamilyMapping() map[string]string {
	return map[string]string{
		// Core fields
		"ticket_id":  "core",
		"tenant_id":  "core",
		"status":     "core",
		"priority":   "core",
		"created_at": "core",
		"updated_at": "core",

		// Details fields
		"title":       "details",
		"description": "details",
		"resolution":  "details",
		"name":        "details", // Map 'name' to title in details

		// Assignment fields
		"assigned_to":    "assignment",
		"assigned_group": "assignment",
		"assigned_at":    "assignment",
		"assignee_name":  "assignment",

		// Metadata fields
		"category":      "metadata",
		"subcategory":   "metadata",
		"tags":          "metadata",
		"custom_fields": "metadata",

		// SLA fields
		"sla_breach":        "sla",
		"due_date":          "sla",
		"response_due_at":   "sla",
		"resolution_due_at": "sla",

		// Common field aliases for backwards compatibility
		"lastviolationtime": "metadata", // Custom field in metadata
		"groupchangedtime":  "metadata", // Custom field in metadata
	}
}

// getFieldFamily returns the column family for a given field name
func (c *DuckDBCache) getFieldFamily(fieldName string) string {
	mapping := c.getColumnFamilyMapping()
	if family, exists := mapping[fieldName]; exists {
		return family
	}

	// Default to metadata for unknown fields (they might be custom fields)
	log.Printf("Field '%s' not in mapping, defaulting to metadata table", fieldName)
	return "metadata"
}

// isCustomField determines if a field should be stored in the JSON custom_fields column
func (c *DuckDBCache) isCustomField(fieldName string) bool {
	predefinedFields := map[string]bool{
		// Core fields
		"ticket_id": true, "tenant_id": true, "status": true, "priority": true,
		"created_at": true, "updated_at": true,
		// Details fields
		"title": true, "description": true, "resolution": true, "name": true,
		// Assignment fields
		"assigned_to": true, "assigned_group": true, "assigned_at": true, "assignee_name": true,
		// Metadata schema fields
		"category": true, "subcategory": true, "tags": true, "custom_fields": true,
		// SLA fields
		"sla_breach": true, "due_date": true, "response_due_at": true, "resolution_due_at": true,
	}

	// If it's not a predefined field, it's a custom field stored in JSON
	return !predefinedFields[fieldName]
}
