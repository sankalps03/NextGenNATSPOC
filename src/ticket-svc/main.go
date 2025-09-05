package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	storage2 "github.com/platform/ticket-svc/storage"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

type Meta struct {
	EventId    string `json:"event_id"`
	Tenant     string `json:"tenant"`
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
	StorageType       string // "dynamodb" or "opensearch"
	StorageMode       string // "fixed" or "dynamic" (for DynamoDB schema)
	OpenSearchURL     string // OpenSearch endpoint URL
	OpenSearchIndex   string // OpenSearch index name
	InteractivePrompt bool   // Enable interactive storage selection
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
}

// Removed hardcoded request structs - now using dynamic field handling

type ServiceRequest struct {
	Action   string      `json:"action"`
	Tenant   string      `json:"tenant"`
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
		"tenant":     ticket.Tenant,
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
		Tenant:    req.Tenant,
		CreatedAt: now,
		UpdatedAt: now,
		Fields:    fields,
	}

	// Measure database latency
	dbStart := time.Now()

	var kvDocs map[string]interface{}

	if err, kvDocs = ts.storage.CreateTicket(req.Tenant, ticketData); err != nil {
		return nil, fmt.Errorf("failed to create ticket: %w", err)
	}
	dbLatency := time.Since(dbStart)

	// Store ticket document in KV store if using OpenSearch storage
	if ts.kvStore != nil {
		kvKey := fmt.Sprintf("%s-%s", req.Tenant, ticketData.Id)

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
	// Measure database latency
	dbStart := time.Now()
	tickets, err := ts.storage.ListTickets(req.Tenant, ts.kvStore)
	dbLatency := time.Since(dbStart)

	if err != nil {
		return nil, fmt.Errorf("failed to list tickets: %w", err)
	}

	// Convert protobuf tickets to JSON
	var jsonTickets []map[string]interface{}
	for _, ticket := range tickets {
		jsonTickets = append(jsonTickets, ticketToJSON(ticket))
	}

	// Prepare response data
	responseData := map[string]interface{}{
		"result":      jsonTickets,
		"tenant":      req.Tenant,
		"total_count": len(tickets),
	}

	// Store in object store if available
	if ts.objStore != nil {
		objectID, size, err := ts.storeInObjectStore(responseData, fmt.Sprintf("list-%s", req.Tenant))
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
	// Measure database latency
	dbStart := time.Now()
	ticketData, found := ts.storage.GetTicket(req.Tenant, req.TicketID, ts.kvStore)
	dbLatency := time.Since(dbStart)

	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
	}

	// Convert to JSON
	jsonTicket := ticketToJSON(ticketData)

	responseData := map[string]interface{}{
		"result":      jsonTicket,
		"tenant":      req.Tenant,
		"total_count": 1,
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
	ticketData, found := ts.storage.GetTicket(req.Tenant, req.TicketID, ts.kvStore)
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
		ts.storage.UpdateTicket(req.Tenant, ticketData)
		updateLatency = time.Since(updateStart)
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
	_, found := ts.storage.DeleteTicket(req.Tenant, req.TicketID)
	dbLatency := time.Since(dbStart)

	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
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

	// Measure database latency
	dbStart := time.Now()
	var tickets []*ticketpb.TicketData

	// Use projection-aware search if projected fields are specified
	if len(searchRequest.ProjectedFields) > 0 {
		tickets, err = ts.storage.SearchTicketsWithProjection(req.Tenant, searchRequest)
		log.Printf("Using projection-aware search with %d projected fields", len(searchRequest.ProjectedFields))
	} else {
		// Fallback to original search for backward compatibility
		tickets, err = ts.storage.SearchTickets(req.Tenant, searchRequest)
		log.Printf("Using standard search (no projection)")
	}
	dbLatency := time.Since(dbStart)

	if err != nil {
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}

	// Convert protobuf tickets to response format
	var responseTickets []map[string]interface{}
	for _, ticket := range tickets {
		ticketMap := make(map[string]interface{})
		ticketMap["id"] = ticket.Id
		ticketMap["tenant"] = ticket.Tenant
		ticketMap["created_at"] = ticket.CreatedAt
		ticketMap["updated_at"] = ticket.UpdatedAt

		// Add dynamic fields
		for fieldName, fieldValue := range ticket.Fields {
			ticketMap[fieldName] = convertFieldValueToInterface(fieldValue)
		}

		if ts.kvStore != nil {

			entries, err := ts.kvStore.Get(context.Background(), fmt.Sprintf("%s-%s", ticket.Tenant, ticket.Id))

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
		"tenant":      req.Tenant,
		"total_count": len(tickets),
	}

	// Store in object store if available
	if ts.objStore != nil {
		objectID, size, err := ts.storeInObjectStore(responseData, fmt.Sprintf("search-%s", req.Tenant))
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

func (ts *TicketService) publishTicketCreated(ctx context.Context, tenant string, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
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
		"tenant":       tenant,
		"schema":       "ticket.create@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishTicketUpdated(ctx context.Context, tenant string, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
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
		"tenant":       tenant,
		"schema":       "ticket.update@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishTicketDeleted(ctx context.Context, tenant string, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
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
		"tenant":       tenant,
		"schema":       "ticket.delete@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishTicketRead(ctx context.Context, tenant string, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
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
		"tenant":       tenant,
		"schema":       "ticket.read@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishNotificationRequested(ctx context.Context, tenant string, ticketData *ticketpb.TicketData) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
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
		"tenant":       tenant,
		"schema":       "notification.event@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func (ts *TicketService) publishTicketSearched(ctx context.Context, tenant string, resultCount int, conditions []storage2.SearchCondition) error {

	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.event.searched@v1",
		},
		Data: &ticketpb.TicketData{
			Id:        "search-" + uuid.New().String(),
			Tenant:    tenant,
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

	subject := fmt.Sprintf("%s.ticket.ticket.event.searched", tenant)
	headers := map[string]string{
		"tenant":       tenant,
		"schema":       "ticket.event.searched@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
	}

	return ts.natsManager.PublishEvent(ctx, subject, payload, headers)
}

func loadConfig() *Config {
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
		InteractivePrompt: getEnv("INTERACTIVE_PROMPT", "false") == "true",
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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

func createObjectStore(natsManager *NATSManager, bucketName string) (jetstream.ObjectStore, error) {
	objStore, err := natsManager.js.CreateObjectStore(context.Background(), jetstream.ObjectStoreConfig{
		Bucket:      bucketName,
		Description: "Store for large ticket responses",
		TTL:         30 * time.Minute, // 30 minutes TTL
	})
	if err != nil {
		// If object store already exists, try to get it
		objStore, err = natsManager.js.ObjectStore(context.Background(), bucketName)
		if err != nil {
			return nil, fmt.Errorf("failed to create or get object store '%s': %w", bucketName, err)
		}
	}

	log.Printf("NATS Object Store '%s' ready with 30 minute TTL", bucketName)
	return objStore, nil
}

func promptForStorageType() string {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\nSelect storage backend:")
		fmt.Println("1. DynamoDB")
		fmt.Println("2. OpenSearch")
		fmt.Print("Enter your choice (1 or 2): ")

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
		default:
			fmt.Println("Invalid choice. Please enter 1 or 2.")
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
		// Default to DynamoDB if no selection
		storageType = "dynamodb"
	}

	// Initialize selected storage
	var kvStore jetstream.KeyValue
	var objStore jetstream.ObjectStore

	// Always create object store for large responses
	objStore, err = createObjectStore(natsManager, "ticket-responses")
	if err != nil {
		log.Printf("WARNING: Failed to create object store: %v. Large responses will use fallback.", err)
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
	case "dynamodb":
		dynamoStorage, err := storage2.NewDynamoDBStorage(context.Background(), config.DynamoDBTable, config.AWSRegion, config.DynamoDBURL, config.DynamoDBAddress)
		if err != nil {
			log.Fatalf("Failed to initialize DynamoDB storage: %v", err)

			return
		}
		storage = dynamoStorage
		log.Printf("Using DynamoDB dynamic storage (protobuf) with table: %s in region: %s", config.DynamoDBTable, config.AWSRegion)
	default:
		log.Fatalf("Unknown storage type: %s", storageType)
	}

	service := &TicketService{
		natsManager: natsManager,
		storage:     storage,
		kvStore:     kvStore,
		objStore:    objStore,
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

	if natsManager != nil && natsManager.conn != nil {
		natsManager.conn.Close()
		log.Println("NATS connection closed")
	}

	log.Println("Service exited")
}
