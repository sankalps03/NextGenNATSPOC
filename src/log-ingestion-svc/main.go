package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net"
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

type LogIngestionService struct {
	natsConn    *nats.Conn
	js          jetstream.JetStream
	objStore    jetstream.ObjectStore
	tcpListener net.Listener
	udpConn     *net.UDPConn
	tlsConfig   *tls.Config
	serviceName string
	shutdownCh  chan struct{}
	wg          sync.WaitGroup
}

type LogEntry struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	Source    string            `json:"source"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata"`
	Timestamp time.Time         `json:"timestamp"`
}

type Config struct {
	NATSURLs    []string
	ServiceName string
	TCPPort     string
	UDPPort     string
	TLSCertFile string
	TLSKeyFile  string
	CACertFile  string
	LogLevel    string
}

func main() {
	config := loadConfig()

	service, err := NewLogIngestionService(config)
	if err != nil {
		log.Fatalf("Failed to create log ingestion service: %v", err)
	}

	if err := service.Start(); err != nil {
		log.Fatalf("Failed to start service: %v", err)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down log ingestion service...")
	service.Shutdown()
}

func loadConfig() Config {
	natsURLs := strings.Split(getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"), ",")

	return Config{
		NATSURLs:    natsURLs,
		ServiceName: getEnv("SERVICE_NAME", "log-ingestion-service"),
		TCPPort:     getEnv("TCP_PORT", "9090"),
		UDPPort:     getEnv("UDP_PORT", "9091"),
		TLSCertFile: getEnv("TLS_CERT_FILE", "./certs/server.crt"),
		TLSKeyFile:  getEnv("TLS_KEY_FILE", "./certs/server.key"),
		CACertFile:  getEnv("CA_CERT_FILE", "./certs/ca.crt"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func NewLogIngestionService(config Config) (*LogIngestionService, error) {
	// Setup TLS configuration for mutual TLS
	tlsConfig, err := setupTLSConfig(config.TLSCertFile, config.TLSKeyFile, config.CACertFile)
	if err != nil {
		return nil, fmt.Errorf("failed to setup TLS: %w", err)
	}

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
	js, err := jetstream.New(natsConn)
	if err != nil {
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	// Ensure stream exists
	if err := ensureStream(js); err != nil {
		return nil, fmt.Errorf("failed to ensure stream: %w", err)
	}

	// Initialize Object Store for raw logs
	objStore, err := js.CreateOrUpdateObjectStore(context.Background(), jetstream.ObjectStoreConfig{
		Bucket:      "raw_logs",
		Description: "Object store for raw log storage",
		TTL:         48 * time.Hour,
		Storage:     jetstream.FileStorage,
		Replicas:    1,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Object Store: %w", err)
	}

	service := &LogIngestionService{
		natsConn:    natsConn,
		js:          js,
		objStore:    objStore,
		tlsConfig:   tlsConfig,
		serviceName: config.ServiceName,
		shutdownCh:  make(chan struct{}),
	}

	return service, nil
}

func setupTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	// Load server certificate
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	// Load CA certificate
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}

	return tlsConfig, nil
}

func ensureStream(js jetstream.JetStream) error {
	streamName := "LOG_EVENTS"
	ctx := context.Background()

	// Check if stream exists
	_, err := js.Stream(ctx, streamName)
	if err != nil {
		// Stream doesn't exist, create it
		_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:     streamName,
			Subjects: []string{"log.raw", "log.batch", "log.agent"},
			Storage:  jetstream.FileStorage,
			MaxMsgs:  1000000,
			MaxAge:   24 * time.Hour,
		})
		if err != nil {
			return fmt.Errorf("failed to create stream: %w", err)
		}
		log.Printf("Created JetStream stream: %s", streamName)
	}

	return nil
}

func extractTenantFromCert(cert *x509.Certificate) string {
	// Extract tenant ID from certificate CN or SAN
	// Format: tenant-{tenantid} or direct tenant ID
	cn := cert.Subject.CommonName
	if strings.HasPrefix(cn, "tenant-") {
		return strings.TrimPrefix(cn, "tenant-")
	}

	// Fallback to organizational unit
	if len(cert.Subject.OrganizationalUnit) > 0 {
		return cert.Subject.OrganizationalUnit[0]
	}

	return cn
}

func (s *LogIngestionService) publishLogEntry(logEntry LogEntry) error {
	startTime := time.Now()
	correlationID := logEntry.Metadata["correlation_id"]

	log.Printf("INFO: Publishing log entry tenant=%s source=%s size=%d log_id=%s correlation_id=%s",
		logEntry.TenantID, logEntry.Source, len(logEntry.Content), logEntry.ID, correlationID)

	// Store raw log in Object Store first
	objectName := fmt.Sprintf("%s/%s/%d_%s", logEntry.TenantID, time.Now().Format("2006/01/02"), time.Now().UnixNano(), logEntry.ID)
	objectMeta := jetstream.ObjectMeta{
		Name: objectName,
		Headers: map[string][]string{
			"tenant-id":      {logEntry.TenantID},
			"correlation-id": {correlationID},
			"source":         {logEntry.Source},
			"log-id":         {logEntry.ID},
			"ingestion-time": {time.Now().Format(time.RFC3339Nano)},
		},
	}
	_, err := s.objStore.Put(context.Background(), objectMeta, strings.NewReader(logEntry.Content))
	if err != nil {
		log.Printf("ERROR: Failed to store raw log in object store: %v correlation_id=%s", err, correlationID)
		return fmt.Errorf("failed to store raw log in object store: %w", err)
	}

	// Add object store reference to metadata
	logEntry.Metadata["object_name"] = objectName
	logEntry.Metadata["object_store"] = "raw_logs"

	data, err := json.Marshal(logEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	// Publish to fixed subject with headers for tracing
	subject := "log.raw"
	msg := &nats.Msg{
		Subject: subject,
		Data:    data,
		Header: nats.Header{
			"correlation-id": {correlationID},
			"tenant-id":      {logEntry.TenantID},
			"source":         {logEntry.Source},
			"object-name":    {objectName},
			"publish-time":   {time.Now().Format(time.RFC3339Nano)},
		},
	}
	_, err = s.js.PublishMsg(context.Background(), msg)
	if err != nil {
		return fmt.Errorf("failed to publish to NATS: %w", err)
	}

	publishTime := time.Since(startTime)
	log.Printf("INFO: Published log entry successfully tenant=%s log_id=%s object_name=%s publish_time=%v correlation_id=%s",
		logEntry.TenantID, logEntry.ID, objectName, publishTime, correlationID)

	return nil
}

func (s *LogIngestionService) Start() error {
	// Start TCP server
	if err := s.startTCPServer(); err != nil {
		return fmt.Errorf("failed to start TCP server: %w", err)
	}

	// Start UDP server
	if err := s.startUDPServer(); err != nil {
		return fmt.Errorf("failed to start UDP server: %w", err)
	}

	// Start NATS agent handler
	s.startNATSAgent()

	// Start log processing with fixed subjects
	go s.startLogProcessing()

	log.Printf("Log ingestion service started successfully")
	return nil
}

func (s *LogIngestionService) startTCPServer() error {
	config := loadConfig()

	listener, err := tls.Listen("tcp", ":"+config.TCPPort, s.tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to create TCP listener: %w", err)
	}
	s.tcpListener = listener

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		log.Printf("Starting TCP server on port %s", config.TCPPort)

		for {
			conn, err := s.tcpListener.Accept()
			if err != nil {
				select {
				case <-s.shutdownCh:
					return
				default:
					log.Printf("TCP accept error: %v", err)
					continue
				}
			}

			go s.handleTCPConnection(conn)
		}
	}()

	return nil
}

func (s *LogIngestionService) handleTCPConnection(conn net.Conn) {
	defer conn.Close()

	// Extract tenant from TLS connection
	tlsConn := conn.(*tls.Conn)
	if err := tlsConn.Handshake(); err != nil {
		log.Printf("TLS handshake failed: %v", err)
		return
	}

	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		log.Printf("No client certificate provided")
		return
	}

	tenantID := extractTenantFromCert(certs[0])
	if tenantID == "" {
		log.Printf("Invalid client certificate")
		return
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		correlationID := uuid.New().String()
		logEntry := LogEntry{
			ID:       uuid.New().String(),
			TenantID: tenantID,
			Source:   "tcp",
			Content:  line,
			Metadata: map[string]string{
				"correlation_id": correlationID,
				"client_ip":      conn.RemoteAddr().String(),
				"client_cert":    certs[0].Subject.CommonName,
				"ingestion_time": time.Now().Format(time.RFC3339Nano),
			},
			Timestamp: time.Now().UTC(),
		}

		log.Printf("INFO: TCP log received tenant=%s size=%d correlation_id=%s",
			tenantID, len(line), correlationID)

		if err := s.publishLogEntry(logEntry); err != nil {
			log.Printf("Failed to publish log entry: %v", err)
		}
	}
}

func (s *LogIngestionService) startUDPServer() error {
	config := loadConfig()

	addr, err := net.ResolveUDPAddr("udp", ":"+config.UDPPort)
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to create UDP listener: %w", err)
	}
	s.udpConn = conn

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		log.Printf("Starting UDP server on port %s", config.UDPPort)

		buffer := make([]byte, 4096)
		for {
			select {
			case <-s.shutdownCh:
				return
			default:
				n, clientAddr, err := conn.ReadFromUDP(buffer)
				if err != nil {
					log.Printf("UDP read error: %v", err)
					continue
				}

				// For UDP, we can't do mutual TLS, so we need alternative auth
				// For now, assume tenant ID is in the first line of the message
				data := string(buffer[:n])
				s.handleUDPData(data, clientAddr.String())
			}
		}
	}()

	return nil
}

func (s *LogIngestionService) handleUDPData(data, clientAddr string) {
	lines := strings.Split(data, "\n")
	if len(lines) == 0 {
		return
	}

	// Extract tenant ID from first line (format: TENANT:tenant_id)
	tenantID := "default"
	firstLine := lines[0]
	if strings.HasPrefix(firstLine, "TENANT:") {
		parts := strings.SplitN(firstLine, ":", 2)
		if len(parts) == 2 {
			tenantID = parts[1]
			lines = lines[1:] // Remove tenant line
		}
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		correlationID := uuid.New().String()
		logEntry := LogEntry{
			ID:       uuid.New().String(),
			TenantID: tenantID,
			Source:   "udp",
			Content:  line,
			Metadata: map[string]string{
				"correlation_id": correlationID,
				"client_ip":      clientAddr,
				"ingestion_time": time.Now().Format(time.RFC3339Nano),
			},
			Timestamp: time.Now().UTC(),
		}

		log.Printf("INFO: UDP log received tenant=%s size=%d correlation_id=%s",
			tenantID, len(line), correlationID)

		if err := s.publishLogEntry(logEntry); err != nil {
			log.Printf("Failed to publish log entry: %v", err)
		}
	}
}

func (s *LogIngestionService) startNATSAgent() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		// Subscribe to agent logs with queue group for load balancing
		sub, err := s.natsConn.QueueSubscribe("log.agent", "log-ingestion", s.handleNATSAgentLog)
		if err != nil {
			log.Printf("Failed to subscribe to agent logs: %v", err)
			return
		}
		defer sub.Unsubscribe()

		log.Printf("Started NATS agent handler")
		<-s.shutdownCh
	}()
}

func (s *LogIngestionService) handleNATSAgentLog(msg *nats.Msg) {
	// Parse agent log message
	var agentLog struct {
		Content  string            `json:"content"`
		Metadata map[string]string `json:"metadata"`
		TenantID string            `json:"tenant_id"`
	}

	if err := json.Unmarshal(msg.Data, &agentLog); err != nil {
		log.Printf("Failed to unmarshal agent log: %v", err)
		return
	}

	// Use tenant ID from message payload, default to "default" if not provided
	tenantID := agentLog.TenantID
	if tenantID == "" {
		tenantID = "default"
	}

	correlationID := uuid.New().String()
	logEntry := LogEntry{
		ID:       uuid.New().String(),
		TenantID: tenantID,
		Source:   "nats_agent",
		Content:  agentLog.Content,
		Metadata: map[string]string{
			"correlation_id": correlationID,
			"ingestion_time": time.Now().Format(time.RFC3339Nano),
		},
		Timestamp: time.Now().UTC(),
	}

	// Merge agent metadata
	for k, v := range agentLog.Metadata {
		logEntry.Metadata[k] = v
	}

	log.Printf("INFO: NATS agent log received tenant=%s size=%d correlation_id=%s",
		tenantID, len(agentLog.Content), correlationID)

	if err := s.publishLogEntry(logEntry); err != nil {
		log.Printf("Failed to publish agent log: %v", err)
	}
}

func (s *LogIngestionService) startLogProcessing() {
	ctx := context.Background()

	// Create consumer for log events
	consumer, err := s.js.CreateOrUpdateConsumer(ctx, "LOG_EVENTS", jetstream.ConsumerConfig{
		Name:          "log-ingestion-processor",
		FilterSubject: "log.raw",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		log.Printf("Failed to create log processing consumer: %v", err)
		return
	}

	iter, err := consumer.Messages()
	if err != nil {
		log.Printf("Failed to get messages from log processing consumer: %v", err)
		return
	}

	log.Printf("Started log processing")

	for {
		select {
		case <-s.shutdownCh:
			iter.Stop()
			return
		default:
			msg, err := iter.Next()
			if err != nil {
				log.Printf("Error getting next log message: %v", err)
				continue
			}
			s.processLogMessage(msg)
		}
	}
}

func (s *LogIngestionService) processLogMessage(msg jetstream.Msg) {
	var logEntry LogEntry
	if err := json.Unmarshal(msg.Data(), &logEntry); err != nil {
		log.Printf("Failed to unmarshal log entry: %v", err)
		msg.Ack()
		return
	}

	log.Printf("Processing log entry from tenant %s: %s", logEntry.TenantID, logEntry.Content[:minInt(50, len(logEntry.Content))])

	// Process the log entry (could be forwarded to other systems, stored, etc.)
	// For now, just acknowledge that we processed it

	msg.Ack()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *LogIngestionService) Shutdown() {
	close(s.shutdownCh)

	// Close TCP listener
	if s.tcpListener != nil {
		s.tcpListener.Close()
	}

	// Close UDP connection
	if s.udpConn != nil {
		s.udpConn.Close()
	}

	// Close NATS connection
	if s.natsConn != nil {
		s.natsConn.Close()
	}

	// Wait for all goroutines to finish
	s.wg.Wait()
	log.Println("Log ingestion service shut down complete")
}
