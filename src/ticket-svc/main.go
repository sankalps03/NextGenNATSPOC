package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Meta struct {
	EventId    string `json:"event_id"`
	Tenant     string `json:"tenant"`
	OccurredAt string `json:"occurred_at"`
	Schema     string `json:"schema"`
}

type Ticket struct {
	Id          string `json:"id"`
	Tenant      string `json:"tenant"`
	Title       string `json:"title"`
	Description string `json:"description"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type TicketEvent struct {
	Meta *Meta   `json:"meta"`
	Data *Ticket `json:"data"`
}

type Config struct {
	NATSUrl     string
	ServiceName string
	LogLevel    string
}

type NATSManager struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

type InMemoryStorage struct {
	mu      sync.RWMutex
	tenants map[string]map[string]*Ticket
}

type TicketService struct {
	natsManager *NATSManager
	storage     *InMemoryStorage
}

type CreateTicketRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	CreatedBy   string `json:"created_by"`
}

type UpdateTicketRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
}

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

func NewInMemoryStorage() *InMemoryStorage {
	return &InMemoryStorage{
		tenants: make(map[string]map[string]*Ticket),
	}
}

func (s *InMemoryStorage) ensureTenant(tenant string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tenants[tenant]; !exists {
		s.tenants[tenant] = make(map[string]*Ticket)
	}
}

func (s *InMemoryStorage) CreateTicket(tenant string, ticket *Ticket) {
	s.ensureTenant(tenant)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[tenant][ticket.Id] = ticket
}

func (s *InMemoryStorage) GetTicket(tenant, id string) (*Ticket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if tenantTickets, exists := s.tenants[tenant]; exists {
		ticket, found := tenantTickets[id]
		return ticket, found
	}
	return nil, false
}

func (s *InMemoryStorage) UpdateTicket(tenant string, ticket *Ticket) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tenantTickets, exists := s.tenants[tenant]; exists {
		if _, found := tenantTickets[ticket.Id]; found {
			tenantTickets[ticket.Id] = ticket
			return true
		}
	}
	return false
}

func (s *InMemoryStorage) DeleteTicket(tenant, id string) (*Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tenantTickets, exists := s.tenants[tenant]; exists {
		if ticket, found := tenantTickets[id]; found {
			delete(tenantTickets, id)
			return ticket, true
		}
	}
	return nil, false
}

func (s *InMemoryStorage) ListTickets(tenant string) map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var tickets []*Ticket
	response := make(map[string]interface{})
	if tenantTickets, exists := s.tenants[tenant]; exists {
		for _, ticket := range tenantTickets {
			tickets = append(tickets, ticket)
		}
	} else {
		tickets = []*Ticket{}
	}

	response["tickets"] = tickets
	response["tenant"] = tenant

	return response
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
	dataBytes, err := json.Marshal(req.Data)
	if err != nil {
		return nil, err
	}

	var createReq CreateTicketRequest
	if err := json.Unmarshal(dataBytes, &createReq); err != nil {
		return nil, err
	}

	if strings.TrimSpace(createReq.Title) == "" || strings.TrimSpace(createReq.CreatedBy) == "" {
		return ErrorResponse{
			Error:   "validation_failed",
			Message: "title and created_by are required and cannot be empty",
		}, nil
	}

	if len(createReq.Title) > 200 {
		return ErrorResponse{
			Error:   "validation_failed",
			Message: "title must be 200 characters or less",
		}, nil
	}

	if len(createReq.Description) > 2000 {
		return ErrorResponse{
			Error:   "validation_failed",
			Message: "description must be 2000 characters or less",
		}, nil
	}

	now := time.Now().Format(time.RFC3339)
	ticket := &Ticket{
		Id:          uuid.New().String(),
		Tenant:      req.Tenant,
		Title:       createReq.Title,
		Description: createReq.Description,
		CreatedBy:   createReq.CreatedBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	ts.storage.CreateTicket(req.Tenant, ticket)

	if err := ts.publishTicketCreated(context.Background(), req.Tenant, ticket); err != nil {
		log.Printf("ERROR: Failed to publish ticket created event for tenant=%s ticket_id=%s: %v", req.Tenant, ticket.Id, err)
	}

	if containsSeverity(createReq.Description) {
		if err := ts.publishNotificationRequested(context.Background(), req.Tenant, ticket); err != nil {
			log.Printf("ERROR: Failed to publish notification requested event for tenant=%s ticket_id=%s: %v", req.Tenant, ticket.Id, err)
		}
	}

	return ticket, nil
}

func (ts *TicketService) handleListTickets(req ServiceRequest) (interface{}, error) {
	return ts.storage.ListTickets(req.Tenant), nil
}

func (ts *TicketService) handleGetTicket(req ServiceRequest) (interface{}, error) {
	ticket, found := ts.storage.GetTicket(req.Tenant, req.TicketID)
	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
	}

	if err := ts.publishTicketRead(context.Background(), req.Tenant, ticket); err != nil {
		log.Printf("ERROR: Failed to publish ticket read event for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
	}

	return ticket, nil
}

func (ts *TicketService) handleUpdateTicket(req ServiceRequest) (interface{}, error) {
	ticket, found := ts.storage.GetTicket(req.Tenant, req.TicketID)
	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
	}

	dataBytes, err := json.Marshal(req.Data)
	if err != nil {
		return nil, err
	}

	var updateReq UpdateTicketRequest
	if err := json.Unmarshal(dataBytes, &updateReq); err != nil {
		return nil, err
	}

	updated := false

	if updateReq.Title != nil && *updateReq.Title != ticket.Title {
		if len(*updateReq.Title) > 200 {
			return ErrorResponse{
				Error:   "validation_failed",
				Message: "title must be 200 characters or less",
			}, nil
		}
		ticket.Title = *updateReq.Title
		updated = true
	}

	if updateReq.Description != nil && *updateReq.Description != ticket.Description {
		if len(*updateReq.Description) > 2000 {
			return ErrorResponse{
				Error:   "validation_failed",
				Message: "description must be 2000 characters or less",
			}, nil
		}
		ticket.Description = *updateReq.Description
		updated = true
	}

	if updated {
		ticket.UpdatedAt = time.Now().Format(time.RFC3339)
		ts.storage.UpdateTicket(req.Tenant, ticket)

		if err := ts.publishTicketUpdated(context.Background(), req.Tenant, ticket); err != nil {
			log.Printf("ERROR: Failed to publish ticket updated event for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
		}

		if containsSeverity(ticket.Description) {
			if err := ts.publishNotificationRequested(context.Background(), req.Tenant, ticket); err != nil {
				log.Printf("ERROR: Failed to publish notification requested event for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
			}
		}
	}

	return ticket, nil
}

func (ts *TicketService) handleDeleteTicket(req ServiceRequest) (interface{}, error) {
	ticket, found := ts.storage.DeleteTicket(req.Tenant, req.TicketID)
	if !found {
		return ErrorResponse{Error: "ticket_not_found"}, nil
	}

	if err := ts.publishTicketDeleted(context.Background(), req.Tenant, ticket); err != nil {
		log.Printf("ERROR: Failed to publish ticket deleted event for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
	}

	return map[string]string{"status": "deleted"}, nil
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

func (ts *TicketService) publishTicketCreated(ctx context.Context, tenant string, ticket *Ticket) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.create@v1",
		},
		Data: ticket,
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

func (ts *TicketService) publishTicketUpdated(ctx context.Context, tenant string, ticket *Ticket) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.update@v1",
		},
		Data: ticket,
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

func (ts *TicketService) publishTicketDeleted(ctx context.Context, tenant string, ticket *Ticket) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.delete@v1",
		},
		Data: ticket,
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

func (ts *TicketService) publishTicketRead(ctx context.Context, tenant string, ticket *Ticket) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "ticket.read@v1",
		},
		Data: ticket,
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

func (ts *TicketService) publishNotificationRequested(ctx context.Context, tenant string, ticket *Ticket) error {
	event := &TicketEvent{
		Meta: &Meta{
			EventId:    uuid.New().String(),
			Tenant:     tenant,
			OccurredAt: time.Now().Format(time.RFC3339),
			Schema:     "notification.event@v1",
		},
		Data: ticket,
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
		NATSUrl:     getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"),
		ServiceName: getEnv("SERVICE_NAME", "ticket-service"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
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

	storage := NewInMemoryStorage()

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

	if natsManager != nil && natsManager.conn != nil {
		natsManager.conn.Close()
		log.Println("NATS connection closed")
	}

	log.Println("Service exited")
}
