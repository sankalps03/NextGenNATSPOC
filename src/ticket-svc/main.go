package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/platform/ticket-svc/cache"
	storage2 "github.com/platform/ticket-svc/storage"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

type Meta struct {
	EventId    string `json:"event_id"`
	OccurredAt string `json:"occurred_at"`
	Schema     string `json:"schema"`
}

type TicketEvent struct {
	Meta *Meta                `json:"meta"`
	Data *ticketpb.TicketData `json:"data"`
}

type Config struct {
	NATSUrl           string
	ServiceName       string
	LogLevel          string
	DynamoDBTable     string
	DynamoDBURL       string // DynamoDB endpoint URL (for local development)
	DynamoDBAddress   string // DynamoDB address (alternative to URL)
	AWSRegion         string
	StorageType       string // "dynamodb", "opensearch", "postgresql", "postgresql-eav", "postgresql-hstore", "postgresql-dynamic", "scylladb", or "mongodb"
	StorageMode       string // "fixed" or "dynamic" (for DynamoDB schema)
	OpenSearchURL     string // OpenSearch endpoint URL
	OpenSearchIndex   string // OpenSearch index name
	PostgreSQLURL     string // PostgreSQL connection string
	PostgreSQLTable   string // PostgreSQL base table name
	ScyllaDBHosts     string // ScyllaDB hosts (comma-separated)
	ScyllaDBKeyspace  string // ScyllaDB keyspace name
	ScyllaDBTable     string // ScyllaDB base table name
	MongoDBURL        string // MongoDB connection string
	MongoDBDatabase   string // MongoDB database name
	MongoDBCollection string // MongoDB base collection name
	MongoDBUsername   string // MongoDB username (optional)
	MongoDBPassword   string // MongoDB password (optional)
	InteractivePrompt bool   // Enable interactive storage selection
	// DragonFly Cache Configuration
	DragonflyURL      string // DragonFly cache URL
	DragonflyPassword string // DragonFly cache password
	DragonflyDB       int    // DragonFly cache database number
	CacheEnabled      bool   // Enable caching
	CacheTTL          int    // Cache TTL in seconds
}

type NATSManager struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

type TicketService struct {
	natsManager *NATSManager
	storage     storage2.TicketStorage
	kvStore     jetstream.KeyValue
	objStore    jetstream.ObjectStore
	cache       *cache.DragonflyCache
	config      *Config
}

// Removed hardcoded request structs - now using dynamic field handling

type ServiceRequest struct {
	Action   string      `json:"action"`
	TicketID string      `json:"ticket_id,omitempty"`
	Data     interface{} `json:"data,omitempty"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// ResponseWithLatency wraps responses with database latency information
type ResponseWithLatency struct {
	Data            interface{} `json:"data"`
	DatabaseLatency string      `json:"database_latency_ms"`
}

// convertToFieldValue converts a Go value to protobuf FieldValue
func convertToFieldValue(value interface{}) (*ticketpb.FieldValue, error) {
	switch v := value.(type) {
	case string:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: v},
		}, nil
	case int:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
		}, nil
	case int64:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_IntValue{IntValue: v},
		}, nil
	case float64:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: v},
		}, nil
	case bool:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_BoolValue{BoolValue: v},
		}, nil
	case []string:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringArray{
				StringArray: &ticketpb.StringArray{Values: v},
			},
		}, nil
	case []interface{}:
		// Convert interface slice to string slice
		var stringArray []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				stringArray = append(stringArray, str)
			} else {
				stringArray = append(stringArray, fmt.Sprintf("%v", item))
			}
		}
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringArray{
				StringArray: &ticketpb.StringArray{Values: stringArray},
			},
		}, nil
	default:
		// Convert unknown types to string
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: fmt.Sprintf("%v", v)},
		}, nil
	}
}

// convertMapToFields converts a map[string]interface{} to protobuf fields
func convertMapToFields(data map[string]interface{}) (map[string]*ticketpb.FieldValue, error) {
	fields := make(map[string]*ticketpb.FieldValue)

	for key, value := range data {
		fieldValue, err := convertToFieldValue(value)
		if err != nil {
			return nil, fmt.Errorf("failed to convert field %s: %w", key, err)
		}
		fields[key] = fieldValue
	}

	return fields, nil
}

// convertFieldValueToInterface converts a protobuf FieldValue back to Go interface{}
func convertFieldValueToInterface(fieldValue *ticketpb.FieldValue) interface{} {
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

// ticketToJSON converts a protobuf TicketData to a JSON-friendly map
func ticketToJSON(ticket *ticketpb.TicketData) map[string]interface{} {
	result := map[string]interface{}{
		"id":         ticket.Id,
		"created_at": ticket.CreatedAt,
		"updated_at": ticket.UpdatedAt,
	}

	// Convert protobuf fields to simple JSON values
	for fieldName, fieldValue := range ticket.Fields {
		result[fieldName] = convertFieldValueToInterface(fieldValue)
	}

	delete(result, "fields")

	return result
}

// storeInObjectStore stores data in NATS object store and returns object ID
func (ts *TicketService) storeInObjectStore(data interface{}, prefix string) (string, int64, error) {
	// Serialize data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal data: %w", err)
	}

	// Generate unique object ID
	objectID := fmt.Sprintf("%s-%s-%s", prefix, time.Now().Format("20060102-150405"), uuid.New().String()[:8])

	// Store in object store
	/*objectMeta := jetstream.ObjectMeta{
		Name:        objectID,
		Description: fmt.Sprintf("Response for %s at %s", prefix, time.Now().Format(time.RFC3339)),
	}
	*/
	objInfo, err := ts.objStore.PutBytes(context.Background(), objectID, jsonData)
	if err != nil {
		return "", 0, fmt.Errorf("failed to store in object store: %w", err)
	}

	return objectID, int64(objInfo.Size), nil
}

// fieldsEqual compares two FieldValue instances for equality
func fieldsEqual(a, b *ticketpb.FieldValue) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	switch aVal := a.Value.(type) {
	case *ticketpb.FieldValue_StringValue:
		if bVal, ok := b.Value.(*ticketpb.FieldValue_StringValue); ok {
			return aVal.StringValue == bVal.StringValue
		}
	case *ticketpb.FieldValue_IntValue:
		if bVal, ok := b.Value.(*ticketpb.FieldValue_IntValue); ok {
			return aVal.IntValue == bVal.IntValue
		}
	case *ticketpb.FieldValue_DoubleValue:
		if bVal, ok := b.Value.(*ticketpb.FieldValue_DoubleValue); ok {
			return aVal.DoubleValue == bVal.DoubleValue
		}
	case *ticketpb.FieldValue_BoolValue:
		if bVal, ok := b.Value.(*ticketpb.FieldValue_BoolValue); ok {
			return aVal.BoolValue == bVal.BoolValue
		}
	case *ticketpb.FieldValue_BytesValue:
		if bVal, ok := b.Value.(*ticketpb.FieldValue_BytesValue); ok {
			return string(aVal.BytesValue) == string(bVal.BytesValue)
		}
	case *ticketpb.FieldValue_StringArray:
		if bVal, ok := b.Value.(*ticketpb.FieldValue_StringArray); ok {
			if len(aVal.StringArray.Values) != len(bVal.StringArray.Values) {
				return false
			}
			for i, v := range aVal.StringArray.Values {
				if v != bVal.StringArray.Values[i] {
					return false
				}
			}
			return true
		}
	}
	return false
}

func (ts *TicketService) handleServiceRequest(msg *nats.Msg) {
	var req ServiceRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		log.Printf("Failed to unmarshal service request: %v", err)
		errorResp := ErrorResponse{Error: "invalid_request", Message: err.Error()}
		if respData, err := json.Marshal(errorResp); err == nil {
			msg.Respond(respData)
		}
		return
	}

	var response interface{}
	var err error

	switch req.Action {
	case "create":
		response, err = ts.handleCreateTicket(req)
	case "list":
		response, err = ts.handleListTickets(req)
	case "get":
		response, err = ts.handleGetTicket(req)
	case "update":
		response, err = ts.handleUpdateTicket(req)
	case "delete":
		response, err = ts.handleDeleteTicket(req)
	case "search":
		response, err = ts.handleSearchTickets(req)
	default:
		response = ErrorResponse{Error: "unknown_action", Message: "Unknown action: " + req.Action}
	}

	if err != nil {
		response = ErrorResponse{Error: "internal_error", Message: err.Error()}
	}

	if respData, err := json.Marshal(response); err == nil {
		msg.Respond(respData)
	} else {
		log.Printf("Failed to marshal response: %v", err)
		errorResp := ErrorResponse{Error: "response_error"}
		if respData, err := json.Marshal(errorResp); err == nil {
			msg.Respond(respData)
		}
	}
}

func (ts *TicketService) handleCreateTicket(req ServiceRequest) (interface{}, error) {
	// Convert req.Data to map[string]interface{} for dynamic field handling
	var fieldData map[string]interface{}

	dataBytes, err := json.Marshal(req.Data)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(dataBytes, &fieldData); err != nil {
		return nil, err
	}

	// Convert dynamic fields to protobuf FieldValue types
	fields, err := convertMapToFields(fieldData)
	if err != nil {
		return nil, fmt.Errorf("failed to convert fields: %w", err)
	}

	now := time.Now().Format(time.RFC3339)

	// Create protobuf TicketData with dynamic fields
	ticketData := &ticketpb.TicketData{
		Id:        uuid.New().String(),
		CreatedAt: now,
		UpdatedAt: now,
		Fields:    fields,
	}

	// Measure database latency
	dbStart := time.Now()

	var kvDocs map[string]interface{}

	if err, kvDocs = ts.storage.CreateTicket(ticketData); err != nil {
		return nil, fmt.Errorf("failed to create ticket: %w", err)
	}
	dbLatency := time.Since(dbStart)

	// Invalidate relevant caches after successful creation
	if ts.config.CacheEnabled && ts.cache != nil {
		// Invalidate ticket list cache since a new ticket was added
		if err := ts.cache.InvalidateTicketList(context.Background()); err != nil {
			log.Printf("Warning: Failed to invalidate ticket list cache: %v", err)
		}

		// Invalidate search result caches since they may now be outdated
		if err := ts.cache.InvalidateSearchPatterns(context.Background(), "*"); err != nil {
			log.Printf("Warning: Failed to invalidate search caches: %v", err)
		}

		log.Printf("Invalidated caches after creating ticket %s", ticketData.Id)
	}

	// Store ticket document in KV store if using OpenSearch storage
	if ts.kvStore != nil {
		kvKey := ticketData.Id

		// Serialize the entire ticket document as JSON
		ticketDoc, err := json.Marshal(kvDocs)
		if err != nil {
			log.Printf("WARNING: Failed to marshal ticket for KV store: %v", err)
		} else {
			if _, err := ts.kvStore.Put(context.Background(), kvKey, ticketDoc); err != nil {
				log.Printf("WARNING: Failed to store ticket document in KV store: %v", err)
			} else {
				log.Printf("Stored ticket document in KV store: %s", kvKey)
			}
		}
	}

	return ResponseWithLatency{
		Data:            ticketToJSON(ticketData),
		DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
	}, nil
}

func (ts *TicketService) handleListTickets(req ServiceRequest) (interface{}, error) {
	var tickets []*ticketpb.TicketData
	var err error
	var dbLatency time.Duration
	cacheHit := false

	// Try cache first if enabled
	if ts.config.CacheEnabled && ts.cache != nil {
		cacheStart := time.Now()
		tickets, cacheHit = ts.cache.GetTicketList(context.Background())
		if cacheHit {
			dbLatency = time.Since(cacheStart)
			log.Printf("Cache HIT for ticket list")
		} else {
			log.Printf("Cache MISS for ticket list")
		}
	}

	// If not found in cache, get from database
	if !cacheHit {
		dbStart := time.Now()
		tickets, err = ts.storage.ListTickets(ts.kvStore)
		dbLatency = time.Since(dbStart)

		if err != nil {
			return nil, fmt.Errorf("failed to list tickets: %w", err)
		}

		// Store in cache for future requests
		if ts.config.CacheEnabled && ts.cache != nil {
			ttl := time.Duration(ts.config.CacheTTL) * time.Second
			if err := ts.cache.SetTicketList(context.Background(), tickets, ttl); err != nil {
				log.Printf("Warning: Failed to cache ticket list: %v", err)
			}
		}
	}

	// Convert protobuf tickets to JSON
	var jsonTickets []map[string]interface{}
	for _, ticket := range tickets {
		jsonTickets = append(jsonTickets, ticketToJSON(ticket))
	}

	// Prepare response data
	responseData := map[string]interface{}{
		"result":      jsonTickets,
		"total_count": len(tickets),
		"cache_hit":   cacheHit,
	}

	// Store in object store if available
	if ts.objStore != nil {
		objectID, size, err := ts.storeInObjectStore(responseData, "list-tickets")
		if err != nil {
			log.Printf("Failed to store in object store: %v", err)
			// Fallback to direct response if object store fails
			return ResponseWithLatency{
				Data:            responseData,
				DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
			}, nil
		}

		// Return object store reference
		return ResponseWithLatency{
			Data: map[string]interface{}{
				"object_id":   objectID,
				"object_size": size,
				"total_count": len(tickets),
				"expires_at":  time.Now().Add(30 * time.Minute).Format(time.RFC3339),
				"type":        "ticket_list",
			},
			DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
		}, nil
	}

	// Fallback to direct response if object store not available
	return ResponseWithLatency{
		Data:            responseData,
		DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
	}, nil
}

func (ts *TicketService) handleGetTicket(req ServiceRequest) (interface{}, error) {
	var ticketData *ticketpb.TicketData
	var found bool
	var dbLatency time.Duration
	cacheHit := false

	// Try cache first if enabled
	if ts.config.CacheEnabled && ts.cache != nil {
		cacheStart := time.Now()
		ticketData, found = ts.cache.GetTicket(context.Background(), req.TicketID)
		if found {
			cacheHit = true
			dbLatency = time.Since(cacheStart)
			log.Printf("Cache HIT for ticket %s", req.TicketID)
		} else {
			log.Printf("Cache MISS for ticket %s", req.TicketID)
		}
	}

	// If not found in cache, get from database
	if !found {
		dbStart := time.Now()
		ticketData, found = ts.storage.GetTicket(req.TicketID, ts.kvStore)
		dbLatency = time.Since(dbStart)

		// Store in cache for future requests
		if found && ts.config.CacheEnabled && ts.cache != nil {
			ttl := time.Duration(ts.config.CacheTTL) * time.Second
			if err := ts.cache.SetTicket(context.Background(), ticketData, ttl); err != nil {
				log.Printf("Warning: Failed to cache ticket %s: %v", req.TicketID, err)
			}
		}
	}

	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
	}

	// Convert to JSON
	jsonTicket := ticketToJSON(ticketData)

	responseData := map[string]interface{}{
		"result":      jsonTicket,
		"total_count": 1,
		"cache_hit":   cacheHit,
	}

	// Store in object store if available
	if ts.objStore != nil {
		objectID, size, err := ts.storeInObjectStore(responseData, fmt.Sprintf("ticket-%s", req.TicketID))
		if err != nil {
			log.Printf("Failed to store in object store: %v", err)
			// Fallback to direct response if object store fails
			return ResponseWithLatency{
				Data:            jsonTicket,
				DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
			}, nil
		}

		// Return object store reference
		return ResponseWithLatency{
			Data: map[string]interface{}{
				"object_id":   objectID,
				"object_size": size,
				"expires_at":  time.Now().Add(30 * time.Minute).Format(time.RFC3339),
				"type":        "ticket",
			},
			DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
		}, nil
	}

	// Fallback to direct response if object store not available
	return ResponseWithLatency{
		Data:            jsonTicket,
		DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
	}, nil
}

func (ts *TicketService) handleUpdateTicket(req ServiceRequest) (interface{}, error) {
	// Measure database latency for get operation
	dbStart := time.Now()
	ticketData, found := ts.storage.GetTicket(req.TicketID, ts.kvStore)
	getLatency := time.Since(dbStart)

	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
	}

	// Convert req.Data to map[string]interface{} for dynamic field handling
	var updateData map[string]interface{}

	dataBytes, err := json.Marshal(req.Data)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(dataBytes, &updateData); err != nil {
		return nil, err
	}

	updated := false

	// Update fields dynamically
	for fieldName, newValue := range updateData {
		// Convert new value to protobuf FieldValue
		newFieldValue, err := convertToFieldValue(newValue)
		if err != nil {
			return nil, fmt.Errorf("failed to convert field %s: %w", fieldName, err)
		}

		// Check if field value has actually changed
		currentFieldValue, exists := ticketData.Fields[fieldName]
		if !exists || !fieldsEqual(currentFieldValue, newFieldValue) {
			ticketData.Fields[fieldName] = newFieldValue
			updated = true
		}
	}

	var updateLatency time.Duration
	if updated {
		ticketData.UpdatedAt = time.Now().Format(time.RFC3339)

		// Measure database latency for update operation
		updateStart := time.Now()
		ts.storage.UpdateTicket(ticketData)
		updateLatency = time.Since(updateStart)

		// Invalidate caches after successful update
		if ts.config.CacheEnabled && ts.cache != nil {
			// Remove the specific ticket from cache
			if err := ts.cache.DeleteTicket(context.Background(), req.TicketID); err != nil {
				log.Printf("Warning: Failed to invalidate ticket cache for %s: %v", req.TicketID, err)
			}

			// Invalidate ticket list cache since ticket data changed
			if err := ts.cache.InvalidateTicketList(context.Background()); err != nil {
				log.Printf("Warning: Failed to invalidate ticket list cache: %v", err)
			}

			// Invalidate search result caches since they may now be outdated
			if err := ts.cache.InvalidateSearchPatterns(context.Background(), "*"); err != nil {
				log.Printf("Warning: Failed to invalidate search caches: %v", err)
			}

			log.Printf("Invalidated caches after updating ticket %s", req.TicketID)
		}
	}

	// Total database latency includes both get and update operations
	totalDbLatency := getLatency + updateLatency

	return ResponseWithLatency{
		Data:            ticketToJSON(ticketData),
		DatabaseLatency: fmt.Sprintf("%.2f", float64(totalDbLatency.Nanoseconds())/1000000),
	}, nil
}

func (ts *TicketService) handleDeleteTicket(req ServiceRequest) (interface{}, error) {
	// Measure database latency
	dbStart := time.Now()
	_, found := ts.storage.DeleteTicket(req.TicketID)
	dbLatency := time.Since(dbStart)

	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
	}

	// Invalidate caches after successful deletion
	if ts.config.CacheEnabled && ts.cache != nil {
		// Remove the specific ticket from cache
		if err := ts.cache.DeleteTicket(context.Background(), req.TicketID); err != nil {
			log.Printf("Warning: Failed to delete ticket from cache for %s: %v", req.TicketID, err)
		}

		// Invalidate ticket list cache since a ticket was removed
		if err := ts.cache.InvalidateTicketList(context.Background()); err != nil {
			log.Printf("Warning: Failed to invalidate ticket list cache: %v", err)
		}

		// Invalidate search result caches since they may now be outdated
		if err := ts.cache.InvalidateSearchPatterns(context.Background(), "*"); err != nil {
			log.Printf("Warning: Failed to invalidate search caches: %v", err)
		}

		log.Printf("Invalidated caches after deleting ticket %s", req.TicketID)
	}

	return ResponseWithLatency{
		Data:            map[string]string{"status": "deleted"},
		DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
	}, nil
}

func (ts *TicketService) handleSearchTickets(req ServiceRequest) (interface{}, error) {
	// Parse search request from request data (supports both old and new format)
	var searchRequest storage2.SearchRequest

	// Convert req.Data to search request
	dataBytes, err := json.Marshal(req.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request data: %w", err)
	}

	if err := json.Unmarshal(dataBytes, &searchRequest); err != nil {
		return nil, fmt.Errorf("failed to parse search request: %w", err)
	}

	var tickets []*ticketpb.TicketData
	var dbLatency time.Duration
	cacheHit := false

	// Generate cache key for search
	var searchKey string
	if ts.config.CacheEnabled && ts.cache != nil {
		searchKey = ts.cache.GenerateSearchKey(searchRequest.Conditions, searchRequest.ProjectedFields, searchRequest.SortFields)

		// Try cache first
		cacheStart := time.Now()
		tickets, cacheHit = ts.cache.GetSearchResults(context.Background(), searchKey)
		if cacheHit {
			dbLatency = time.Since(cacheStart)
			log.Printf("Cache HIT for search key %s", searchKey)
		} else {
			log.Printf("Cache MISS for search key %s", searchKey)
		}
	}

	// If not found in cache, search database
	if !cacheHit {
		dbStart := time.Now()

		// Use projection-aware search if projected fields are specified
		if len(searchRequest.ProjectedFields) > 0 {
			tickets, err = ts.storage.SearchTicketsWithProjection(searchRequest)
			log.Printf("Using projection-aware search with %d projected fields", len(searchRequest.ProjectedFields))
		} else {
			// Fallback to original search for backward compatibility
			tickets, err = ts.storage.SearchTickets(searchRequest)
			log.Printf("Using standard search (no projection)")
		}
		dbLatency = time.Since(dbStart)

		if err != nil {
			return nil, fmt.Errorf("failed to search tickets: %w", err)
		}

		// Store search results in cache
		if ts.config.CacheEnabled && ts.cache != nil {
			// Use shorter TTL for search results as they change more frequently
			searchTTL := time.Duration(ts.config.CacheTTL/2) * time.Second
			if err := ts.cache.SetSearchResults(context.Background(), searchKey, tickets, searchTTL); err != nil {
				log.Printf("Warning: Failed to cache search results: %v", err)
			}
		}
	}

	// Convert protobuf tickets to response format
	var responseTickets []map[string]interface{}
	for _, ticket := range tickets {
		ticketMap := make(map[string]interface{})
		ticketMap["id"] = ticket.Id
		ticketMap["created_at"] = ticket.CreatedAt
		ticketMap["updated_at"] = ticket.UpdatedAt

		// Add dynamic fields
		for fieldName, fieldValue := range ticket.Fields {
			ticketMap[fieldName] = convertFieldValueToInterface(fieldValue)
		}

		if ts.kvStore != nil {

			entries, err := ts.kvStore.Get(context.Background(), ticket.Id)

			if err == nil && entries != nil {

				if searchRequest.ProjectedFields == nil {

					json.Unmarshal(entries.Value(), &ticketMap)

				} else {

					var docs map[string]interface{}

					json.Unmarshal(entries.Value(), &docs)

					counters := map[string]struct{}{}

					for _, c := range searchRequest.ProjectedFields {

						counters[c] = struct{}{}
					}

					for key, value := range docs {

						if _, ok := counters[key]; ok {

							ticketMap[key] = value
						}

					}
				}
			}
		}

		delete(ticketMap, "fields")

		responseTickets = append(responseTickets, ticketMap)
	}

	// Prepare response data
	responseData := map[string]interface{}{
		"result":      responseTickets,
		"total_count": len(tickets),
		"cache_hit":   cacheHit,
	}

	// Store in object store if available
	if ts.objStore != nil {
		objectID, size, err := ts.storeInObjectStore(responseData, "search-tickets")
		if err != nil {
			log.Printf("Failed to store in object store: %v", err)
			// Fallback to direct response if object store fails
			return ResponseWithLatency{
				Data:            responseData,
				DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
			}, nil
		}

		// Return object store reference
		return ResponseWithLatency{
			Data: map[string]interface{}{
				"object_id":   objectID,
				"object_size": size,
				"total_count": len(tickets),
				"expires_at":  time.Now().Add(30 * time.Minute).Format(time.RFC3339),
				"type":        "search_results",
			},
			DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
		}, nil
	}

	// Fallback to direct response if object store not available
	return ResponseWithLatency{
		Data:            responseData,
		DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
	}, nil
}

func connectNATS(urls string) (*NATSManager, error) {
	serverList := strings.Split(urls, ",")
	for i, url := range serverList {
		serverList[i] = strings.TrimSpace(url)
	}

	opts := []nats.Option{
		nats.Name("ticket-service"),
		nats.ReconnectWait(time.Second),
		nats.MaxReconnects(10),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			log.Printf("NATS disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("NATS reconnected to %v", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			log.Printf("NATS connection closed")
		}),
	}

	conn, err := nats.Connect(strings.Join(serverList, ","), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS cluster: %w", err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	log.Printf("Successfully connected to NATS cluster: %v (active: %s)", serverList, conn.ConnectedUrl())
	return &NATSManager{conn: conn, js: js}, nil
}

func (nm *NATSManager) PublishEvent(ctx context.Context, subject string, payload []byte, headers map[string]string) error {
	msg := &nats.Msg{
		Subject: subject,
		Data:    payload,
		Header:  make(nats.Header),
	}

	for k, v := range headers {
		msg.Header.Set(k, v)
	}

	_, err := nm.js.PublishMsg(ctx, msg)
	return err
}

func (ts *TicketService) publishTicketCreated(ctx context.Context, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.create@v1",
		},
		Data: ticketData,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	subject := "ticket.create"
	headers := map[string]string{
		"schema":       "ticket.create@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishTicketUpdated(ctx context.Context, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.update@v1",
		},
		Data: ticketData,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	subject := "ticket.update"
	headers := map[string]string{
		"schema":       "ticket.update@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishTicketDeleted(ctx context.Context, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.delete@v1",
		},
		Data: ticketData,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	subject := "ticket.delete"
	headers := map[string]string{
		"schema":       "ticket.delete@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishTicketRead(ctx context.Context, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.read@v1",
		},
		Data: ticketData,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	subject := "ticket.read"
	headers := map[string]string{
		"schema":       "ticket.read@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishNotificationRequested(ctx context.Context, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "notification.event@v1",
		},
		Data: ticketData,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	subject := "notification.event"
	headers := map[string]string{
		"schema":       "notification.event@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishTicketSearched(ctx context.Context, resultCount int, conditions []storage2.SearchCondition) error {

	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.event.searched@v1",
		},
		Data: &ticketpb.TicketData{
			Id:        "search-" + uuid.New().String(),
			CreatedAt: time.Now().Format(time.RFC3339),
			UpdatedAt: time.Now().Format(time.RFC3339),
			Fields: map[string]*ticketpb.FieldValue{
				"search_result_count": {
					Value: &ticketpb.FieldValue_IntValue{IntValue: int64(resultCount)},
				},
				"search_conditions": {
					Value: &ticketpb.FieldValue_StringValue{StringValue: fmt.Sprintf("%+v", conditions)},
				},
			},
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal ticket searched event: %w", err)
	}

	subject := "ticket.event.searched"
	headers := map[string]string{
		"schema":       "ticket.event.searched@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func loadConfig() *Config {
	cacheTTL := 300 // Default 5 minutes
	if ttlStr := getEnv("CACHE_TTL", "300"); ttlStr != "" {
		if parsedTTL, err := strconv.Atoi(ttlStr); err == nil {
			cacheTTL = parsedTTL
		}
	}

	dragonflyDB := 0 // Default database 0
	if dbStr := getEnv("DRAGONFLY_DB", "0"); dbStr != "" {
		if parsedDB, err := strconv.Atoi(dbStr); err == nil {
			dragonflyDB = parsedDB
		}
	}

	return &Config{
		NATSUrl:           getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"),
		ServiceName:       getEnv("SERVICE_NAME", "ticket-service"),
		LogLevel:          getEnv("LOG_LEVEL", "info"),
		DynamoDBTable:     getEnv("DYNAMODB_TABLE", "tickets"),
		DynamoDBURL:       getEnv("DYNAMODB_URL", ""),
		DynamoDBAddress:   getEnv("DYNAMODB_ADDRESS", ""),
		AWSRegion:         getEnv("AWS_REGION", "us-east-1"),
		StorageType:       getEnv("STORAGE_TYPE", ""),
		StorageMode:       getEnv("STORAGE_MODE", "dynamic"),
		OpenSearchURL:     getEnv("OPENSEARCH_URL", "http://localhost:9200"),
		OpenSearchIndex:   getEnv("OPENSEARCH_INDEX", "tickets"),
		PostgreSQLURL:     getEnv("POSTGRESQL_URL", "postgres://postgres:postgres123@localhost/tickets_db?sslmode=disable"),
		PostgreSQLTable:   getEnv("POSTGRESQL_TABLE", "tickets"),
		ScyllaDBHosts:     getEnv("SCYLLADB_HOSTS", "localhost:9042"),
		ScyllaDBKeyspace:  getEnv("SCYLLADB_KEYSPACE", "ticket_management"),
		ScyllaDBTable:     getEnv("SCYLLADB_TABLE", "tickets"),
		MongoDBURL:        getEnv("MONGODB_URL", "mongodb://localhost:27017"),
		MongoDBDatabase:   getEnv("MONGODB_DATABASE", "tickets"),
		MongoDBCollection: getEnv("MONGODB_COLLECTION", "tickets"),
		MongoDBUsername:   getEnv("MONGODB_USERNAME", ""),
		MongoDBPassword:   getEnv("MONGODB_PASSWORD", ""),
		InteractivePrompt: getEnv("INTERACTIVE_PROMPT", "false") == "true",
		// DragonFly Cache Configuration
		DragonflyURL:      getEnv("DRAGONFLY_URL", "localhost:6379"),
		DragonflyPassword: getEnv("DRAGONFLY_PASSWORD", ""),
		DragonflyDB:       dragonflyDB,
		CacheEnabled:      getEnv("CACHE_ENABLED", "true") == "true",
		CacheTTL:          cacheTTL,
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// buildMongoDBConnectionString constructs a MongoDB connection string with authentication if provided
func buildMongoDBConnectionString(config *Config) string {
	baseURL := config.MongoDBURL
	username := config.MongoDBUsername
	password := config.MongoDBPassword

	// If no username/password provided, return the base URL as-is
	if username == "" || password == "" {
		return baseURL
	}

	// Parse the base URL to inject credentials
	if strings.HasPrefix(baseURL, "mongodb://") {
		// Remove mongodb:// prefix
		urlWithoutPrefix := strings.TrimPrefix(baseURL, "mongodb://")

		// Check if URL already contains credentials
		if strings.Contains(urlWithoutPrefix, "@") {
			log.Printf("WARNING: MongoDB URL already contains credentials, using provided URL as-is")
			return baseURL
		}

		// Build URL with credentials: mongodb://username:password@host:port/database
		return fmt.Sprintf("mongodb://%s:%s@%s", username, password, urlWithoutPrefix)
	} else if strings.HasPrefix(baseURL, "mongodb+srv://") {
		// Remove mongodb+srv:// prefix
		urlWithoutPrefix := strings.TrimPrefix(baseURL, "mongodb+srv://")

		// Check if URL already contains credentials
		if strings.Contains(urlWithoutPrefix, "@") {
			log.Printf("WARNING: MongoDB URL already contains credentials, using provided URL as-is")
			return baseURL
		}

		// Build URL with credentials: mongodb+srv://username:password@host/database
		return fmt.Sprintf("mongodb+srv://%s:%s@%s", username, password, urlWithoutPrefix)
	}

	// If URL format is not recognized, return as-is with warning
	log.Printf("WARNING: Unrecognized MongoDB URL format, using provided URL as-is")
	return baseURL
}

// maskConnectionString masks sensitive information in PostgreSQL connection string for logging
func maskConnectionString(connectionString string) string {
	// Simple masking - replace password with ***
	if strings.Contains(connectionString, "password=") {
		parts := strings.Split(connectionString, " ")
		for i, part := range parts {
			if strings.HasPrefix(part, "password=") {
				parts[i] = "password=***"
			}
		}
		return strings.Join(parts, " ")
	}

	// Handle URL format: postgres://user:password@host/db
	if strings.HasPrefix(connectionString, "postgres://") {
		// Find the password part
		if atIndex := strings.Index(connectionString, "@"); atIndex != -1 {
			beforeAt := connectionString[:atIndex]
			afterAt := connectionString[atIndex:]

			if colonIndex := strings.LastIndex(beforeAt, ":"); colonIndex != -1 {
				// Replace password with ***
				return beforeAt[:colonIndex+1] + "***" + afterAt
			}
		}
	}

	return connectionString
}

func createKVBucket(natsManager *NATSManager, bucketName string) (jetstream.KeyValue, error) {
	kv, err := natsManager.js.CreateKeyValue(context.Background(), jetstream.KeyValueConfig{
		Bucket: bucketName,
	})
	if err != nil {
		// If bucket already exists, try to get it
		kv, err = natsManager.js.KeyValue(context.Background(), bucketName)
		if err != nil {
			return nil, fmt.Errorf("failed to create or get KV bucket '%s': %w", bucketName, err)
		}
	}

	log.Printf("NATS KV bucket '%s' ready", bucketName)
	return kv, nil
}

func createObjectStore(natsManager *NATSManager, storeName string) (jetstream.ObjectStore, error) {
	objStore, err := natsManager.js.CreateObjectStore(context.Background(), jetstream.ObjectStoreConfig{
		Bucket: storeName,
	})
	if err != nil {
		// If object store already exists, try to get it
		objStore, err = natsManager.js.ObjectStore(context.Background(), storeName)
		if err != nil {
			return nil, fmt.Errorf("failed to create or get object store '%s': %w", storeName, err)
		}
	}

	log.Printf("NATS Object Store '%s' ready", storeName)
	return objStore, nil
}

func promptForStorageType() string {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\nSelect storage backend:")
		fmt.Println("1. DynamoDB")
		fmt.Println("2. OpenSearch")
		fmt.Println("3. PostgreSQL")
		fmt.Println("4. PostgreSQL EAV")
		fmt.Println("5. PostgreSQL Hstore")
		fmt.Println("6. PostgreSQL Dynamic Columns")
		fmt.Println("7. ScyllaDB")
		fmt.Println("8. MongoDB")
		fmt.Print("Enter your choice (1-8): ")

		input, err := reader.ReadString('\n')
		if err != nil {
			log.Printf("Error reading input: %v", err)
			continue
		}

		choice := strings.TrimSpace(input)
		switch choice {
		case "1":
			fmt.Println("Selected: DynamoDB")
			return "dynamodb"
		case "2":
			fmt.Println("Selected: OpenSearch")
			return "opensearch"
		case "3":
			fmt.Println("Selected: PostgreSQL")
			return "postgresql"
		case "4":
			fmt.Println("Selected: PostgreSQL EAV")
			return "postgresql-eav"
		case "5":
			fmt.Println("Selected: PostgreSQL Hstore")
			return "postgresql-hstore"
		case "6":
			fmt.Println("Selected: PostgreSQL Dynamic Columns")
			return "postgresql-dynamic"
		case "7":
			fmt.Println("Selected: ScyllaDB")
			return "scylladb"
		case "8":
			fmt.Println("Selected: MongoDB")
			return "mongodb"
		default:
			fmt.Println("Invalid choice. Please enter 1-8.")
		}
	}
}

func main() {
	fmt.Println("Starting Ticket Service...")
	config := loadConfig()

	natsManager, err := connectNATS(config.NATSUrl)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}

	// Initialize storage based on configuration
	var storage storage2.TicketStorage
	var storageType string

	// Determine storage type
	if config.StorageType == "" && config.InteractivePrompt {
		// Interactive prompt for storage selection
		storageType = promptForStorageType()
	} else if config.StorageType != "" {
		storageType = config.StorageType
	} else {
		// Default to PostgreSQL if no selection
		storageType = "mongodb"
	}

	// Initialize selected storage
	var kvStore jetstream.KeyValue

	// Get or create object store for retrieving ticket responses
	objStore, err := createObjectStore(natsManager, "ticket-responses")
	if err != nil {
		log.Printf("WARNING: Failed to create object store: %v. Large responses may not be available.", err)
		objStore = nil // Set to nil so service can continue without object store
	}

	switch storageType {
	case "opensearch":
		opensearchStorage, err := storage2.NewOpenSearchStorage(context.Background(), config.OpenSearchURL, config.OpenSearchIndex)
		if err != nil {
			log.Fatalf("Failed to initialize OpenSearch storage: %v", err)

			return
		}
		storage = opensearchStorage

		// Create KV store bucket for OpenSearch storage only
		kvStore, err = createKVBucket(natsManager, "ticket-kv")
		if err != nil {
			log.Fatalf("Failed to create KV bucket for OpenSearch storage: %v", err)

			return
		}

		log.Printf("Using OpenSearch storage with endpoint: %s and index: %s", config.OpenSearchURL, config.OpenSearchIndex)
		log.Printf("Created NATS KV bucket: ticket-kv")
	case "postgresql":
		postgresStorage, err := storage2.NewPostgreSQLStorage(context.Background(), config.PostgreSQLTable, config.PostgreSQLURL)
		if err != nil {
			log.Fatalf("Failed to initialize PostgreSQL storage: %v", err)

			return
		}
		storage = postgresStorage
		log.Printf("Using PostgreSQL storage with connection: %s and base table: %s",
			maskConnectionString(config.PostgreSQLURL), config.PostgreSQLTable)
	case "postgresql-eav":
		postgresEAVStorage, err := storage2.NewPostgreSQLEAVStorage(context.Background(), config.PostgreSQLTable, config.PostgreSQLURL)
		if err != nil {
			log.Fatalf("Failed to initialize PostgreSQL EAV storage: %v", err)

			return
		}
		storage = postgresEAVStorage
		log.Printf("Using PostgreSQL EAV storage with connection: %s and base table: %s",
			maskConnectionString(config.PostgreSQLURL), config.PostgreSQLTable)
	case "postgresql-hstore":
		postgresHstoreStorage, err := storage2.NewPostgreSQLHstoreStorage(context.Background(), config.PostgreSQLTable, config.PostgreSQLURL)
		if err != nil {
			log.Fatalf("Failed to initialize PostgreSQL Hstore storage: %v", err)

			return
		}
		storage = postgresHstoreStorage
		log.Printf("Using PostgreSQL Hstore storage with connection: %s and base table: %s",
			maskConnectionString(config.PostgreSQLURL), config.PostgreSQLTable)
	case "postgresql-dynamic":
		postgresDynamicStorage, err := storage2.NewPostgreSQLDynamicStorage(context.Background(), config.PostgreSQLTable, config.PostgreSQLURL)
		if err != nil {
			log.Fatalf("Failed to initialize PostgreSQL Dynamic Columns storage: %v", err)

			return
		}
		storage = postgresDynamicStorage
		log.Printf("Using PostgreSQL Dynamic Columns storage with connection: %s and base table: %s",
			maskConnectionString(config.PostgreSQLURL), config.PostgreSQLTable)
		log.Printf("Field mappings loaded into memory for high-performance operations")
	case "scylladb":
		// Parse hosts from comma-separated string
		hosts := strings.Split(config.ScyllaDBHosts, ",")
		for i, host := range hosts {
			hosts[i] = strings.TrimSpace(host)
		}

		scyllaStorage, err := storage2.NewScyllaDBStorage(context.Background(), hosts, config.ScyllaDBKeyspace, config.ScyllaDBTable)
		if err != nil {
			log.Fatalf("Failed to initialize ScyllaDB storage: %v", err)

			return
		}
		storage = scyllaStorage
		log.Printf("Using ScyllaDB storage with hosts: %v, keyspace: %s, base table: %s",
			hosts, config.ScyllaDBKeyspace, config.ScyllaDBTable)
	case "mongodb":
		// Build MongoDB connection string with authentication if provided
		mongoURL := buildMongoDBConnectionString(config)

		mongoStorage, err := storage2.NewMongoDBStorage(context.Background(), config.MongoDBCollection, mongoURL, config.MongoDBDatabase)
		if err != nil {
			log.Fatalf("Failed to initialize MongoDB storage: %v", err)

			return
		}
		storage = mongoStorage
		log.Printf("Using MongoDB storage with connection: %s, database: %s, base collection: %s",
			maskConnectionString(mongoURL), config.MongoDBDatabase, config.MongoDBCollection)
	default:
		log.Fatalf("Unknown storage type: %s", storageType)
	}

	// Initialize DragonFly cache if enabled
	var dragonflyCache *cache.DragonflyCache
	if config.CacheEnabled {
		var err error
		dragonflyCache, err = cache.NewDragonflyCache(config.DragonflyURL, config.DragonflyPassword, config.DragonflyDB)
		if err != nil {
			log.Printf("WARNING: Failed to initialize DragonFly cache: %v. Continuing without cache.", err)
			config.CacheEnabled = false // Disable caching for this session
		} else {
			log.Printf("DragonFly cache initialized successfully. TTL: %d seconds", config.CacheTTL)
		}
	}

	service := &TicketService{
		natsManager: natsManager,
		storage:     storage,
		kvStore:     kvStore,
		objStore:    objStore,
		cache:       dragonflyCache,
		config:      config,
	}

	sub, err := natsManager.conn.Subscribe("ticket.service", service.handleServiceRequest)
	if err != nil {
		log.Fatalf("Failed to subscribe to ticket.service: %v", err)
	}
	defer sub.Unsubscribe()

	log.Printf("Ticket Service listening on subject: ticket.service")

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down service...")

	// Close storage connection
	if storage != nil {
		if err := storage.Close(); err != nil {
			log.Printf("Error closing storage: %v", err)
		}
	}

	// Close DragonFly cache connection
	if dragonflyCache != nil {
		if err := dragonflyCache.Close(); err != nil {
			log.Printf("Error closing DragonFly cache: %v", err)
		} else {
			log.Println("DragonFly cache connection closed")
		}
	}

	if natsManager != nil && natsManager.conn != nil {
		natsManager.conn.Close()
		log.Println("NATS connection closed")
	}

	log.Println("Service exited")
}
