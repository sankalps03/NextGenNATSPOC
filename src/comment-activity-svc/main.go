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

	service := &CommentActivityService{
		natsConn:    natsConn,
		js:          js,
		serviceName: config.ServiceName,
		shutdownCh:  make(chan struct{}),
		comments:    make(map[string]map[string]*Comment),
		activities:  make(map[string][]*Activity),
	}

	return service, nil
}

func ensureStreams(js nats.JetStreamContext) error {
	// Ensure ACTIVITY_EVENTS stream exists
	streamName := "ACTIVITY_EVENTS"
	_, err := js.StreamInfo(streamName)
	if err != nil {
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     streamName,
			Subjects: []string{"activity.created", "comment.created", "comment.updated"},
			Storage:  nats.FileStorage,
			MaxMsgs:  1000000,
			MaxAge:   7 * 24 * time.Hour, // 7 days
		})
		if err != nil {
			return fmt.Errorf("failed to create activity events stream: %w", err)
		}
		log.Printf("Created JetStream stream: %s", streamName)
	}

	return nil
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
	sub, err := s.natsConn.Subscribe("comment-activity.service", s.handleServiceRequest)
	if err != nil {
		log.Printf("Failed to subscribe to service requests: %v", err)
		return
	}
	defer sub.Unsubscribe()

	log.Printf("Subscribed to comment-activity.service")
	<-s.shutdownCh
}

func (s *CommentActivityService) subscribeToTicketEvents() {
	// Subscribe to ticket events with durable consumer
	sub, err := s.js.Subscribe("ticket.>", s.handleTicketEvent, nats.Durable("comment-activity-ticket-events"))
	if err != nil {
		log.Printf("Failed to subscribe to ticket events: %v", err)
		return
	}
	defer sub.Unsubscribe()

	log.Printf("Subscribed to ticket events")
	<-s.shutdownCh
}

func (s *CommentActivityService) handleServiceRequest(msg *nats.Msg) {
	var req ServiceRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		log.Printf("Failed to unmarshal service request: %v", err)
		s.respondWithError(msg, "invalid_request", err.Error())
		return
	}

	log.Printf("Processing request: action=%s, tenant=%s", req.Action, req.TenantID)

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

	// Store comment
	s.mu.Lock()
	if s.comments[req.TenantID] == nil {
		s.comments[req.TenantID] = make(map[string]*Comment)
	}
	s.comments[req.TenantID][comment.ID] = comment
	s.mu.Unlock()

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
}

func (s *CommentActivityService) publishCommentEvent(comment *Comment) {
	eventData := map[string]interface{}{
		"event_type": "comment.created",
		"comment":    comment,
		"timestamp":  time.Now().UTC(),
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		log.Printf("Failed to marshal comment event: %v", err)
		return
	}

	if _, err := s.js.Publish("comment.created", data); err != nil {
		log.Printf("Failed to publish comment event: %v", err)
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
		log.Printf("Failed to marshal activity event: %v", err)
		return
	}

	if _, err := s.js.Publish("activity.created", data); err != nil {
		log.Printf("Failed to publish activity event: %v", err)
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
