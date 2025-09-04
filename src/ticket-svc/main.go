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
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

type Meta struct {
	EventId    string `json:"event_id"`
	Tenant     string `json:"tenant"`
	OccurredAt string `json:"occurred_at"`
	Schema     string `json:"schema"`
}

type Ticket struct {
	// Core Fields
	Id          string `json:"id"`
	Tenant      string `json:"tenant"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Name        string `json:"name"`
	Subject     string `json:"subject"`

	// Status & Priority
	Status            string `json:"status"`
	StatusId          string `json:"statusid"`
	StatusChangedTime string `json:"statuschangedtime"`
	Priority          string `json:"priority"`
	PriorityId        string `json:"priorityid"`
	Urgency           string `json:"urgency"`
	UrgencyId         string `json:"urgencyid"`
	Impact            string `json:"impact"`
	ImpactId          string `json:"impactid"`

	// User Fields
	CreatedBy    string `json:"created_by"`
	CreatedById  string `json:"createdbyid"`
	UpdatedById  string `json:"updatedbyid"`
	Requester    string `json:"requester"`
	RequesterId  string `json:"requesterid"`
	Assignee     string `json:"assignee"`
	TechnicianId string `json:"technicianid"`
	ClosedBy     string `json:"closedby"`
	ResolvedBy   string `json:"resolvedby"`

	// Time Fields
	CreatedAt            string `json:"created_at"`
	CreatedTime          string `json:"createdtime"`
	CreatedDate          string `json:"createddate"`
	UpdatedAt            string `json:"updated_at"`
	UpdatedTime          string `json:"updatedtime"`
	LastUpdatedDate      string `json:"lastupdateddate"`
	DueBy                string `json:"dueby"`
	FirstResponseDueBy   string `json:"firstresponsedueby"`
	ResolvedDate         string `json:"resolveddate"`
	ClosedDate           string `json:"closeddate"`
	LastApprovalDate     string `json:"lastapprovaldate"`
	LastApprovedDate     string `json:"lastapproveddate"`
	LastClosedTime       string `json:"lastclosedtime"`
	LastOpenedTime       string `json:"lastopenedtime"`
	LastResolvedTime     string `json:"lastresolvedtime"`
	LastViolationTime    string `json:"lastviolationtime"`
	OldDueBy             string `json:"olddueby"`
	OldResponseDue       string `json:"oldresponsedue"`
	ResponseDue          string `json:"responsedue"`
	AskFeedbackDate      string `json:"askfeedbackdate"`
	FirstFeedbackDate    string `json:"firstfeedbackdate"`
	GroupChangedTime     string `json:"groupchangedtime"`
	LastOlaViolationTime string `json:"lastolaviolationtime"`
	OlaDueBy             string `json:"oladueby"`
	OldOlaDueBy          string `json:"oldoladueby"`
	LastUcViolationTime  string `json:"lastucviolationtime"`
	OldUcDueBy           string `json:"olducdueby"`
	UcDueBy              string `json:"ucdueby"`
	RemovedTime          string `json:"removedtime"`

	// Organization Fields
	Company         string `json:"company"`
	CompanyId       string `json:"companyid"`
	Location        string `json:"location"`
	LocationId      string `json:"locationid"`
	Department      string `json:"department"`
	DepartmentId    string `json:"departmentid"`
	TechnicianGroup string `json:"techniciangroup"`
	GroupId         string `json:"groupid"`
	VendorId        string `json:"vendorid"`

	// Category & Service
	Category            string `json:"category"`
	CategoryId          string `json:"categoryid"`
	ServiceCategory     string `json:"servicecategory"`
	ServiceCatalog      string `json:"servicecatalog"`
	ServiceCatalogId    string `json:"servicecatalogid"`
	SuggestedCategoryId string `json:"suggestedcategoryid"`
	SuggestedGroupId    string `json:"suggestedgroupid"`

	// Approval Fields
	ApprovalStatus string `json:"approvalstatus"`
	ApprovalType   string `json:"approvaltype"`

	// Source & Type
	Source      string `json:"source"`
	SourceId    string `json:"sourceid"`
	Type        string `json:"type"`
	RequestType string `json:"requesttype"`
	OobType     string `json:"oobtype"`

	// Support & SLA
	SupportLevel             string `json:"supportlevel"`
	FirstResponseTime        string `json:"firstresponsetime"`
	ResolutionDueLevel       string `json:"resolutionduelevel"`
	ResolutionEscalationTime string `json:"resolutionescalationtime"`
	ResponseDueLevel         string `json:"responseduelevel"`
	ResponseDueViolated      string `json:"responsedueviolated"`
	ResponseEscalationTime   string `json:"responseescalationtime"`
	SlaViolated              string `json:"slaviolated"`
	ViolatedSlaId            string `json:"violatedslaid"`
	OlaDueLevel              string `json:"oladuelevel"`
	OlaEscalationTime        string `json:"olaescalationtime"`
	OlaViolated              string `json:"olaviolated"`
	UcDueLevel               string `json:"ucduelevel"`
	UcEscalationTime         string `json:"ucescalationtime"`
	UcViolated               string `json:"ucviolated"`
	ViolatedUcId             string `json:"violateducid"`

	// Duration Fields
	TotalOnHoldDuration   string `json:"totalonholdduration"`
	TotalResolutionTime   string `json:"totalresolutiontime"`
	TotalSlaPauseTime     string `json:"totalslapausetime"`
	TotalWorkingTime      string `json:"totalworkingtime"`
	TotalUcOnHoldDuration string `json:"totaluconholdduration"`
	TotalUcPauseTime      string `json:"totalucpausetime"`
	TotalUcWorkingTime    string `json:"totalucworkingtime"`
	TotalUcResolutionTime string `json:"totalucresolutiontime"`

	// Misc Fields
	Tags                   string `json:"tags"`
	Removed                string `json:"removed"`
	RemovedById            string `json:"removedbyid"`
	DueTimeManuallyUpdated string `json:"duetimemanuallyupdated"`
	Reopened               string `json:"reopened"`
	OriginalDescription    string `json:"originaldescription"`
	TemplateId             string `json:"templateid"`
	CallFrom               string `json:"callfrom"`
	EmailReadConfigEmail   string `json:"emailreadconfigemail"`
	EmailReadConfigId      string `json:"emailreadconfigid"`
	PurchaseRequest        string `json:"purchaserequest"`
	Spam                   string `json:"spam"`
	VipRequest             string `json:"viprequest"`
	TransitionModelId      string `json:"transitionmodelid"`
	Migrated               string `json:"migrated"`
	MergedRequest          string `json:"mergedrequest"`
	MessengerConfigId      string `json:"messengerconfigid"`
}

type TicketEvent struct {
	Meta *Meta   `json:"meta"`
	Data *Ticket `json:"data"`
}

type Config struct {
	NATSUrl            string
	ServiceName        string
	LogLevel           string
	OpenSearchURL      string
	OpenSearchUsername string
	OpenSearchPassword string
	OpenSearchIndex    string
}

type NATSManager struct {
	conn      *nats.Conn
	js        jetstream.JetStream
	kvBuckets map[string]jetstream.KeyValue
	mu        sync.RWMutex
}

type NATSStorage struct {
	natsManager *NATSManager
}

type TicketService struct {
	natsManager          *NATSManager
	storage              *NATSStorage
	opensearchClient     *opensearch.Client
	config               *Config
	tenantConsumers      map[string]jetstream.Consumer
	tenantConsumersMutex sync.RWMutex
	shutdownCh           chan struct{}
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

type SearchRequest struct {
	Query   string            `json:"query"`
	Filters map[string]string `json:"filters,omitempty"`
	Limit   int               `json:"limit,omitempty"`
	Offset  int               `json:"offset,omitempty"`
}

type SearchResponse struct {
	Tickets []*Ticket `json:"tickets"`
	Total   int64     `json:"total"`
	TookMs  int64     `json:"took_ms"`
}

type TenantEvent struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"` // tenant.created, tenant.deleted
	TenantID  string `json:"tenant_id"`
	Timestamp string `json:"timestamp"`
}

type TenantRegistrationRequest struct {
	TenantID   string `json:"tenant_id"`
	Action     string `json:"action"`
	StreamName string `json:"stream_name"`
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

func NewNATSStorage(natsManager *NATSManager) *NATSStorage {
	return &NATSStorage{
		natsManager: natsManager,
	}
}

func (s *NATSStorage) getTenantKV(tenantID string) (jetstream.KeyValue, error) {
	s.natsManager.mu.RLock()
	kv, exists := s.natsManager.kvBuckets[tenantID]
	s.natsManager.mu.RUnlock()

	if exists {
		return kv, nil
	}

	return nil, fmt.Errorf("KV bucket not found for tenant: %s", tenantID)
}

func (s *NATSStorage) getTicketKey(ticketID string) string {
	return ticketID
}

func (s *NATSStorage) CreateTicket(tenant string, ticket *Ticket) error {
	kv, err := s.getTenantKV(tenant)
	if err != nil {
		return fmt.Errorf("failed to get tenant KV: %w", err)
	}

	key := s.getTicketKey(ticket.Id)
	data, err := json.Marshal(ticket)
	if err != nil {
		return fmt.Errorf("failed to marshal ticket: %w", err)
	}

	_, err = kv.Put(context.Background(), key, data)
	if err != nil {
		return fmt.Errorf("failed to store ticket in KV: %w", err)
	}
	return nil
}

func (s *NATSStorage) GetTicket(tenant, id string) (*Ticket, bool) {
	kv, err := s.getTenantKV(tenant)
	if err != nil {
		log.Printf("Error getting tenant KV: %v", err)
		return nil, false
	}

	key := s.getTicketKey(id)
	entry, err := kv.Get(context.Background(), key)
	if err != nil {
		if err == jetstream.ErrKeyNotFound {
			return nil, false
		}
		log.Printf("Error getting ticket from KV: %v", err)
		return nil, false
	}

	var ticket Ticket
	if err := json.Unmarshal(entry.Value(), &ticket); err != nil {
		log.Printf("Error unmarshaling ticket: %v", err)
		return nil, false
	}

	return &ticket, true
}

func (s *NATSStorage) UpdateTicket(tenant string, ticket *Ticket) bool {
	kv, err := s.getTenantKV(tenant)
	if err != nil {
		log.Printf("Error getting tenant KV: %v", err)
		return false
	}

	key := s.getTicketKey(ticket.Id)

	if _, exists := s.GetTicket(tenant, ticket.Id); !exists {
		return false
	}

	data, err := json.Marshal(ticket)
	if err != nil {
		log.Printf("Error marshaling ticket for update: %v", err)
		return false
	}

	_, err = kv.Put(context.Background(), key, data)
	if err != nil {
		log.Printf("Error updating ticket in KV: %v", err)
		return false
	}
	return true
}

func (s *NATSStorage) DeleteTicket(tenant, id string) (*Ticket, bool) {
	ticket, found := s.GetTicket(tenant, id)
	if !found {
		return nil, false
	}

	kv, err := s.getTenantKV(tenant)
	if err != nil {
		log.Printf("Error getting tenant KV: %v", err)
		return nil, false
	}

	key := s.getTicketKey(id)
	err = kv.Delete(context.Background(), key)
	if err != nil {
		log.Printf("Error deleting ticket from KV: %v", err)
		return nil, false
	}

	return ticket, true
}

func (s *NATSStorage) ListTickets(tenant string) map[string]interface{} {
	var tickets []*Ticket
	response := make(map[string]interface{})

	kv, err := s.getTenantKV(tenant)
	if err != nil {
		log.Printf("Error getting tenant KV: %v", err)
		tickets = []*Ticket{}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		watcher, err := kv.WatchAll(ctx)
		if err != nil {
			log.Printf("Error creating watcher for KV: %v", err)
			tickets = []*Ticket{}
		} else {
			for entry := range watcher.Updates() {
				if entry == nil {
					break
				}

				if entry.Operation() == jetstream.KeyValueDelete {
					continue
				}

				var ticket Ticket
				if err := json.Unmarshal(entry.Value(), &ticket); err != nil {
					log.Printf("Error unmarshaling ticket %s: %v", entry.Key(), err)
					continue
				}

				tickets = append(tickets, &ticket)
			}
		}
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

	if err := ts.storage.CreateTicket(req.Tenant, ticket); err != nil {
		return nil, fmt.Errorf("failed to create ticket: %w", err)
	}

	if err := ts.indexTicket(ticket); err != nil {
		log.Printf("ERROR: Failed to index ticket for tenant=%s ticket_id=%s: %v", req.Tenant, ticket.Id, err)
	}

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

		if err := ts.indexTicket(ticket); err != nil {
			log.Printf("ERROR: Failed to index updated ticket for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
		}

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

	if err := ts.deleteFromIndex(req.Tenant, req.TicketID); err != nil {
		log.Printf("ERROR: Failed to delete ticket from index for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
	}

	if err := ts.publishTicketDeleted(context.Background(), req.Tenant, ticket); err != nil {
		log.Printf("ERROR: Failed to publish ticket deleted event for tenant=%s ticket_id=%s: %v", req.Tenant, req.TicketID, err)
	}

	return map[string]string{"status": "deleted"}, nil
}

func (ts *TicketService) handleSearchTickets(req ServiceRequest) (interface{}, error) {
	dataBytes, err := json.Marshal(req.Data)
	if err != nil {
		return nil, err
	}

	var searchReq SearchRequest
	if err := json.Unmarshal(dataBytes, &searchReq); err != nil {
		return nil, err
	}

	return ts.searchTickets(req.Tenant, searchReq)
}

func connectNATS(urls string) (*NATSManager, error) {
	serverList := strings.Split(urls, ",")
	for i, url := range serverList {
		serverList[i] = strings.TrimSpace(url)
	}

	opts := []nats.Option{
		nats.Name("ticket-service"),
		nats.MaxReconnects(-1), // Retry forever
		nats.ReconnectWait(2 * time.Second),
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
	return &NATSManager{
		conn:      conn,
		js:        js,
		kvBuckets: make(map[string]jetstream.KeyValue),
	}, nil
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

	// Use tenant-aware subject hierarchy
	subject := fmt.Sprintf("tickets.%s.created", tenant)
	headers := map[string]string{
		"Tenant-ID":    tenant,
		"Event-Type":   "ticket.created",
		"Schema":       "ticket.create@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
		"Ticket-ID":    ticket.Id,
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

	// Use tenant-aware subject hierarchy
	subject := fmt.Sprintf("tickets.%s.updated", tenant)
	headers := map[string]string{
		"Tenant-ID":    tenant,
		"Event-Type":   "ticket.updated",
		"Schema":       "ticket.update@v1",
		"Nats-Msg-Id":  uuid.New().String(),
		"Content-Type": "application/json",
		"Ticket-ID":    ticket.Id,
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

func (nm *NATSManager) createTenantKV(tenantID string) error {
	ctx := context.Background()
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if _, exists := nm.kvBuckets[tenantID]; exists {
		return nil // Already exists
	}

	bucketName := fmt.Sprintf("tickets-%s", tenantID)
	kv, err := nm.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:      bucketName,
		Description: fmt.Sprintf("Ticket storage bucket for tenant %s", tenantID),
		TTL:         0,
	})
	if err != nil {
		return fmt.Errorf("failed to create KV bucket for tenant %s: %w", tenantID, err)
	}

	nm.kvBuckets[tenantID] = kv
	log.Printf("Created KV bucket for tenant: %s", tenantID)
	return nil
}

func (nm *NATSManager) deleteTenantKV(tenantID string) error {
	ctx := context.Background()
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if _, exists := nm.kvBuckets[tenantID]; !exists {
		return nil // Already deleted
	}

	bucketName := fmt.Sprintf("tickets-%s", tenantID)
	err := nm.js.DeleteKeyValue(ctx, bucketName)
	if err != nil {
		log.Printf("Failed to delete KV bucket for tenant %s: %v", tenantID, err)
		// Continue with cleanup even if deletion fails
	}

	delete(nm.kvBuckets, tenantID)
	log.Printf("Deleted KV bucket for tenant: %s", tenantID)
	return nil
}

func (ts *TicketService) createTenantStream(tenantID string) error {
	ctx := context.Background()
	streamName := fmt.Sprintf("TICKETS-%s", strings.ToUpper(tenantID))

	_, err := ts.natsManager.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     streamName,
		Subjects: []string{fmt.Sprintf("ticket.%s.>", tenantID)},
		Storage:  jetstream.FileStorage,
		MaxMsgs:  1000000,
		MaxAge:   30 * 24 * time.Hour, // 30 days
	})
	if err != nil {
		return fmt.Errorf("failed to create stream for tenant %s: %w", tenantID, err)
	}

	log.Printf("Created stream for tenant: %s", tenantID)
	return nil
}

func (ts *TicketService) deleteTenantStream(tenantID string) error {
	ctx := context.Background()
	streamName := fmt.Sprintf("TICKETS-%s", strings.ToUpper(tenantID))

	err := ts.natsManager.js.DeleteStream(ctx, streamName)
	if err != nil {
		log.Printf("Failed to delete stream for tenant %s: %v", tenantID, err)
	}

	log.Printf("Deleted stream for tenant: %s", tenantID)
	return nil
}

func (ts *TicketService) createTenantOpenSearchIndex(tenantID string) error {
	ctx := context.Background()
	indexName := fmt.Sprintf("%s-%s", ts.config.OpenSearchIndex, tenantID)

	indexMapping := `{
		"mappings": {
			"properties": {
				"id": {"type": "keyword"},
				"tenant": {"type": "keyword"},
				"title": {"type": "text"},
				"description": {"type": "text"},
				"name": {"type": "text"},
				"subject": {"type": "text"},
				
				"status": {"type": "keyword"},
				"statusid": {"type": "keyword"},
				"statuschangedtime": {"type": "date"},
				"priority": {"type": "keyword"},
				"priorityid": {"type": "keyword"},
				"urgency": {"type": "keyword"},
				"urgencyid": {"type": "keyword"},
				"impact": {"type": "keyword"},
				"impactid": {"type": "keyword"},
				
				"created_by": {"type": "keyword"},
				"createdbyid": {"type": "keyword"},
				"updatedbyid": {"type": "keyword"},
				"requester": {"type": "keyword"},
				"requesterid": {"type": "keyword"},
				"assignee": {"type": "keyword"},
				"technicianid": {"type": "keyword"},
				"closedby": {"type": "keyword"},
				"resolvedby": {"type": "keyword"},
				
				"created_at": {"type": "date"},
				"createdtime": {"type": "date"},
				"createddate": {"type": "date"},
				"updated_at": {"type": "date"},
				"updatedtime": {"type": "date"},
				"lastupdateddate": {"type": "date"},
				"dueby": {"type": "date"},
				"firstresponsedueby": {"type": "date"},
				"resolveddate": {"type": "date"},
				"closeddate": {"type": "date"},
				"lastapprovaldate": {"type": "date"},
				"lastapproveddate": {"type": "date"},
				"lastclosedtime": {"type": "date"},
				"lastopenedtime": {"type": "date"},
				"lastresolvedtime": {"type": "date"},
				"lastviolationtime": {"type": "date"},
				"olddueby": {"type": "date"},
				"oldresponsedue": {"type": "date"},
				"responsedue": {"type": "date"},
				"askfeedbackdate": {"type": "date"},
				"firstfeedbackdate": {"type": "date"},
				"groupchangedtime": {"type": "date"},
				"lastolaviolationtime": {"type": "date"},
				"oladueby": {"type": "date"},
				"oldoladueby": {"type": "date"},
				"lastucviolationtime": {"type": "date"},
				"olducdueby": {"type": "date"},
				"ucdueby": {"type": "date"},
				"removedtime": {"type": "date"},
				
				"company": {"type": "keyword"},
				"companyid": {"type": "keyword"},
				"location": {"type": "keyword"},
				"locationid": {"type": "keyword"},
				"department": {"type": "keyword"},
				"departmentid": {"type": "keyword"},
				"techniciangroup": {"type": "keyword"},
				"groupid": {"type": "keyword"},
				"vendorid": {"type": "keyword"},
				
				"category": {"type": "keyword"},
				"categoryid": {"type": "keyword"},
				"servicecategory": {"type": "keyword"},
				"servicecatalog": {"type": "keyword"},
				"servicecatalogid": {"type": "keyword"},
				"suggestedcategoryid": {"type": "keyword"},
				"suggestedgroupid": {"type": "keyword"},
				
				"approvalstatus": {"type": "keyword"},
				"approvaltype": {"type": "keyword"},
				
				"source": {"type": "keyword"},
				"sourceid": {"type": "keyword"},
				"type": {"type": "keyword"},
				"requesttype": {"type": "keyword"},
				"oobtype": {"type": "keyword"},
				
				"supportlevel": {"type": "keyword"},
				"firstresponsetime": {"type": "keyword"},
				"resolutionduelevel": {"type": "keyword"},
				"resolutionescalationtime": {"type": "keyword"},
				"responseduelevel": {"type": "keyword"},
				"responsedueviolated": {"type": "keyword"},
				"responseescalationtime": {"type": "keyword"},
				"slaviolated": {"type": "keyword"},
				"violatedslaid": {"type": "keyword"},
				"oladuelevel": {"type": "keyword"},
				"olaescalationtime": {"type": "keyword"},
				"olaviolated": {"type": "keyword"},
				"ucduelevel": {"type": "keyword"},
				"ucescalationtime": {"type": "keyword"},
				"ucviolated": {"type": "keyword"},
				"violateducid": {"type": "keyword"},
				
				"totalonholdduration": {"type": "long"},
				"totalresolutiontime": {"type": "long"},
				"totalslapausetime": {"type": "long"},
				"totalworkingtime": {"type": "long"},
				"totaluconholdduration": {"type": "long"},
				"totalucpausetime": {"type": "long"},
				"totalucworkingtime": {"type": "long"},
				"totalucresolutiontime": {"type": "long"},
				
				"tags": {"type": "text"},
				"removed": {"type": "keyword"},
				"removedbyid": {"type": "keyword"},
				"duetimemanuallyupdated": {"type": "keyword"},
				"reopened": {"type": "keyword"},
				"originaldescription": {"type": "text"},
				"templateid": {"type": "keyword"},
				"callfrom": {"type": "keyword"},
				"emailreadconfigemail": {"type": "keyword"},
				"emailreadconfigid": {"type": "keyword"},
				"purchaserequest": {"type": "keyword"},
				"spam": {"type": "keyword"},
				"viprequest": {"type": "keyword"},
				"transitionmodelid": {"type": "keyword"},
				"migrated": {"type": "keyword"},
				"mergedrequest": {"type": "keyword"},
				"messengerconfigid": {"type": "keyword"}
			}
		}
	}`

	createReq := opensearchapi.IndicesCreateRequest{
		Index: indexName,
		Body:  strings.NewReader(indexMapping),
	}

	createRes, err := createReq.Do(ctx, ts.opensearchClient)
	if err != nil {
		return fmt.Errorf("failed to create index for tenant %s: %w", tenantID, err)
	}
	defer createRes.Body.Close()

	if createRes.IsError() && createRes.StatusCode != 400 { // 400 = index already exists
		return fmt.Errorf("failed to create index for tenant %s, status: %s", tenantID, createRes.Status())
	}

	log.Printf("Created/ensured OpenSearch index for tenant: %s", tenantID)
	return nil
}

func (ts *TicketService) deleteTenantOpenSearchIndex(tenantID string) error {
	ctx := context.Background()
	indexName := fmt.Sprintf("%s-%s", ts.config.OpenSearchIndex, tenantID)

	deleteReq := opensearchapi.IndicesDeleteRequest{
		Index: []string{indexName},
	}

	deleteRes, err := deleteReq.Do(ctx, ts.opensearchClient)
	if err != nil {
		log.Printf("Failed to delete index for tenant %s: %v", tenantID, err)
		return nil // Continue even if deletion fails
	}
	defer deleteRes.Body.Close()

	log.Printf("Deleted OpenSearch index for tenant: %s", tenantID)
	return nil
}

func createOpenSearchClient(config *Config) (*opensearch.Client, error) {
	cfg := opensearch.Config{
		Addresses: []string{config.OpenSearchURL},
	}

	if config.OpenSearchUsername != "" && config.OpenSearchPassword != "" {
		cfg.Username = config.OpenSearchUsername
		cfg.Password = config.OpenSearchPassword
	}

	client, err := opensearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenSearch client: %w", err)
	}

	log.Printf("Successfully connected to OpenSearch: %s", config.OpenSearchURL)
	return client, nil
}

func (ts *TicketService) ensureOpenSearchIndex() error {
	ctx := context.Background()

	indexMapping := `{
		"mappings": {
			"properties": {
				"id": {"type": "keyword"},
				"tenant": {"type": "keyword"},
				"title": {"type": "text"},
				"description": {"type": "text"},
				"name": {"type": "text"},
				"subject": {"type": "text"},
				
				"status": {"type": "keyword"},
				"statusid": {"type": "keyword"},
				"statuschangedtime": {"type": "date"},
				"priority": {"type": "keyword"},
				"priorityid": {"type": "keyword"},
				"urgency": {"type": "keyword"},
				"urgencyid": {"type": "keyword"},
				"impact": {"type": "keyword"},
				"impactid": {"type": "keyword"},
				
				"created_by": {"type": "keyword"},
				"createdbyid": {"type": "keyword"},
				"updatedbyid": {"type": "keyword"},
				"requester": {"type": "keyword"},
				"requesterid": {"type": "keyword"},
				"assignee": {"type": "keyword"},
				"technicianid": {"type": "keyword"},
				"closedby": {"type": "keyword"},
				"resolvedby": {"type": "keyword"},
				
				"created_at": {"type": "date"},
				"createdtime": {"type": "date"},
				"createddate": {"type": "date"},
				"updated_at": {"type": "date"},
				"updatedtime": {"type": "date"},
				"lastupdateddate": {"type": "date"},
				"dueby": {"type": "date"},
				"firstresponsedueby": {"type": "date"},
				"resolveddate": {"type": "date"},
				"closeddate": {"type": "date"},
				"lastapprovaldate": {"type": "date"},
				"lastapproveddate": {"type": "date"},
				"lastclosedtime": {"type": "date"},
				"lastopenedtime": {"type": "date"},
				"lastresolvedtime": {"type": "date"},
				"lastviolationtime": {"type": "date"},
				"olddueby": {"type": "date"},
				"oldresponsedue": {"type": "date"},
				"responsedue": {"type": "date"},
				"askfeedbackdate": {"type": "date"},
				"firstfeedbackdate": {"type": "date"},
				"groupchangedtime": {"type": "date"},
				"lastolaviolationtime": {"type": "date"},
				"oladueby": {"type": "date"},
				"oldoladueby": {"type": "date"},
				"lastucviolationtime": {"type": "date"},
				"olducdueby": {"type": "date"},
				"ucdueby": {"type": "date"},
				"removedtime": {"type": "date"},
				
				"company": {"type": "keyword"},
				"companyid": {"type": "keyword"},
				"location": {"type": "keyword"},
				"locationid": {"type": "keyword"},
				"department": {"type": "keyword"},
				"departmentid": {"type": "keyword"},
				"techniciangroup": {"type": "keyword"},
				"groupid": {"type": "keyword"},
				"vendorid": {"type": "keyword"},
				
				"category": {"type": "keyword"},
				"categoryid": {"type": "keyword"},
				"servicecategory": {"type": "keyword"},
				"servicecatalog": {"type": "keyword"},
				"servicecatalogid": {"type": "keyword"},
				"suggestedcategoryid": {"type": "keyword"},
				"suggestedgroupid": {"type": "keyword"},
				
				"approvalstatus": {"type": "keyword"},
				"approvaltype": {"type": "keyword"},
				
				"source": {"type": "keyword"},
				"sourceid": {"type": "keyword"},
				"type": {"type": "keyword"},
				"requesttype": {"type": "keyword"},
				"oobtype": {"type": "keyword"},
				
				"supportlevel": {"type": "keyword"},
				"firstresponsetime": {"type": "keyword"},
				"resolutionduelevel": {"type": "keyword"},
				"resolutionescalationtime": {"type": "keyword"},
				"responseduelevel": {"type": "keyword"},
				"responsedueviolated": {"type": "keyword"},
				"responseescalationtime": {"type": "keyword"},
				"slaviolated": {"type": "keyword"},
				"violatedslaid": {"type": "keyword"},
				"oladuelevel": {"type": "keyword"},
				"olaescalationtime": {"type": "keyword"},
				"olaviolated": {"type": "keyword"},
				"ucduelevel": {"type": "keyword"},
				"ucescalationtime": {"type": "keyword"},
				"ucviolated": {"type": "keyword"},
				"violateducid": {"type": "keyword"},
				
				"totalonholdduration": {"type": "long"},
				"totalresolutiontime": {"type": "long"},
				"totalslapausetime": {"type": "long"},
				"totalworkingtime": {"type": "long"},
				"totaluconholdduration": {"type": "long"},
				"totalucpausetime": {"type": "long"},
				"totalucworkingtime": {"type": "long"},
				"totalucresolutiontime": {"type": "long"},
				
				"tags": {"type": "text"},
				"removed": {"type": "keyword"},
				"removedbyid": {"type": "keyword"},
				"duetimemanuallyupdated": {"type": "keyword"},
				"reopened": {"type": "keyword"},
				"originaldescription": {"type": "text"},
				"templateid": {"type": "keyword"},
				"callfrom": {"type": "keyword"},
				"emailreadconfigemail": {"type": "keyword"},
				"emailreadconfigid": {"type": "keyword"},
				"purchaserequest": {"type": "keyword"},
				"spam": {"type": "keyword"},
				"viprequest": {"type": "keyword"},
				"transitionmodelid": {"type": "keyword"},
				"migrated": {"type": "keyword"},
				"mergedrequest": {"type": "keyword"},
				"messengerconfigid": {"type": "keyword"}
			}
		}
	}`

	req := opensearchapi.IndicesExistsRequest{
		Index: []string{ts.config.OpenSearchIndex},
	}

	res, err := req.Do(ctx, ts.opensearchClient)
	if err != nil {
		return fmt.Errorf("failed to check if index exists: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == 404 {
		createReq := opensearchapi.IndicesCreateRequest{
			Index: ts.config.OpenSearchIndex,
			Body:  strings.NewReader(indexMapping),
		}

		createRes, err := createReq.Do(ctx, ts.opensearchClient)
		if err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
		defer createRes.Body.Close()

		if createRes.IsError() {
			return fmt.Errorf("failed to create index, status: %s", createRes.Status())
		}

		log.Printf("Successfully created OpenSearch index: %s", ts.config.OpenSearchIndex)
	} else if res.IsError() {
		return fmt.Errorf("error checking index existence, status: %s", res.Status())
	}

	return nil
}

func (ts *TicketService) indexTicket(ticket *Ticket) error {
	ctx := context.Background()
	indexName := fmt.Sprintf("%s-%s", ts.config.OpenSearchIndex, ticket.Tenant)

	documentBody, err := json.Marshal(ticket)
	if err != nil {
		return fmt.Errorf("failed to marshal ticket for indexing: %w", err)
	}

	req := opensearchapi.IndexRequest{
		Index:      indexName,
		DocumentID: ticket.Id,
		Body:       strings.NewReader(string(documentBody)),
		Refresh:    "wait_for",
	}

	res, err := req.Do(ctx, ts.opensearchClient)
	if err != nil {
		return fmt.Errorf("failed to index ticket: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to index ticket, status: %s", res.Status())
	}

	return nil
}

func (ts *TicketService) deleteFromIndex(tenant, ticketID string) error {
	ctx := context.Background()
	indexName := fmt.Sprintf("%s-%s", ts.config.OpenSearchIndex, tenant)

	req := opensearchapi.DeleteRequest{
		Index:      indexName,
		DocumentID: ticketID,
		Refresh:    "wait_for",
	}

	res, err := req.Do(ctx, ts.opensearchClient)
	if err != nil {
		return fmt.Errorf("failed to delete ticket from index: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("failed to delete ticket from index, status: %s", res.Status())
	}

	return nil
}

func (ts *TicketService) searchTickets(tenant string, req SearchRequest) (*SearchResponse, error) {
	ctx := context.Background()
	indexName := fmt.Sprintf("%s-%s", ts.config.OpenSearchIndex, tenant)

	if req.Limit == 0 {
		req.Limit = 10
	}

	query := map[string]interface{}{
		"size": req.Limit,
		"from": req.Offset,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must":   []interface{}{},
				"filter": []interface{}{},
			},
		},
	}

	boolQuery := query["query"].(map[string]interface{})["bool"].(map[string]interface{})
	mustQueries := boolQuery["must"].([]interface{})
	filterQueries := boolQuery["filter"].([]interface{})

	if req.Query != "" {
		mustQueries = append(mustQueries, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query": req.Query,
				"fields": []string{
					"title^3",
					"subject^3",
					"name^2",
					"description^2",
					"originaldescription",
					"tags^2",
					"created_by",
					"requester",
					"assignee",
					"company",
					"category",
					"location",
					"department",
					"status",
					"priority",
					"urgency",
					"impact",
					"source",
					"type",
					"techniciangroup",
				},
				"type": "best_fields",
			},
		})
	} else {
		mustQueries = append(mustQueries, map[string]interface{}{
			"match_all": map[string]interface{}{},
		})
	}

	for key, value := range req.Filters {
		filterQueries = append(filterQueries, map[string]interface{}{
			"term": map[string]interface{}{
				key: value,
			},
		})
	}

	boolQuery["must"] = mustQueries
	boolQuery["filter"] = filterQueries

	queryBody, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search query: %w", err)
	}

	searchReq := opensearchapi.SearchRequest{
		Index: []string{indexName},
		Body:  strings.NewReader(string(queryBody)),
	}

	res, err := searchReq.Do(ctx, ts.opensearchClient)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search request failed, status: %s", res.Status())
	}

	var searchResult map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&searchResult); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	hits := searchResult["hits"].(map[string]interface{})
	total := int64(hits["total"].(map[string]interface{})["value"].(float64))
	took := int64(searchResult["took"].(float64))

	var ticketIDs []string
	for _, hit := range hits["hits"].([]interface{}) {
		hitMap := hit.(map[string]interface{})
		docID := hitMap["_id"].(string)
		ticketIDs = append(ticketIDs, docID)
	}

	var tickets []*Ticket
	for _, ticketID := range ticketIDs {
		ticket, found := ts.storage.GetTicket(tenant, ticketID)
		if found {
			tickets = append(tickets, ticket)
		}
	}

	return &SearchResponse{
		Tickets: tickets,
		Total:   total,
		TookMs:  took,
	}, nil
}

func (ts *TicketService) startTenantManagementConsumer() {
	ctx := context.Background()

	// Ensure TENANT_EVENTS stream exists
	_, err := ts.natsManager.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "TENANT_EVENTS",
		Subjects: []string{"tenant.created", "tenant.deleted"},
		Storage:  jetstream.FileStorage,
		MaxMsgs:  1000000,
		MaxAge:   7 * 24 * time.Hour, // 7 days
	})
	if err != nil {
		log.Printf("Failed to create tenant events stream: %v", err)
		return
	}

	// Create consumer for tenant events
	consumer, err := ts.natsManager.js.CreateOrUpdateConsumer(ctx, "TENANT_EVENTS", jetstream.ConsumerConfig{
		Name:          "ticket-service-tenant-events",
		FilterSubject: "tenant.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		log.Printf("Failed to create tenant events consumer: %v", err)
		return
	}

	iter, err := consumer.Messages()
	if err != nil {
		log.Printf("Failed to get messages from tenant events consumer: %v", err)
		return
	}

	log.Printf("Started tenant management consumer")

	for {
		select {
		case <-ts.shutdownCh:
			iter.Stop()
			return
		default:
			msg, err := iter.Next()
			if err != nil {
				log.Printf("Error getting next tenant event message: %v", err)
				continue
			}
			ts.handleTenantEvent(msg)
		}
	}
}

func (ts *TicketService) handleTenantEvent(msg jetstream.Msg) {
	var event TenantEvent
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		log.Printf("Failed to unmarshal tenant event: %v", err)
		msg.Ack()
		return
	}

	log.Printf("Processing tenant event: type=%s, tenant=%s", event.EventType, event.TenantID)

	switch event.EventType {
	case "tenant.created":
		// Create KV bucket for tenant
		if err := ts.natsManager.createTenantKV(event.TenantID); err != nil {
			log.Printf("Failed to create KV bucket for tenant %s: %v", event.TenantID, err)
		}

		// Create stream for tenant
		if err := ts.createTenantStream(event.TenantID); err != nil {
			log.Printf("Failed to create stream for tenant %s: %v", event.TenantID, err)
		}

		// Create OpenSearch index for tenant
		if err := ts.createTenantOpenSearchIndex(event.TenantID); err != nil {
			log.Printf("Failed to create OpenSearch index for tenant %s: %v", event.TenantID, err)
		}

	case "tenant.deleted":
		// Delete KV bucket for tenant
		if err := ts.natsManager.deleteTenantKV(event.TenantID); err != nil {
			log.Printf("Failed to delete KV bucket for tenant %s: %v", event.TenantID, err)
		}

		// Delete stream for tenant
		if err := ts.deleteTenantStream(event.TenantID); err != nil {
			log.Printf("Failed to delete stream for tenant %s: %v", event.TenantID, err)
		}

		// Delete OpenSearch index for tenant
		if err := ts.deleteTenantOpenSearchIndex(event.TenantID); err != nil {
			log.Printf("Failed to delete OpenSearch index for tenant %s: %v", event.TenantID, err)
		}
	}

	msg.Ack()
}

func containsSeverity(description string) bool {
	lower := strings.ToLower(description)
	return strings.Contains(lower, "critical") || strings.Contains(lower, "major")
}

func loadConfig() *Config {
	return &Config{
		NATSUrl:            getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"),
		ServiceName:        getEnv("SERVICE_NAME", "ticket-service"),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
		OpenSearchURL:      getEnv("OPENSEARCH_URL", "http://localhost:9200"),
		OpenSearchUsername: getEnv("OPENSEARCH_USERNAME", ""),
		OpenSearchPassword: getEnv("OPENSEARCH_PASSWORD", ""),
		OpenSearchIndex:    getEnv("OPENSEARCH_INDEX", "tickets"),
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

	opensearchClient, err := createOpenSearchClient(config)
	if err != nil {
		log.Fatalf("Failed to connect to OpenSearch: %v", err)
	}

	storage := NewNATSStorage(natsManager)

	service := &TicketService{
		natsManager:      natsManager,
		storage:          storage,
		opensearchClient: opensearchClient,
		config:           config,
		tenantConsumers:  make(map[string]jetstream.Consumer),
		shutdownCh:       make(chan struct{}),
	}

	// Start tenant management consumer
	//go service.startTenantManagementConsumer()

	// Start tenant registration listener
	go service.startTenantRegistrationListener()

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

	// Signal shutdown to goroutines
	close(service.shutdownCh)

	if natsManager != nil && natsManager.conn != nil {
		natsManager.conn.Close()
		log.Println("NATS connection closed")
	}

	log.Println("Service exited")
}

func (ts *TicketService) startTenantRegistrationListener() {
	ctx := context.Background()

	// Create consumer for tenant management events
	consumer, err := ts.natsManager.js.CreateOrUpdateConsumer(ctx, "TENANT_MANAGEMENT", jetstream.ConsumerConfig{
		Name:          "ticket-service-tenant-registration",
		FilterSubject: "tenant.register.ticket-service",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		log.Printf("Failed to create tenant registration consumer: %v", err)
		return
	}

	iter, err := consumer.Messages()
	if err != nil {
		log.Printf("Failed to get messages from tenant registration consumer: %v", err)
		return
	}

	log.Printf("Started tenant registration listener")

	for {
		select {
		case <-ts.shutdownCh:
			iter.Stop()
			return
		default:
			msg, err := iter.Next()
			if err != nil {
				log.Printf("Error getting next tenant registration message: %v", err)
				continue
			}
			ts.handleTenantRegistration(msg)
		}
	}
}

func (ts *TicketService) handleTenantRegistration(msg jetstream.Msg) {
	var req TenantRegistrationRequest
	if err := json.Unmarshal(msg.Data(), &req); err != nil {
		log.Printf("Failed to unmarshal tenant registration request: %v", err)
		msg.Ack()
		return
	}

	log.Printf("Processing tenant registration: %s action for tenant %s", req.Action, req.TenantID)

	switch req.Action {
	case "register":
		// Create KV bucket for this tenant
		if err := ts.natsManager.createTenantKV(req.TenantID); err != nil {
			log.Printf("Failed to create KV bucket for tenant %s: %v", req.TenantID, err)
		} else {
			log.Printf("Successfully created KV bucket for tenant %s", req.TenantID)
		}

		// Create consumer for this tenant's stream to process ticket events
		if err := ts.createTenantConsumer(req.TenantID, req.StreamName); err != nil {
			log.Printf("Failed to create consumer for tenant %s: %v", req.TenantID, err)
		} else {
			log.Printf("Successfully created consumer for tenant %s", req.TenantID)
		}
	case "unregister":
		// Remove consumer for this tenant
		if err := ts.removeTenantConsumer(req.TenantID); err != nil {
			log.Printf("Failed to remove consumer for tenant %s: %v", req.TenantID, err)
		} else {
			log.Printf("Successfully removed consumer for tenant %s", req.TenantID)
		}

		// Delete KV bucket for this tenant
		if err := ts.natsManager.deleteTenantKV(req.TenantID); err != nil {
			log.Printf("Failed to delete KV bucket for tenant %s: %v", req.TenantID, err)
		} else {
			log.Printf("Successfully deleted KV bucket for tenant %s", req.TenantID)
		}
	default:
		log.Printf("Unknown tenant registration action: %s", req.Action)
	}

	msg.Ack()
}

func (ts *TicketService) createTenantConsumer(tenantID, streamName string) error {
	ctx := context.Background()

	// Create consumer for tenant-specific ticket events
	consumerName := fmt.Sprintf("ticket-service-%s", tenantID)
	filterSubject := fmt.Sprintf("tenant.%s.tickets.>", tenantID)

	consumer, err := ts.natsManager.js.CreateOrUpdateConsumer(ctx, streamName, jetstream.ConsumerConfig{
		Name:          consumerName,
		FilterSubject: filterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
		Description:   fmt.Sprintf("Consumer for ticket service processing tenant %s events", tenantID),
	})
	if err != nil {
		return fmt.Errorf("failed to create consumer %s: %w", consumerName, err)
	}

	// Store consumer reference
	ts.tenantConsumersMutex.Lock()
	ts.tenantConsumers[tenantID] = consumer
	ts.tenantConsumersMutex.Unlock()

	// Start processing messages for this tenant
	go ts.processTenantMessages(tenantID, consumer)

	return nil
}

func (ts *TicketService) removeTenantConsumer(tenantID string) error {
	ts.tenantConsumersMutex.Lock()
	defer ts.tenantConsumersMutex.Unlock()

	consumer, exists := ts.tenantConsumers[tenantID]
	if !exists {
		return fmt.Errorf("consumer for tenant %s not found", tenantID)
	}

	// Remove from tracking
	delete(ts.tenantConsumers, tenantID)

	// Note: JetStream consumers don't have explicit stop method
	// The consumer will be cleaned up when the service shuts down
	_ = consumer // Acknowledge the consumer variable
	log.Printf("Removed consumer tracking for tenant %s", tenantID)
	return nil
}

func (ts *TicketService) processTenantMessages(tenantID string, consumer jetstream.Consumer) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Panic in tenant message processor for %s: %v", tenantID, r)
		}
	}()

	iter, err := consumer.Messages()
	if err != nil {
		log.Printf("Failed to get message iterator for tenant %s: %v", tenantID, err)
		return
	}

	log.Printf("Started processing messages for tenant %s", tenantID)

	for {
		select {
		case <-ts.shutdownCh:
			iter.Stop()
			return
		default:
			msg, err := iter.Next()
			if err != nil {
				log.Printf("Error getting next message for tenant %s: %v", tenantID, err)
				continue
			}

			// Validate message is for the correct tenant
			if !ts.validateMessageTenant(msg, tenantID) {
				log.Printf("Message tenant validation failed for %s", tenantID)
				msg.Term()
				continue
			}

			// Process the message (implement your business logic here)
			if err := ts.processTicketMessage(tenantID, msg); err != nil {
				log.Printf("Failed to process message for tenant %s: %v", tenantID, err)
				msg.Term()
			} else {
				msg.Ack()
			}
		}
	}
}

func (ts *TicketService) validateMessageTenant(msg jetstream.Msg, expectedTenantID string) bool {
	// Check tenant ID in headers
	if tenantID := msg.Headers().Get("Tenant-ID"); tenantID != "" {
		return tenantID == expectedTenantID
	}

	// Check tenant ID in subject
	subject := msg.Subject()
	if strings.HasPrefix(subject, fmt.Sprintf("tenant.%s", expectedTenantID)) {
		return true
	}

	return false
}

func (ts *TicketService) processTicketMessage(tenantID string, msg jetstream.Msg) error {
	subject := msg.Subject()
	log.Printf("Processing ticket message for tenant %s: %s", tenantID, subject)

	// Parse the subject to determine the event type
	// Expected format: tickets.{tenantID}.{action} (e.g., tickets.tenant1.created)
	subjectParts := strings.Split(subject, ".")
	if len(subjectParts) < 3 {
		return fmt.Errorf("invalid subject format: %s", subject)
	}

	eventType := subjectParts[3] // created, updated, deleted, etc.

	// Get tenant KV bucket
	kv, err := ts.getTenantKV(tenantID)
	if err != nil {
		return fmt.Errorf("failed to get tenant KV bucket: %w", err)
	}

	switch eventType {
	case "create", "update":
		return ts.handleTicketCreateOrUpdate(tenantID, kv, msg)
	case "delete":
		return ts.handleTicketDelete(tenantID, kv, msg)
	case "get":
		return ts.handleTicketGet(tenantID, kv, msg)
	case "getAll":
		return ts.handleTicketGetAll(tenantID, kv, msg)
	default:
		log.Printf("Unknown ticket event type: %s", eventType)
		return nil // Don't fail for unknown event types
	}
}

func (ts *TicketService) getTenantKV(tenantID string) (jetstream.KeyValue, error) {
	ts.natsManager.mu.RLock()
	kv, exists := ts.natsManager.kvBuckets[tenantID]
	ts.natsManager.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("KV bucket not found for tenant: %s", tenantID)
	}

	return kv, nil
}

func (ts *TicketService) handleTicketCreateOrUpdate(tenantID string, kv jetstream.KeyValue, msg jetstream.Msg) error {
	// Parse the ticket event
	var ticketEvent TicketEvent
	if err := json.Unmarshal(msg.Data(), &ticketEvent); err != nil {
		return fmt.Errorf("failed to unmarshal ticket event: %w", err)
	}

	if ticketEvent.Data == nil {
		return fmt.Errorf("ticket event data is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ticket := ticketEvent.Data

	if len(ticket.Id) == 0 {

		ticket.Id = uuid.New().String()
	}

	// Store the ticket in the KV bucket
	ticketKey := ticket.Id
	ticketData, err := json.Marshal(ticket)
	if err != nil {
		return fmt.Errorf("failed to marshal ticket for storage: %w", err)
	}

	_, err = kv.Put(ctx, ticketKey, ticketData)
	if err != nil {
		return fmt.Errorf("failed to store ticket in KV: %w", err)
	}

	log.Printf("Successfully stored ticket %s for tenant %s in KV store", ticket.Id, tenantID)

	return nil
}

func (ts *TicketService) handleTicketDelete(tenantID string, kv jetstream.KeyValue, msg jetstream.Msg) error {
	// Parse the ticket event
	var ticketEvent TicketEvent
	if err := json.Unmarshal(msg.Data(), &ticketEvent); err != nil {
		return fmt.Errorf("failed to unmarshal ticket event: %w", err)
	}

	if ticketEvent.Data == nil {
		return fmt.Errorf("ticket event data is nil")
	}

	ticket := ticketEvent.Data

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Delete the main ticket entry
	ticketKey := ticket.Id
	err := kv.Delete(ctx, ticketKey)
	if err != nil && err != jetstream.ErrKeyNotFound {
		return fmt.Errorf("failed to delete ticket from KV: %w", err)
	}

	log.Printf("Successfully deleted ticket %s for tenant %s from KV store", ticket.Id, tenantID)
	return nil
}
func (ts *TicketService) handleTicketGet(tenantID string, kv jetstream.KeyValue, msg jetstream.Msg) error {
	// Parse the ticket event
	var ticketEvent TicketEvent
	if err := json.Unmarshal(msg.Data(), &ticketEvent); err != nil {
		return fmt.Errorf("failed to unmarshal ticket event: %w", err)
	}

	if ticketEvent.Data == nil {
		return fmt.Errorf("ticket event data is nil")
	}

	ticket := ticketEvent.Data

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Delete the main ticket entry
	ticketKey := ticket.Id
	err := kv.Delete(ctx, ticketKey)
	if err != nil && err != jetstream.ErrKeyNotFound {
		return fmt.Errorf("failed to delete ticket from KV: %w", err)
	}

	log.Printf("Successfully deleted ticket %s for tenant %s from KV store", ticket.Id, tenantID)
	return nil
}
func (ts *TicketService) handleTicketGetAll(tenantID string, kv jetstream.KeyValue, msg jetstream.Msg) error {
	// Parse the ticket event
	var ticketEvent TicketEvent
	if err := json.Unmarshal(msg.Data(), &ticketEvent); err != nil {
		return fmt.Errorf("failed to unmarshal ticket event: %w", err)
	}

	if ticketEvent.Data == nil {
		return fmt.Errorf("ticket event data is nil")
	}

	ticket := ticketEvent.Data

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Delete the main ticket entry
	ticketKey := ticket.Id
	value, err := kv.Get(ctx, ticketKey)
	if err != nil && err != jetstream.ErrKeyNotFound {
		return fmt.Errorf("failed to delete ticket from KV: %w", err)
	}

	log.Printf("Successfully deleted ticket %s for tenant %s from KV store", ticket.Id, tenantID)
	return value.Value()
}
