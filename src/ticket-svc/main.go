package main

import (
	"context"
	"encoding/json"
	"fmt"
	storage2 "github.com/platform/ticket-svc/storage"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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
	NATSUrl         string
	ServiceName     string
	LogLevel        string
	DynamoDBTable   string
	DynamoDBURL     string // DynamoDB endpoint URL (for local development)
	DynamoDBAddress string // DynamoDB address (alternative to URL)
	AWSRegion       string
	StorageType     string // "dynamodb" or "dynamodb-dynamic"
	StorageMode     string // "fixed" or "dynamic" (for DynamoDB schema)
}

type NATSManager struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

type TicketService struct {
	natsManager *NATSManager
	storage     storage2.TicketStorage
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

// checkForSeverityInFields checks if any field contains severity keywords
func (ts *TicketService) checkForSeverityInFields(fields map[string]*ticketpb.FieldValue) bool {
	for _, fieldValue := range fields {
		if stringVal := fieldValue.GetStringValue(); stringVal != "" {
			if containsSeverity(stringVal) {
				return true
			}
		}
	}
	return false
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
	if err := ts.storage.CreateTicket(req.Tenant, ticketData); err != nil {
		return nil, fmt.Errorf("failed to create ticket: %w", err)
	}
	dbLatency := time.Since(dbStart)

	if err := ts.publishTicketCreated(context.Background(), req.Tenant, ticketData); err != nil {
		log.Printf("ERROR: Failed to publish ticket created event for tenant=%s ticket_id=%s: %v", req.Tenant, ticketData.Id, err)
	}

	// Check for severity in any description-like field for notification
	if ts.checkForSeverityInFields(fields) {
		if err := ts.publishNotificationRequested(context.Background(), req.Tenant, ticketData); err != nil {
			log.Printf("ERROR: Failed to publish notification requested event for tenant=%s ticket_id=%s: %v", req.Tenant, ticketData.Id, err)
		}
	}

	return ResponseWithLatency{
		Data:            ticketData,
		DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
	}, nil
}

func (ts *TicketService) handleListTickets(req ServiceRequest) (interface{}, error) {
	// Measure database latency
	dbStart := time.Now()
	tickets, err := ts.storage.ListTickets(req.Tenant)
	dbLatency := time.Since(dbStart)

	if err != nil {
		return nil, fmt.Errorf("failed to list tickets: %w", err)
	}

	return ResponseWithLatency{
		Data: map[string]interface{}{
			"tickets": tickets,
			"tenant":  req.Tenant,
		},
		DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
	}, nil
}

func (ts *TicketService) handleGetTicket(req ServiceRequest) (interface{}, error) {
	// Measure database latency
	dbStart := time.Now()
	ticketData, found := ts.storage.GetTicket(req.Tenant, req.TicketID)
	dbLatency := time.Since(dbStart)

	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
	}

	if err := ts.publishTicketRead(context.Background(), req.Tenant, ticketData); err != nil {
		log.Printf("ERROR: Failed to publish ticket read event for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
	}

	return ResponseWithLatency{
		Data:            ticketData,
		DatabaseLatency: fmt.Sprintf("%.2f", float64(dbLatency.Nanoseconds())/1000000),
	}, nil
}

func (ts *TicketService) handleUpdateTicket(req ServiceRequest) (interface{}, error) {
	// Measure database latency for get operation
	dbStart := time.Now()
	ticketData, found := ts.storage.GetTicket(req.Tenant, req.TicketID)
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

		if err := ts.publishTicketUpdated(context.Background(), req.Tenant, ticketData); err != nil {
			log.Printf("ERROR: Failed to publish ticket updated event for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
		}

		if ts.checkForSeverityInFields(ticketData.Fields) {
			if err := ts.publishNotificationRequested(context.Background(), req.Tenant, ticketData); err != nil {
				log.Printf("ERROR: Failed to publish notification requested event for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
			}
		}
	}

	// Total database latency includes both get and update operations
	totalDbLatency := getLatency + updateLatency

	return ResponseWithLatency{
		Data:            ticketData,
		DatabaseLatency: fmt.Sprintf("%.2f", float64(totalDbLatency.Nanoseconds())/1000000),
	}, nil
}

func (ts *TicketService) handleDeleteTicket(req ServiceRequest) (interface{}, error) {
	// Measure database latency
	dbStart := time.Now()
	ticketData, found := ts.storage.DeleteTicket(req.Tenant, req.TicketID)
	dbLatency := time.Since(dbStart)

	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
	}

	if err := ts.publishTicketDeleted(context.Background(), req.Tenant, ticketData); err != nil {
		log.Printf("ERROR: Failed to publish ticket deleted event for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
	}

	return ResponseWithLatency{
		Data:            map[string]string{"status": "deleted"},
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

func containsSeverity(description string) bool {
	lower := strings.ToLower(description)
	return strings.Contains(lower, "critical") || strings.Contains(lower, "major")
}

func loadConfig() *Config {
	return &Config{
		NATSUrl:         getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"),
		ServiceName:     getEnv("SERVICE_NAME", "ticket-service"),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		DynamoDBTable:   getEnv("DYNAMODB_TABLE", "tickets"),
		DynamoDBURL:     getEnv("DYNAMODB_URL", ""),
		DynamoDBAddress: getEnv("DYNAMODB_ADDRESS", ""),
		AWSRegion:       getEnv("AWS_REGION", "us-east-1"),
		StorageType:     getEnv("STORAGE_TYPE", "dynamodb-dynamic"),
		StorageMode:     getEnv("STORAGE_MODE", "dynamic"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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

	dynamoStorage, err := storage2.NewDynamoDBStorage(context.Background(), config.DynamoDBTable, config.AWSRegion, config.DynamoDBURL, config.DynamoDBAddress)
	if err != nil {
		log.Fatalf("Failed to initialize DynamoDB dynamic storage: %v", err)
	}
	storage = dynamoStorage
	log.Printf("Using DynamoDB dynamic storage (protobuf) with table: %s in region: %s", config.DynamoDBTable, config.AWSRegion)

	service := &TicketService{
		natsManager: natsManager,
		storage:     storage,
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
