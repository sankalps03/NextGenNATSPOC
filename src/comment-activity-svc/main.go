package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

type CommentActivityService struct {
	natsConn    *nats.Conn
	js          nats.JetStreamContext
	objStore    nats.ObjectStore // NEW: Object Store for comments and activities
	serviceName string
	shutdownCh  chan struct{}
	wg          sync.WaitGroup

	// In-memory storage (POC only)
	comments   map[string]map[string]*Comment // tenant_id -> comment_id -> Comment
	activities map[string][]*Activity         // tenant_id -> []Activity
	mu         sync.RWMutex
}

type Comment struct {
	ID        string    `json:"id"`
	TicketID  string    `json:"ticket_id"`
	TenantID  string    `json:"tenant_id"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Activity struct {
	ID          string                 `json:"id"`
	TicketID    string                 `json:"ticket_id"`
	TenantID    string                 `json:"tenant_id"`
	Type        string                 `json:"type"` // comment, status_change, assignment, priority_change, etc.
	Description string                 `json:"description"`
	UserID      string                 `json:"user_id"`
	UserName    string                 `json:"user_name"`
	Metadata    map[string]interface{} `json:"metadata"`
	Timestamp   time.Time              `json:"timestamp"`
}

type ServiceRequest struct {
	Action   string      `json:"action"`
	TenantID string      `json:"tenant_id"`
	Data     interface{} `json:"data,omitempty"`
}

type ServiceResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type AddCommentRequest struct {
	TicketID string `json:"ticket_id"`
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	Content  string `json:"content"`
}

type GetCommentsRequest struct {
	TicketID string `json:"ticket_id"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

type GetActivitiesRequest struct {
	TicketID string `json:"ticket_id"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

type TicketEvent struct {
	EventID   string                 `json:"event_id"`
	EventType string                 `json:"event_type"` // ticket.created, ticket.updated, ticket.deleted
	TenantID  string                 `json:"tenant_id"`
	TicketID  string                 `json:"ticket_id"`
	UserID    string                 `json:"user_id"`
	UserName  string                 `json:"user_name"`
	Changes   map[string]interface{} `json:"changes"`
	Timestamp time.Time              `json:"timestamp"`
}

type Config struct {
	NATSURLs    []string
	ServiceName string
	LogLevel    string
}

func main() {
	config := loadConfig()

	service, err := NewCommentActivityService(config)
	if err != nil {
		log.Fatalf("Failed to create comment activity service: %v", err)
	}

	if err := service.Start(); err != nil {
		log.Fatalf("Failed to start service: %v", err)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down comment activity service...")
	service.Shutdown()
}

func loadConfig() Config {
	natsURLs := strings.Split(getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"), ",")

	return Config{
		NATSURLs:    natsURLs,
		ServiceName: getEnv("SERVICE_NAME", "comment-activity-service"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func NewCommentActivityService(config Config) (*CommentActivityService, error) {
	// Connect to NATS
	natsConn, err := nats.Connect(strings.Join(config.NATSURLs, ","),
		nats.Name(config.ServiceName),
		nats.ReconnectWait(time.Second*2),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create JetStream context
	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	// Ensure streams exist
	if err := ensureStreams(js); err != nil {
		return nil, fmt.Errorf("failed to ensure streams: %w", err)
	}

	// Create or get Object Store for comments and activities
	objStore, err := createObjectStore(js, "comment-activity-objects")
	if err != nil {
		log.Printf("WARNING: Failed to create Object Store: %v. Comments will only use in-memory storage.", err)
		objStore = nil
	}

	service := &CommentActivityService{
		natsConn:    natsConn,
		js:          js,
		objStore:    objStore,
		serviceName: config.ServiceName,
		shutdownCh:  make(chan struct{}),
		comments:    make(map[string]map[string]*Comment),
		activities:  make(map[string][]*Activity),
	}

	return service, nil
}

func ensureStreams(js nats.JetStreamContext) error {
	// Ensure EVENTS stream exists for ticket events (needed for subscription)
	eventsStreamName := "EVENTS"
	_, err := js.StreamInfo(eventsStreamName)
	if err != nil {
		_, err = js.AddStream(&nats.StreamConfig{
			Name: eventsStreamName,
			Subjects: []string{
				"tenant.*.ticket.created",
				"tenant.*.ticket.updated",
				"tenant.*.ticket.deleted",
			},
			Storage: nats.FileStorage,
			MaxMsgs: 1000000,
			MaxAge:  7 * 24 * time.Hour, // 7 days
		})
		if err != nil {
			log.Printf("Warning: Failed to create EVENTS stream (may already exist): %v", err)
		} else {
			log.Printf("Created JetStream stream: %s with tenant-scoped ticket event subjects", eventsStreamName)
		}
	}

	// Ensure ACTIVITY_EVENTS stream exists with tenant-scoped subjects
	activityStreamName := "ACTIVITY_EVENTS"
	_, err = js.StreamInfo(activityStreamName)
	if err != nil {
		_, err = js.AddStream(&nats.StreamConfig{
			Name: activityStreamName,
			Subjects: []string{
				"tenant.*.activity.created",
				"tenant.*.comment.added",
				"tenant.*.comment.updated",
			},
			Storage: nats.FileStorage,
			MaxMsgs: 1000000,
			MaxAge:  7 * 24 * time.Hour, // 7 days
		})
		if err != nil {
			return fmt.Errorf("failed to create activity events stream: %w", err)
		}
		log.Printf("Created JetStream stream: %s with tenant-scoped subjects", activityStreamName)
	}

	return nil
}

func createObjectStore(js nats.JetStreamContext, storeName string) (nats.ObjectStore, error) {
	objStore, err := js.CreateObjectStore(&nats.ObjectStoreConfig{
		Bucket:      storeName,
		Description: "Tenant-scoped comments and activity logs",
		TTL:         30 * 24 * time.Hour, // 30 days retention
	})
	if err != nil {
		// If object store already exists, try to get it
		objStore, err = js.ObjectStore(storeName)
		if err != nil {
			return nil, fmt.Errorf("failed to create or get object store '%s': %w", storeName, err)
		}
	}

	log.Printf("NATS Object Store '%s' ready for tenant-scoped comments and activities", storeName)
	return objStore, nil
}

func (s *CommentActivityService) Start() error {
	// Subscribe to service requests
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.subscribeToServiceRequests()
	}()

	// Subscribe to ticket events
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.subscribeToTicketEvents()
	}()

	log.Printf("Comment & Activity service started successfully")
	return nil
}

func (s *CommentActivityService) subscribeToServiceRequests() {
	// Subscribe to tenant-wildcard subject with queue group
	// Subject pattern: tenant.*.comment-activity.service
	queueGroup := "comment-activity-service"
	subject := "tenant.*.comment-activity.service"

	sub, err := s.natsConn.QueueSubscribe(subject, queueGroup, s.handleServiceRequest)
	if err != nil {
		log.Printf("Failed to subscribe to service requests: %v", err)
		return
	}
	defer sub.Unsubscribe()

	log.Printf("Subscribed to %s with queue group: %s", subject, queueGroup)
	<-s.shutdownCh
}

func (s *CommentActivityService) subscribeToTicketEvents() {
	// Subscribe to tenant-scoped ticket events with durable consumer
	// Pattern: tenant.*.ticket.> matches all ticket events for all tenants
	sub, err := s.js.Subscribe("tenant.*.ticket.>", s.handleTicketEvent,
		nats.Durable("comment-activity-tenant-ticket-events"))
	if err != nil {
		log.Printf("Failed to subscribe to ticket events: %v", err)
		return
	}
	defer sub.Unsubscribe()

	log.Printf("Subscribed to tenant-scoped ticket events: tenant.*.ticket.>")
	<-s.shutdownCh
}

// extractTenantFromSubject extracts tenant ID from NATS subject
// Subject format: tenant.{id}.comment-activity.service
func extractTenantFromSubject(subject string) string {
	parts := strings.Split(subject, ".")
	if len(parts) >= 2 && parts[0] == "tenant" {
		return parts[1]
	}
	return "default" // Fallback for non-tenant subjects
}

func (s *CommentActivityService) handleServiceRequest(msg *nats.Msg) {
	// Extract tenant ID from subject (tenant.{id}.comment-activity.service)
	tenantID := extractTenantFromSubject(msg.Subject)

	var req ServiceRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		log.Printf("[Tenant: %s] Failed to unmarshal service request: %v", tenantID, err)
		s.respondWithError(msg, "invalid_request", err.Error())
		return
	}

	// Set tenant ID from subject (authoritative source)
	req.TenantID = tenantID
	log.Printf("[Tenant: %s] Processing request: action=%s", tenantID, req.Action)

	switch req.Action {
	case "add_comment":
		s.handleAddComment(msg, req)
	case "get_comments":
		s.handleGetComments(msg, req)
	case "get_activities":
		s.handleGetActivities(msg, req)
	case "get_timeline":
		s.handleGetTimeline(msg, req)
	default:
		s.respondWithError(msg, "unknown_action", fmt.Sprintf("Unknown action: %s", req.Action))
	}
}

func (s *CommentActivityService) handleAddComment(msg *nats.Msg, req ServiceRequest) {
	var commentReq AddCommentRequest
	if err := json.Unmarshal(jsonMarshal(req.Data), &commentReq); err != nil {
		s.respondWithError(msg, "invalid_data", err.Error())
		return
	}

	// Validate required fields
	if commentReq.TicketID == "" || commentReq.UserID == "" || commentReq.Content == "" {
		s.respondWithError(msg, "validation_error", "ticket_id, user_id, and content are required")
		return
	}

	// Create comment
	comment := &Comment{
		ID:        uuid.New().String(),
		TicketID:  commentReq.TicketID,
		TenantID:  req.TenantID,
		UserID:    commentReq.UserID,
		UserName:  commentReq.UserName,
		Content:   commentReq.Content,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	// Store comment in-memory
	s.mu.Lock()
	if s.comments[req.TenantID] == nil {
		s.comments[req.TenantID] = make(map[string]*Comment)
	}
	s.comments[req.TenantID][comment.ID] = comment
	s.mu.Unlock()

	// Store comment in Object Store (async, don't block on errors)
	go func() {
		if err := s.storeCommentInObjectStore(comment); err != nil {
			log.Printf("[Tenant: %s] WARNING: Failed to store comment in Object Store: %v", req.TenantID, err)
		}
	}()

	// Create activity entry
	activity := &Activity{
		ID:          uuid.New().String(),
		TicketID:    commentReq.TicketID,
		TenantID:    req.TenantID,
		Type:        "comment",
		Description: fmt.Sprintf("%s added a comment", comment.UserName),
		UserID:      commentReq.UserID,
		UserName:    commentReq.UserName,
		Metadata: map[string]interface{}{
			"comment_id": comment.ID,
			"content":    comment.Content,
		},
		Timestamp: comment.CreatedAt,
	}

	s.addActivity(activity)

	// Publish events
	s.publishCommentEvent(comment)
	s.publishActivityEvent(activity)

	s.respondWithSuccess(msg, comment)
}

func (s *CommentActivityService) handleGetComments(msg *nats.Msg, req ServiceRequest) {
	var getReq GetCommentsRequest
	if err := json.Unmarshal(jsonMarshal(req.Data), &getReq); err != nil {
		s.respondWithError(msg, "invalid_data", err.Error())
		return
	}

	if getReq.TicketID == "" {
		s.respondWithError(msg, "validation_error", "ticket_id is required")
		return
	}

	// Get comments for the ticket
	s.mu.RLock()
	var comments []*Comment
	if tenantComments, exists := s.comments[req.TenantID]; exists {
		for _, comment := range tenantComments {
			if comment.TicketID == getReq.TicketID {
				comments = append(comments, comment)
			}
		}
	}
	s.mu.RUnlock()

	// Sort by creation time
	sort.Slice(comments, func(i, j int) bool {
		return comments[i].CreatedAt.Before(comments[j].CreatedAt)
	})

	// Apply pagination
	if getReq.Limit == 0 {
		getReq.Limit = 50
	}

	start := getReq.Offset
	end := start + getReq.Limit
	if start > len(comments) {
		start = len(comments)
	}
	if end > len(comments) {
		end = len(comments)
	}

	paginatedComments := comments[start:end]

	response := map[string]interface{}{
		"comments": paginatedComments,
		"total":    len(comments),
		"limit":    getReq.Limit,
		"offset":   getReq.Offset,
	}

	s.respondWithSuccess(msg, response)
}

func (s *CommentActivityService) handleGetActivities(msg *nats.Msg, req ServiceRequest) {
	var getReq GetActivitiesRequest
	if err := json.Unmarshal(jsonMarshal(req.Data), &getReq); err != nil {
		s.respondWithError(msg, "invalid_data", err.Error())
		return
	}

	if getReq.TicketID == "" {
		s.respondWithError(msg, "validation_error", "ticket_id is required")
		return
	}

	// Get activities for the ticket
	s.mu.RLock()
	var activities []*Activity
	if tenantActivities, exists := s.activities[req.TenantID]; exists {
		for _, activity := range tenantActivities {
			if activity.TicketID == getReq.TicketID {
				activities = append(activities, activity)
			}
		}
	}
	s.mu.RUnlock()

	// Sort by timestamp (newest first)
	sort.Slice(activities, func(i, j int) bool {
		return activities[i].Timestamp.After(activities[j].Timestamp)
	})

	// Apply pagination
	if getReq.Limit == 0 {
		getReq.Limit = 100
	}

	start := getReq.Offset
	end := start + getReq.Limit
	if start > len(activities) {
		start = len(activities)
	}
	if end > len(activities) {
		end = len(activities)
	}

	paginatedActivities := activities[start:end]

	response := map[string]interface{}{
		"activities": paginatedActivities,
		"total":      len(activities),
		"limit":      getReq.Limit,
		"offset":     getReq.Offset,
	}

	s.respondWithSuccess(msg, response)
}

func (s *CommentActivityService) handleGetTimeline(msg *nats.Msg, req ServiceRequest) {
	var getReq GetActivitiesRequest
	if err := json.Unmarshal(jsonMarshal(req.Data), &getReq); err != nil {
		s.respondWithError(msg, "invalid_data", err.Error())
		return
	}

	if getReq.TicketID == "" {
		s.respondWithError(msg, "validation_error", "ticket_id is required")
		return
	}

	// Get all activities and comments for the ticket
	s.mu.RLock()
	var timeline []interface{}

	// Add activities
	if tenantActivities, exists := s.activities[req.TenantID]; exists {
		for _, activity := range tenantActivities {
			if activity.TicketID == getReq.TicketID {
				timeline = append(timeline, map[string]interface{}{
					"type": "activity",
					"data": activity,
				})
			}
		}
	}

	// Add comments as activities
	if tenantComments, exists := s.comments[req.TenantID]; exists {
		for _, comment := range tenantComments {
			if comment.TicketID == getReq.TicketID {
				timeline = append(timeline, map[string]interface{}{
					"type": "comment",
					"data": comment,
				})
			}
		}
	}
	s.mu.RUnlock()

	// Sort by timestamp (newest first)
	sort.Slice(timeline, func(i, j int) bool {
		var timeI, timeJ time.Time

		if timeline[i].(map[string]interface{})["type"] == "activity" {
			timeI = timeline[i].(map[string]interface{})["data"].(*Activity).Timestamp
		} else {
			timeI = timeline[i].(map[string]interface{})["data"].(*Comment).CreatedAt
		}

		if timeline[j].(map[string]interface{})["type"] == "activity" {
			timeJ = timeline[j].(map[string]interface{})["data"].(*Activity).Timestamp
		} else {
			timeJ = timeline[j].(map[string]interface{})["data"].(*Comment).CreatedAt
		}

		return timeI.After(timeJ)
	})

	// Apply pagination
	if getReq.Limit == 0 {
		getReq.Limit = 100
	}

	start := getReq.Offset
	end := start + getReq.Limit
	if start > len(timeline) {
		start = len(timeline)
	}
	if end > len(timeline) {
		end = len(timeline)
	}

	paginatedTimeline := timeline[start:end]

	response := map[string]interface{}{
		"timeline": paginatedTimeline,
		"total":    len(timeline),
		"limit":    getReq.Limit,
		"offset":   getReq.Offset,
	}

	s.respondWithSuccess(msg, response)
}

func (s *CommentActivityService) handleTicketEvent(msg *nats.Msg) {
	var event TicketEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		log.Printf("Failed to unmarshal ticket event: %v", err)
		msg.Ack()
		return
	}

	log.Printf("Processing ticket event: type=%s, ticket=%s, tenant=%s", event.EventType, event.TicketID, event.TenantID)

	// Create activity based on event type
	var activity *Activity

	switch event.EventType {
	case "ticket.created":
		activity = &Activity{
			ID:          uuid.New().String(),
			TicketID:    event.TicketID,
			TenantID:    event.TenantID,
			Type:        "creation",
			Description: fmt.Sprintf("Ticket created by %s", event.UserName),
			UserID:      event.UserID,
			UserName:    event.UserName,
			Metadata:    map[string]interface{}{"event_id": event.EventID},
			Timestamp:   event.Timestamp,
		}

	case "ticket.updated":
		description := s.generateUpdateDescription(event.Changes, event.UserName)
		activity = &Activity{
			ID:          uuid.New().String(),
			TicketID:    event.TicketID,
			TenantID:    event.TenantID,
			Type:        "update",
			Description: description,
			UserID:      event.UserID,
			UserName:    event.UserName,
			Metadata: map[string]interface{}{
				"event_id": event.EventID,
				"changes":  event.Changes,
			},
			Timestamp: event.Timestamp,
		}

	case "ticket.deleted":
		activity = &Activity{
			ID:          uuid.New().String(),
			TicketID:    event.TicketID,
			TenantID:    event.TenantID,
			Type:        "deletion",
			Description: fmt.Sprintf("Ticket deleted by %s", event.UserName),
			UserID:      event.UserID,
			UserName:    event.UserName,
			Metadata:    map[string]interface{}{"event_id": event.EventID},
			Timestamp:   event.Timestamp,
		}
	}

	if activity != nil {
		s.addActivity(activity)
		s.publishActivityEvent(activity)
	}

	msg.Ack()
}

func (s *CommentActivityService) generateUpdateDescription(changes map[string]interface{}, userName string) string {
	if len(changes) == 0 {
		return fmt.Sprintf("Ticket updated by %s", userName)
	}

	var descriptions []string
	for field, value := range changes {
		switch field {
		case "status":
			if changeMap, ok := value.(map[string]interface{}); ok {
				old := changeMap["old"]
				new := changeMap["new"]
				descriptions = append(descriptions, fmt.Sprintf("changed status from %v to %v", old, new))
			}
		case "priority":
			if changeMap, ok := value.(map[string]interface{}); ok {
				old := changeMap["old"]
				new := changeMap["new"]
				descriptions = append(descriptions, fmt.Sprintf("changed priority from %v to %v", old, new))
			}
		case "assignee":
			if changeMap, ok := value.(map[string]interface{}); ok {
				old := changeMap["old"]
				new := changeMap["new"]
				if old == nil || old == "" {
					descriptions = append(descriptions, fmt.Sprintf("assigned to %v", new))
				} else if new == nil || new == "" {
					descriptions = append(descriptions, fmt.Sprintf("unassigned from %v", old))
				} else {
					descriptions = append(descriptions, fmt.Sprintf("reassigned from %v to %v", old, new))
				}
			}
		case "title":
			descriptions = append(descriptions, "updated title")
		case "description":
			descriptions = append(descriptions, "updated description")
		default:
			descriptions = append(descriptions, fmt.Sprintf("updated %s", field))
		}
	}

	if len(descriptions) == 1 {
		return fmt.Sprintf("%s %s", userName, descriptions[0])
	}

	return fmt.Sprintf("%s %s", userName, strings.Join(descriptions, ", "))
}

func (s *CommentActivityService) addActivity(activity *Activity) {
	s.mu.Lock()
	if s.activities[activity.TenantID] == nil {
		s.activities[activity.TenantID] = []*Activity{}
	}
	s.activities[activity.TenantID] = append(s.activities[activity.TenantID], activity)
	s.mu.Unlock()

	// Store activity log in Object Store (async, don't block on errors)
	go func() {
		if err := s.storeActivityLogInObjectStore(activity.TenantID, activity.TicketID); err != nil {
			log.Printf("[Tenant: %s] WARNING: Failed to store activity log in Object Store: %v", activity.TenantID, err)
		}
	}()
}

// storeCommentInObjectStore stores a comment in tenant-scoped Object Store
// Object naming: tenant.{tenant_id}.comments.{ticket_id}.{comment_id}
func (s *CommentActivityService) storeCommentInObjectStore(comment *Comment) error {
	if s.objStore == nil {
		return nil // Object Store not available, skip storage
	}

	objectKey := fmt.Sprintf("tenant.%s.comments.%s.%s", comment.TenantID, comment.TicketID, comment.ID)
	jsonData, err := json.Marshal(comment)
	if err != nil {
		return fmt.Errorf("failed to marshal comment: %w", err)
	}

	_, err = s.objStore.PutBytes(objectKey, jsonData)
	if err != nil {
		return fmt.Errorf("failed to store comment in Object Store: %w", err)
	}

	log.Printf("[Tenant: %s] Stored comment in Object Store: %s (size: %d bytes)",
		comment.TenantID, objectKey, len(jsonData))
	return nil
}

// storeActivityLogInObjectStore stores activity log in tenant-scoped Object Store
// Object naming: tenant.{tenant_id}.activities.{ticket_id}
func (s *CommentActivityService) storeActivityLogInObjectStore(tenantID string, ticketID string) error {
	if s.objStore == nil {
		return nil // Object Store not available, skip storage
	}

	// Gather all activities for this ticket
	s.mu.RLock()
	var activities []*Activity
	if tenantActivities, exists := s.activities[tenantID]; exists {
		for _, activity := range tenantActivities {
			if activity.TicketID == ticketID {
				activities = append(activities, activity)
			}
		}
	}
	s.mu.RUnlock()

	if len(activities) == 0 {
		return nil // No activities to store
	}

	objectKey := fmt.Sprintf("tenant.%s.activities.%s", tenantID, ticketID)
	jsonData, err := json.Marshal(activities)
	if err != nil {
		return fmt.Errorf("failed to marshal activities: %w", err)
	}

	_, err = s.objStore.PutBytes(objectKey, jsonData)
	if err != nil {
		return fmt.Errorf("failed to store activities in Object Store: %w", err)
	}

	log.Printf("[Tenant: %s] Stored activity log in Object Store: %s (count: %d, size: %d bytes)",
		tenantID, objectKey, len(activities), len(jsonData))
	return nil
}

func (s *CommentActivityService) publishCommentEvent(comment *Comment) {
	eventData := map[string]interface{}{
		"event_type": "comment.created",
		"comment":    comment,
		"timestamp":  time.Now().UTC(),
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		log.Printf("[Tenant: %s] Failed to marshal comment event: %v", comment.TenantID, err)
		return
	}

	// Publish to tenant-scoped subject: tenant.{id}.comment.added
	subject := fmt.Sprintf("tenant.%s.comment.added", comment.TenantID)
	if _, err := s.js.Publish(subject, data); err != nil {
		log.Printf("[Tenant: %s] Failed to publish comment event: %v", comment.TenantID, err)
	} else {
		log.Printf("[Tenant: %s] Published comment event to subject: %s", comment.TenantID, subject)
	}
}

func (s *CommentActivityService) publishActivityEvent(activity *Activity) {
	eventData := map[string]interface{}{
		"event_type": "activity.created",
		"activity":   activity,
		"timestamp":  time.Now().UTC(),
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		log.Printf("[Tenant: %s] Failed to marshal activity event: %v", activity.TenantID, err)
		return
	}

	// Publish to tenant-scoped subject: tenant.{id}.activity.created
	subject := fmt.Sprintf("tenant.%s.activity.created", activity.TenantID)
	if _, err := s.js.Publish(subject, data); err != nil {
		log.Printf("[Tenant: %s] Failed to publish activity event: %v", activity.TenantID, err)
	} else {
		log.Printf("[Tenant: %s] Published activity event to subject: %s", activity.TenantID, subject)
	}
}

func (s *CommentActivityService) respondWithSuccess(msg *nats.Msg, data interface{}) {
	response := ServiceResponse{
		Success: true,
		Data:    data,
	}

	responseData, err := json.Marshal(response)
	if err != nil {
		log.Printf("Failed to marshal success response: %v", err)
		return
	}

	if err := msg.Respond(responseData); err != nil {
		log.Printf("Failed to send success response: %v", err)
	}
}

func (s *CommentActivityService) respondWithError(msg *nats.Msg, errorType, message string) {
	response := ServiceResponse{
		Success: false,
		Error:   fmt.Sprintf("%s: %s", errorType, message),
	}

	responseData, err := json.Marshal(response)
	if err != nil {
		log.Printf("Failed to marshal error response: %v", err)
		return
	}

	if err := msg.Respond(responseData); err != nil {
		log.Printf("Failed to send error response: %v", err)
	}
}

func (s *CommentActivityService) Shutdown() {
	close(s.shutdownCh)

	// Close NATS connection
	if s.natsConn != nil {
		s.natsConn.Close()
	}

	// Wait for all goroutines to finish
	s.wg.Wait()
	log.Println("Comment & Activity service shut down complete")
}

// Helper function to marshal interface{} to JSON bytes
func jsonMarshal(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}
