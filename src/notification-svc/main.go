package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Config struct {
	NATSUrl                   string
	ServiceName               string
	LogLevel                  string
	LogFilePath               string
	TenantHeader              string
	NotificationRetentionDays int
}

type Notification struct {
	ID          string     `json:"id"`
	Tenant      string     `json:"tenant"`
	TicketID    string     `json:"ticket_id"`
	TicketTitle string     `json:"ticket_title"`
	Severity    string     `json:"severity"`
	Message     string     `json:"message"`
	Channel     string     `json:"channel"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

type TicketEvent struct {
	Meta   EventMeta  `json:"meta"`
	Ticket TicketData `json:"data"`
}

// TicketData represents the dynamic ticket data from protobuf
type TicketData struct {
	ID        string                 `json:"id"`
	Tenant    string                 `json:"tenant"`
	CreatedAt string                 `json:"created_at"`
	UpdatedAt string                 `json:"updated_at"`
	Fields    map[string]interface{} `json:"fields"`
}

type EventMeta struct {
	EventID    string    `json:"event_id"`
	Tenant     string    `json:"tenant"`
	OccurredAt time.Time `json:"occurred_at"`
	Schema     string    `json:"schema"`
}

type NotificationEvent struct {
	Meta         EventMeta    `json:"meta"`
	Notification Notification `json:"notification"`
}

type ServiceRequest struct {
	Action         string            `json:"action"`
	Tenant         string            `json:"tenant"`
	NotificationID string            `json:"notification_id,omitempty"`
	Params         map[string]string `json:"params,omitempty"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// ResponseWithLatency wraps responses with storage latency information
type ResponseWithLatency struct {
	Data           interface{} `json:"data"`
	StorageLatency string      `json:"storage_latency_ms"`
}

// Helper functions to extract field values from dynamic ticket data
func (td *TicketData) GetStringField(fieldName string) string {
	if td.Fields == nil {
		return ""
	}

	if fieldValue, exists := td.Fields[fieldName]; exists {
		// Handle nested field structure from protobuf
		if fieldMap, ok := fieldValue.(map[string]interface{}); ok {
			if stringValue, exists := fieldMap["string_value"]; exists {
				if str, ok := stringValue.(string); ok {
					return str
				}
			}
		}
		// Handle direct string value
		if str, ok := fieldValue.(string); ok {
			return str
		}
	}
	return ""
}

func (td *TicketData) GetTitle() string {
	return td.GetStringField("title")
}

func (td *TicketData) GetDescription() string {
	return td.GetStringField("description")
}

func (td *TicketData) GetCreatedBy() string {
	return td.GetStringField("created_by")
}

const (
	StatusPending   = "pending"
	StatusDelivered = "delivered"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
)

const (
	ChannelLog = "log"
)

const (
	SeverityCritical = "critical"
	SeverityMajor    = "major"
)

type NotificationService struct {
	config             Config
	natsConn           *nats.Conn
	js                 jetstream.JetStream
	notifications      map[string]map[string]*Notification
	deduplication      map[string]map[string]time.Time
	mutex              sync.RWMutex
	templates          map[string]*template.Template
	logWriters         map[string]*lumberjack.Logger
	logWritersMutex    sync.RWMutex
	lastProcessedEvent time.Time
	shutdown           chan struct{}
}

func NewNotificationService(config Config) *NotificationService {
	return &NotificationService{
		config:             config,
		notifications:      make(map[string]map[string]*Notification),
		deduplication:      make(map[string]map[string]time.Time),
		templates:          make(map[string]*template.Template),
		logWriters:         make(map[string]*lumberjack.Logger),
		lastProcessedEvent: time.Now(),
		shutdown:           make(chan struct{}),
	}
}

func (ns *NotificationService) Start() error {
	if err := ns.loadTemplates(); err != nil {
		return fmt.Errorf("failed to load templates: %v", err)
	}

	if err := ns.connectNATS(); err != nil {
		return fmt.Errorf("failed to connect to NATS: %v", err)
	}

	if err := ns.subscribeToEvents(); err != nil {
		return fmt.Errorf("failed to subscribe to events: %v", err)
	}

	if err := ns.setupServiceSubscription(); err != nil {
		return fmt.Errorf("failed to setup service subscription: %v", err)
	}

	log.Printf("Notification service started successfully")
	return nil
}

func (ns *NotificationService) setupServiceSubscription() error {
	_, err := ns.natsConn.Subscribe("notification.service", ns.handleServiceRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to notification.service: %v", err)
	}
	log.Printf("Notification Service listening on subject: notification.service")
	return nil
}

func (ns *NotificationService) handleServiceRequest(msg *nats.Msg) {
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

	switch req.Action {
	case "list":
		response = ns.handleListNotifications(req)
	case "get":
		response = ns.handleGetNotification(req)
	default:
		response = ErrorResponse{Error: "unknown_action", Message: "Unknown action: " + req.Action}
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

func (ns *NotificationService) handleListNotifications(req ServiceRequest) interface{} {
	tenant := strings.ToLower(req.Tenant)

	page := 1
	if pageStr, exists := req.Params["page"]; exists && pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	size := 50
	if sizeStr, exists := req.Params["size"]; exists && sizeStr != "" {
		if s, err := strconv.Atoi(sizeStr); err == nil && s > 0 && s <= 100 {
			size = s
		}
	}

	status := req.Params["status"]
	channel := req.Params["channel"]

	// Measure storage latency
	storageStart := time.Now()
	ns.mutex.RLock()
	tenantNotifications, exists := ns.notifications[tenant]
	if !exists {
		ns.mutex.RUnlock()
		storageLatency := time.Since(storageStart)
		return ResponseWithLatency{
			Data: map[string]interface{}{
				"notifications": []Notification{},
				"total":         0,
				"page":          page,
				"size":          size,
				"tenant":        tenant,
			},
			StorageLatency: fmt.Sprintf("%.2f", float64(storageLatency.Nanoseconds())/1000000),
		}
	}

	var filtered []*Notification
	for _, notification := range tenantNotifications {
		if status != "" && notification.Status != status {
			continue
		}
		if channel != "" && notification.Channel != channel {
			continue
		}
		filtered = append(filtered, notification)
	}
	ns.mutex.RUnlock()
	storageLatency := time.Since(storageStart)

	total := len(filtered)
	start := (page - 1) * size
	end := start + size
	if end > total {
		end = total
	}
	if start > total {
		start = total
	}

	result := filtered[start:end]

	return ResponseWithLatency{
		Data: map[string]interface{}{
			"notifications": result,
			"total":         total,
			"page":          page,
			"size":          size,
		},
		StorageLatency: fmt.Sprintf("%.2f", float64(storageLatency.Nanoseconds())/1000000),
	}
}

func (ns *NotificationService) handleGetNotification(req ServiceRequest) interface{} {
	tenant := strings.ToLower(req.Tenant)

	// Measure storage latency
	storageStart := time.Now()
	ns.mutex.RLock()
	tenantNotifications, exists := ns.notifications[tenant]
	if !exists {
		ns.mutex.RUnlock()
		return ErrorResponse{Error: "notification_not_found"}
	}

	notification, exists := tenantNotifications[req.NotificationID]
	ns.mutex.RUnlock()
	storageLatency := time.Since(storageStart)

	if !exists {
		return ErrorResponse{Error: "notification_not_found"}
	}

	return ResponseWithLatency{
		Data:           notification,
		StorageLatency: fmt.Sprintf("%.2f", float64(storageLatency.Nanoseconds())/1000000),
	}
}

func (ns *NotificationService) loadTemplates() error {
	templateFiles := map[string]string{
		SeverityCritical: "templates/critical.tmpl",
		SeverityMajor:    "templates/major.tmpl",
		"default":        "templates/default.tmpl",
	}

	for name, file := range templateFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			log.Printf("Failed to read template %s, using default: %v", file, err)
			switch name {
			case SeverityCritical:
				content = []byte("🚨 CRITICAL: Ticket #{{.TicketID}} requires immediate attention: {{.TicketTitle}}")
			case SeverityMajor:
				content = []byte("⚠️ MAJOR: Ticket #{{.TicketID}} needs priority handling: {{.TicketTitle}}")
			default:
				content = []byte("📋 Ticket #{{.TicketID}} notification: {{.TicketTitle}}")
			}
		}

		tmpl, err := template.New(name).Parse(string(content))
		if err != nil {
			return fmt.Errorf("failed to parse template %s: %v", name, err)
		}
		ns.templates[name] = tmpl
	}

	return nil
}

func (ns *NotificationService) connectNATS() error {
	var err error

	opts := []nats.Option{
		nats.Name(ns.config.ServiceName),
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

	ns.natsConn, err = nats.Connect(ns.config.NATSUrl, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %v", err)
	}

	ns.js, err = jetstream.New(ns.natsConn)
	if err != nil {
		return fmt.Errorf("failed to access JetStream: %v", err)
	}

	ctx := context.Background()
	_, err = ns.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "EVENTS",
		Subjects: []string{"ticket.*", "notification.*"},
	})
	if err != nil {
		return fmt.Errorf("failed to create EVENTS stream: %v", err)
	}

	log.Printf("Successfully connected to NATS server: %s", ns.natsConn.ConnectedUrl())
	return nil
}

func (ns *NotificationService) subscribeToEvents() error {
	ctx := context.Background()
	subjects := []string{"ticket.create", "ticket.update", "notification.event"}

	for _, subject := range subjects {
		consumer, err := ns.js.CreateOrUpdateConsumer(ctx, "EVENTS", jetstream.ConsumerConfig{
			Durable:       "notification-service-consumer",
			FilterSubject: subject,
		})
		if err != nil {
			return fmt.Errorf("failed to create consumer for %s: %v", subject, err)
		}

		_, err = consumer.Consume(ns.handleEvent)
		if err != nil {
			return fmt.Errorf("failed to start consuming from %s: %v", subject, err)
		}
		log.Printf("Subscribed to subject: %s", subject)
	}

	return nil
}

func (ns *NotificationService) handleEvent(msg jetstream.Msg) {
	ns.lastProcessedEvent = time.Now()

	maxRetries := 5
	baseDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		err := ns.processEvent(msg)
		if err == nil {
			msg.Ack()
			return
		}

		log.Printf("Failed to process event (attempt %d/%d): %v", attempt+1, maxRetries, err)

		if attempt < maxRetries-1 {
			delay := time.Duration(1<<attempt) * baseDelay
			if delay > 3200*time.Millisecond {
				delay = 3200 * time.Millisecond
			}
			time.Sleep(delay)
		}
	}

	log.Printf("Event processing failed after max retries, sending to DLQ: %s", msg.Subject())
	msg.Ack()
}

func (ns *NotificationService) processEvent(msg jetstream.Msg) error {
	var ticketEvent TicketEvent
	if err := json.Unmarshal(msg.Data(), &ticketEvent); err != nil {
		return fmt.Errorf("failed to unmarshal ticket event: %v", err)
	}

	if !ns.shouldCreateNotification(ticketEvent) {
		log.Printf("Skipping notification for ticket %s (no critical/major keywords)", ticketEvent.Ticket.ID)
		return nil
	}

	if ns.isDuplicate(ticketEvent.Ticket.Tenant, ticketEvent.Ticket.ID) {
		log.Printf("Skipping duplicate notification for ticket %s", ticketEvent.Ticket.ID)
		return nil
	}

	notification := ns.createNotification(ticketEvent)

	ns.storeNotification(notification)

	if err := ns.deliverNotification(notification); err != nil {
		notification.Status = StatusFailed
		ns.updateNotification(notification)
		return fmt.Errorf("failed to deliver notification: %v", err)
	}

	now := time.Now()
	notification.Status = StatusDelivered
	notification.DeliveredAt = &now
	notification.UpdatedAt = now
	ns.updateNotification(notification)

	ns.publishNotificationEvent("notification.created", notification)

	return nil
}

func (ns *NotificationService) shouldCreateNotification(event TicketEvent) bool {
	description := strings.ToLower(event.Ticket.GetDescription())
	return strings.Contains(description, "critical") || strings.Contains(description, "major")
}

func (ns *NotificationService) isDuplicate(tenant, ticketID string) bool {
	ns.mutex.RLock()
	defer ns.mutex.RUnlock()

	if tenantDedup, exists := ns.deduplication[tenant]; exists {
		if lastTime, exists := tenantDedup[ticketID]; exists {
			return time.Since(lastTime) < 5*time.Minute
		}
	}
	return false
}

func (ns *NotificationService) createNotification(event TicketEvent) *Notification {
	id := uuid.New().String()
	now := time.Now()

	description := strings.ToLower(event.Ticket.GetDescription())
	severity := SeverityMajor
	if strings.Contains(description, "critical") {
		severity = SeverityCritical
	}

	message := ns.generateMessage(severity, event.Ticket.ID, event.Ticket.GetTitle())

	return &Notification{
		ID:          id,
		Tenant:      event.Ticket.Tenant,
		TicketID:    event.Ticket.ID,
		TicketTitle: event.Ticket.GetTitle(),
		Severity:    severity,
		Message:     message,
		Channel:     ChannelLog,
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func (ns *NotificationService) generateMessage(severity, ticketID, ticketTitle string) string {
	templateName := severity
	if _, exists := ns.templates[templateName]; !exists {
		templateName = "default"
	}

	tmpl := ns.templates[templateName]
	data := struct {
		TicketID    string
		TicketTitle string
	}{
		TicketID:    ticketID,
		TicketTitle: ticketTitle,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("Failed to execute template: %v", err)
		return fmt.Sprintf("Ticket #%s notification: %s", ticketID, ticketTitle)
	}

	return buf.String()
}

func (ns *NotificationService) storeNotification(notification *Notification) {
	ns.mutex.Lock()
	defer ns.mutex.Unlock()

	if _, exists := ns.notifications[notification.Tenant]; !exists {
		ns.notifications[notification.Tenant] = make(map[string]*Notification)
	}
	if _, exists := ns.deduplication[notification.Tenant]; !exists {
		ns.deduplication[notification.Tenant] = make(map[string]time.Time)
	}

	ns.notifications[notification.Tenant][notification.ID] = notification

	ns.deduplication[notification.Tenant][notification.TicketID] = time.Now()
}

func (ns *NotificationService) updateNotification(notification *Notification) {
	ns.mutex.Lock()
	defer ns.mutex.Unlock()

	if tenantNotifications, exists := ns.notifications[notification.Tenant]; exists {
		tenantNotifications[notification.ID] = notification
	}
}

func (ns *NotificationService) deliverNotification(notification *Notification) error {
	switch notification.Channel {
	case ChannelLog:
		return ns.deliverToLog(notification)
	default:
		return fmt.Errorf("unsupported delivery channel: %s", notification.Channel)
	}
}

func (ns *NotificationService) deliverToLog(notification *Notification) error {
	writer := ns.getLogWriter(notification.Tenant)

	logEntry := map[string]interface{}{
		"timestamp":       time.Now().UTC(),
		"tenant":          notification.Tenant,
		"notification_id": notification.ID,
		"ticket_id":       notification.TicketID,
		"severity":        notification.Severity,
		"message":         notification.Message,
		"status":          StatusDelivered,
		"delivered_at":    time.Now().UTC(),
	}

	logData, err := json.Marshal(logEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %v", err)
	}

	if _, err := writer.Write(append(logData, '\n')); err != nil {
		return fmt.Errorf("failed to write to log: %v", err)
	}

	return nil
}

func (ns *NotificationService) getLogWriter(tenant string) *lumberjack.Logger {
	ns.logWritersMutex.Lock()
	defer ns.logWritersMutex.Unlock()

	if writer, exists := ns.logWriters[tenant]; exists {
		return writer
	}

	logDir := filepath.Join(ns.config.LogFilePath, "notifications", tenant)
	os.MkdirAll(logDir, 0755)

	writer := &lumberjack.Logger{
		Filename:   filepath.Join(logDir, fmt.Sprintf("notifications-%s.log", time.Now().Format("2006-01-02"))),
		MaxSize:    100,
		MaxBackups: 7,
		MaxAge:     7,
		Compress:   true,
		LocalTime:  false,
	}

	ns.logWriters[tenant] = writer
	return writer
}

func (ns *NotificationService) publishNotificationEvent(subject string, notification *Notification) {
	event := NotificationEvent{
		Meta: EventMeta{
			EventID:    uuid.New().String(),
			Tenant:     notification.Tenant,
			OccurredAt: time.Now(),
			Schema:     subject + "@v1",
		},
		Notification: *notification,
	}

	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to marshal notification event: %v", err)
		return
	}

	headers := nats.Header{}
	headers.Set("tenant", notification.Tenant)
	headers.Set("schema", event.Meta.Schema)
	headers.Set("Nats-Msg-Id", event.Meta.EventID)
	headers.Set("Content-Type", "application/json")

	msg := &nats.Msg{
		Subject: subject,
		Header:  headers,
		Data:    data,
	}

	if _, err := ns.js.PublishMsg(context.Background(), msg); err != nil {
		log.Printf("Failed to publish notification event to %s: %v", subject, err)
	}
}

func (ns *NotificationService) Stop() error {
	close(ns.shutdown)

	if ns.natsConn != nil {
		ns.natsConn.Close()
	}

	ns.logWritersMutex.Lock()
	for _, writer := range ns.logWriters {
		writer.Close()
	}
	ns.logWritersMutex.Unlock()

	log.Printf("Notification service stopped")
	return nil
}

func loadConfig() Config {
	config := Config{
		NATSUrl:                   getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"),
		ServiceName:               getEnv("SERVICE_NAME", "notification-service"),
		LogLevel:                  getEnv("LOG_LEVEL", "info"),
		LogFilePath:               getEnv("LOG_FILE_PATH", "./notifications.log"),
		TenantHeader:              getEnv("X_TENANT_HEADER", "X-Tenant-ID"),
		NotificationRetentionDays: getEnvInt("NOTIFICATION_RETENTION_DAYS", 30),
	}

	return config
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func main() {
	fmt.Println("Starting Notification Service...")

	config := loadConfig()
	service := NewNotificationService(config)

	if err := service.Start(); err != nil {
		log.Fatalf("Failed to start notification service: %v", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Printf("Received shutdown signal")
	if err := service.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}
