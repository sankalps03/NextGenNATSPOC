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

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Config struct {
	NATSUrl     string
	ServiceName string
	LogLevel    string
}

type TenantManager struct {
	natsConn      *nats.Conn
	js            jetstream.JetStream
	config        *Config
	tenantStreams map[string]string               // tenantID -> streamName
	serviceCons   map[string][]jetstream.Consumer // serviceID -> consumers
	shutdownCh    chan struct{}
	mu            sync.RWMutex
}

type TenantCreatedEvent struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	TenantID  string `json:"tenant_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

type TenantDeletedEvent struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	TenantID  string `json:"tenant_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Timestamp string `json:"timestamp"`
}

func main() {
	fmt.Println("Starting Tenant Manager Service...")

	config := loadConfig()
	manager, err := NewTenantManager(config)
	if err != nil {
		log.Fatalf("Failed to create tenant manager: %v", err)
	}

	if err := manager.Start(); err != nil {
		log.Fatalf("Failed to start tenant manager: %v", err)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down tenant manager service...")
	manager.shutdown()
}

func NewTenantManager(config *Config) (*TenantManager, error) {
	// Connect to NATS
	conn, err := nats.Connect(config.NATSUrl,
		nats.Name(config.ServiceName),
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(10),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			log.Printf("NATS disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("NATS reconnected to %v", nc.ConnectedUrl())
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	manager := &TenantManager{
		natsConn:      conn,
		js:            js,
		config:        config,
		tenantStreams: make(map[string]string),
		serviceCons:   make(map[string][]jetstream.Consumer),
		shutdownCh:    make(chan struct{}),
	}

	return manager, nil
}

func (tm *TenantManager) Start() error {
	// Setup infrastructure streams
	if err := tm.setupInfrastructureStreams(); err != nil {
		return fmt.Errorf("failed to setup infrastructure streams: %w", err)
	}

	// Start tenant event listener
	go tm.startTenantEventListener()

	log.Printf("Tenant Manager Service started successfully")
	return nil
}

func (tm *TenantManager) setupInfrastructureStreams() error {
	ctx := context.Background()

	// Create TENANT_EVENTS stream for tenant lifecycle events
	_, err := tm.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        "TENANT_EVENTS",
		Description: "Stream for tenant lifecycle events (created/deleted)",
		Subjects:    []string{"tenant.created", "tenant.deleted"},
		Retention:   jetstream.InterestPolicy,
		MaxMsgs:     1000000,
		MaxAge:      24 * time.Hour * 30, // 30 days
		Storage:     jetstream.FileStorage,
		Replicas:    3,
		Discard:     jetstream.DiscardOld,
	})
	if err != nil {
		return fmt.Errorf("failed to create TENANT_EVENTS stream: %w", err)
	}

	// Create TENANT_MANAGEMENT stream for management operations
	_, err = tm.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:         "TENANT_MANAGEMENT",
		Description:  "Stream for tenant management operations",
		Subjects:     []string{"tenant.register.>", "tenant.unregister.>"},
		Retention:    jetstream.WorkQueuePolicy,
		MaxConsumers: 50,
		MaxMsgs:      100000,
		MaxAge:       24 * time.Hour * 7, // 7 days
		Storage:      jetstream.FileStorage,
		Replicas:     3,
	})
	if err != nil {
		return fmt.Errorf("failed to create TENANT_MANAGEMENT stream: %w", err)
	}

	log.Printf("Successfully created infrastructure streams")
	return nil
}

func (tm *TenantManager) createTenantStream(tenantID string) error {
	ctx := context.Background()
	streamName := fmt.Sprintf("TENANT_%s_STREAM", strings.ToUpper(tenantID))

	// Define all subjects that this tenant's stream will handle
	subjects := []string{
		fmt.Sprintf("tenant.%s.>", tenantID),
		fmt.Sprintf("tickets.%s.>", tenantID),
		fmt.Sprintf("comments.%s.>", tenantID),
		fmt.Sprintf("activities.%s.>", tenantID),
		fmt.Sprintf("notifications.%s.>", tenantID),
		fmt.Sprintf("logs.%s.>", tenantID),
		fmt.Sprintf("events.%s.>", tenantID),
	}

	streamConfig := jetstream.StreamConfig{
		Name:         streamName,
		Description:  fmt.Sprintf("Dedicated stream for tenant %s - all tenant messages flow through this stream", tenantID),
		Subjects:     subjects,
		Retention:    jetstream.WorkQueuePolicy,
		MaxConsumers: 100,                     // Allow up to 100 consumers per tenant
		MaxMsgs:      10000000,                // 10M messages
		MaxBytes:     10 * 1024 * 1024 * 1024, // 10GB
		MaxAge:       24 * time.Hour * 90,     // 90 days retention
		Storage:      jetstream.FileStorage,
		Replicas:     3,
		NoAck:        false,
		Discard:      jetstream.DiscardOld,
	}

	stream, err := tm.js.CreateOrUpdateStream(ctx, streamConfig)
	if err != nil {
		return fmt.Errorf("failed to create stream %s: %w", streamName, err)
	}

	// Track the stream
	tm.mu.Lock()
	tm.tenantStreams[tenantID] = streamName
	tm.mu.Unlock()

	log.Printf("Successfully created stream %s for tenant %s", streamName, tenantID)
	log.Printf("Stream subjects: %v", subjects)

	// Verify stream creation
	info, err := stream.Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to get stream info after creation: %w", err)
	}

	log.Printf("Stream %s created with %d subjects", streamName, len(info.Config.Subjects))
	return nil
}

func (tm *TenantManager) startTenantEventListener() {
	ctx := context.Background()

	// Create consumer for tenant events
	consumer, err := tm.js.CreateOrUpdateConsumer(ctx, "TENANT_EVENTS", jetstream.ConsumerConfig{
		Name:          "tenant-manager-events",
		FilterSubject: "tenant.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
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

	log.Printf("Started tenant event listener")

	for {
		select {
		case <-tm.shutdownCh:
			iter.Stop()
			return
		default:
			msg, err := iter.Next()
			if err != nil {
				log.Printf("Error getting next tenant event message: %v", err)
				continue
			}
			tm.handleTenantEvent(msg)
		}
	}
}

func (tm *TenantManager) handleTenantEvent(msg jetstream.Msg) {
	subject := msg.Subject()

	switch {
	case strings.Contains(subject, "tenant.created"):
		var event TenantCreatedEvent
		if err := json.Unmarshal(msg.Data(), &event); err != nil {
			log.Printf("Failed to unmarshal tenant created event: %v", err)
			msg.Ack()
			return
		}
		tm.handleTenantCreated(event)

	case strings.Contains(subject, "tenant.deleted"):
		var event TenantDeletedEvent
		if err := json.Unmarshal(msg.Data(), &event); err != nil {
			log.Printf("Failed to unmarshal tenant deleted event: %v", err)
			msg.Ack()
			return
		}
		tm.handleTenantDeleted(event)
	}

	msg.Ack()
}

func (tm *TenantManager) handleTenantCreated(event TenantCreatedEvent) {
	log.Printf("Processing tenant created event for tenant: %s", event.TenantID)

	// 1. Create dedicated JetStream stream for tenant
	if err := tm.createTenantStream(event.TenantID); err != nil {
		log.Printf("Failed to create stream for tenant %s: %v", event.TenantID, err)
		return
	}

	// 2. Notify all services about new tenant
	tm.notifyServicesOfNewTenant(event.TenantID)

	log.Printf("Successfully processed tenant created event for tenant: %s", event.TenantID)
}

func (tm *TenantManager) handleTenantDeleted(event TenantDeletedEvent) {
	log.Printf("Processing tenant deleted event for tenant: %s", event.TenantID)

	// 1. Notify all services about tenant deletion
	tm.notifyServicesOfTenantDeletion(event.TenantID)

	// 2. Delete tenant stream
	if err := tm.deleteTenantStream(event.TenantID); err != nil {
		log.Printf("Failed to delete stream for tenant %s: %v", event.TenantID, err)
	}

	// 3. Clean up internal tracking
	tm.cleanupTenantTracking(event.TenantID)

	log.Printf("Successfully processed tenant deleted event for tenant: %s", event.TenantID)
}

func (tm *TenantManager) cleanupTenantTracking(tenantID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	delete(tm.tenantStreams, tenantID)

	// Clean up any service consumers for this tenant
	for serviceID, consumers := range tm.serviceCons {
		var activeConsumers []jetstream.Consumer
		for _, consumer := range consumers {
			// This is a simplified cleanup - in production you'd want more sophisticated tracking
			activeConsumers = append(activeConsumers, consumer)
		}
		tm.serviceCons[serviceID] = activeConsumers
	}

	log.Printf("Cleaned up internal tracking for tenant %s", tenantID)
}

func (tm *TenantManager) notifyServicesOfNewTenant(tenantID string) {
	// Publish tenant registration request for each service
	services := []string{
		"ticket-service",
		"notification-service",
		"comment-activity-service",
		"log-ingestion-service",
		"log-parser-service",
	}

	for _, serviceName := range services {
		registrationData := map[string]interface{}{
			"tenant_id":   tenantID,
			"action":      "register",
			"stream_name": tm.tenantStreams[tenantID],
		}

		data, err := json.Marshal(registrationData)
		if err != nil {
			log.Printf("Failed to marshal registration data for service %s: %v", serviceName, err)
			continue
		}

		subject := fmt.Sprintf("tenant.register.%s", serviceName)
		if _, err := tm.js.Publish(context.Background(), subject, data); err != nil {
			log.Printf("Failed to publish registration request to %s: %v", serviceName, err)
		} else {
			log.Printf("Sent tenant registration request to %s for tenant %s", serviceName, tenantID)
		}
	}
}

func loadConfig() *Config {
	return &Config{
		NATSUrl:     getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"),
		ServiceName: getEnv("SERVICE_NAME", "tenant-manager-service"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}
}

func (tm *TenantManager) shutdown() {
	close(tm.shutdownCh)

	// Close NATS connection
	if tm.natsConn != nil {
		tm.natsConn.Close()
		log.Println("NATS connection closed")
	}

	log.Println("Tenant manager service shut down complete")
}

func (tm *TenantManager) deleteTenantStream(tenantID string) error {
	ctx := context.Background()

	tm.mu.RLock()
	streamName, exists := tm.tenantStreams[tenantID]
	tm.mu.RUnlock()

	if !exists {
		log.Printf("Stream for tenant %s not found in tracking", tenantID)
		// Try with expected name pattern
		streamName = fmt.Sprintf("TENANT_%s_STREAM", strings.ToUpper(tenantID))
	}

	err := tm.js.DeleteStream(ctx, streamName)
	if err != nil {
		return fmt.Errorf("failed to delete stream %s: %w", streamName, err)
	}

	tm.mu.Lock()
	delete(tm.tenantStreams, tenantID)
	tm.mu.Unlock()

	log.Printf("Successfully deleted stream %s for tenant %s", streamName, tenantID)
	return nil
}

func (tm *TenantManager) notifyServicesOfTenantDeletion(tenantID string) {
	// Publish tenant unregistration request for each service
	services := []string{
		"ticket-service",
		"notification-service",
		"comment-activity-service",
		"log-ingestion-service",
		"log-parser-service",
	}

	for _, serviceName := range services {
		unregistrationData := map[string]interface{}{
			"tenant_id": tenantID,
			"action":    "unregister",
		}

		data, err := json.Marshal(unregistrationData)
		if err != nil {
			log.Printf("Failed to marshal unregistration data for service %s: %v", serviceName, err)
			continue
		}

		subject := fmt.Sprintf("tenant.unregister.%s", serviceName)
		if _, err := tm.js.Publish(context.Background(), subject, data); err != nil {
			log.Printf("Failed to publish unregistration request to %s: %v", serviceName, err)
		} else {
			log.Printf("Sent tenant unregistration request to %s for tenant %s", serviceName, tenantID)
		}
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
