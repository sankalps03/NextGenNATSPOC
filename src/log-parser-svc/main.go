package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type LogParserService struct {
	natsConn        *nats.Conn
	js              jetstream.JetStream
	serviceName     string
	parsers         []LogParser
	shutdownCh      chan struct{}
	wg              sync.WaitGroup
	tenantConsumers map[string]jetstream.Consumer
	mu              sync.RWMutex
}

type LogEntry struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	Source    string            `json:"source"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata"`
	Timestamp time.Time         `json:"timestamp"`
}

type ParsedLog struct {
	ID           string            `json:"id"`
	OriginalID   string            `json:"original_id"`
	TenantID     string            `json:"tenant_id"`
	Source       string            `json:"source"`
	ParsedFields map[string]string `json:"parsed_fields"`
	ParsedTime   time.Time         `json:"parsed_time"`
	LogLevel     string            `json:"log_level"`
	Message      string            `json:"message"`
	RawContent   string            `json:"raw_content"`
	ParserUsed   string            `json:"parser_used"`
	ParseStatus  string            `json:"parse_status"` // success, partial, failed
	Metadata     map[string]string `json:"metadata"`
	Timestamp    time.Time         `json:"timestamp"`
}

type LogParser interface {
	Name() string
	Parse(content string) (*ParsedLog, error)
	CanParse(content string) bool
}

// Common log patterns
type CommonLogParser struct {
	name     string
	pattern  *regexp.Regexp
	fields   []string
	timeIdx  int
	levelIdx int
	msgIdx   int
}

type JSONLogParser struct{}
type SyslogParser struct{}
type NginxLogParser struct{}
type ApacheLogParser struct{}
type CustomRegexParser struct {
	name    string
	pattern *regexp.Regexp
	fields  []string
}

type Config struct {
	NATSURLs    []string
	ServiceName string
	LogLevel    string
}

type TenantRegistrationRequest struct {
	TenantID   string `json:"tenant_id"`
	Action     string `json:"action"`
	StreamName string `json:"stream_name"`
}

func main() {
	config := loadConfig()

	service, err := NewLogParserService(config)
	if err != nil {
		log.Fatalf("Failed to create log parser service: %v", err)
	}

	if err := service.Start(); err != nil {
		log.Fatalf("Failed to start service: %v", err)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down log parser service...")
	service.Shutdown()
}

func loadConfig() Config {
	natsURLs := strings.Split(getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"), ",")

	return Config{
		NATSURLs:    natsURLs,
		ServiceName: getEnv("SERVICE_NAME", "log-parser-service"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func NewLogParserService(config Config) (*LogParserService, error) {
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

	// Ensure streams exist
	if err := ensureStreams(js); err != nil {
		return nil, fmt.Errorf("failed to ensure streams: %w", err)
	}

	service := &LogParserService{
		natsConn:        natsConn,
		js:              js,
		serviceName:     config.ServiceName,
		shutdownCh:      make(chan struct{}),
		tenantConsumers: make(map[string]jetstream.Consumer),
	}

	// Initialize parsers
	service.initializeParsers()

	return service, nil
}

func ensureStreams(js jetstream.JetStream) error {
	// Ensure PARSED_LOGS stream exists
	streamName := "PARSED_LOGS"
	ctx := context.Background()
	_, err := js.Stream(ctx, streamName)
	if err != nil {
		_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:     streamName,
			Subjects: []string{"log.parsed", "log.failed", "log.structured"},
			Storage:  jetstream.FileStorage,
			MaxMsgs:  2000000,
			MaxAge:   48 * time.Hour,
		})
		if err != nil {
			return fmt.Errorf("failed to create parsed logs stream: %w", err)
		}
		log.Printf("Created JetStream stream: %s", streamName)
	}

	return nil
}

func (s *LogParserService) initializeParsers() {
	s.parsers = []LogParser{
		&JSONLogParser{},
		&SyslogParser{},
		NewCommonLogParser("timestamp-level-message",
			`(\d{4}-\d{2}-\d{2}[\sT]\d{2}:\d{2}:\d{2}[^\s]*)\s+\[?(\w+)\]?\s+(.*)`,
			[]string{"timestamp", "level", "message"}, 0, 1, 2),
		NewCommonLogParser("apache-combined",
			`(\S+) \S+ \S+ \[(.*?)\] "(\S+) (.*?) (\S+)" (\d+) (\S+) "(.*?)" "(.*?)"`,
			[]string{"ip", "timestamp", "method", "path", "protocol", "status", "size", "referer", "user_agent"}, 1, -1, -1),
		&NginxLogParser{},
		&ApacheLogParser{},
	}
}

func (s *LogParserService) Start() error {
	// Start tenant registration listener
	go s.startTenantRegistrationListener()

	log.Printf("Log parser service started successfully")
	return nil
}

func (s *LogParserService) parseLogEntry(logEntry LogEntry) ParsedLog {
	parsedLog := ParsedLog{
		ID:           uuid.New().String(),
		OriginalID:   logEntry.ID,
		TenantID:     logEntry.TenantID,
		Source:       logEntry.Source,
		RawContent:   logEntry.Content,
		ParsedFields: make(map[string]string),
		Metadata:     logEntry.Metadata,
		Timestamp:    time.Now().UTC(),
		ParseStatus:  "failed",
	}

	// Try each parser
	for _, parser := range s.parsers {
		if parser.CanParse(logEntry.Content) {
			if parsed, err := parser.Parse(logEntry.Content); err == nil {
				// Copy parsed data
				parsedLog.ParsedFields = parsed.ParsedFields
				parsedLog.ParsedTime = parsed.ParsedTime
				parsedLog.LogLevel = parsed.LogLevel
				parsedLog.Message = parsed.Message
				parsedLog.ParserUsed = parser.Name()
				parsedLog.ParseStatus = "success"
				break
			}
		}
	}

	// If no parser worked, store as-is with failed status
	if parsedLog.ParseStatus == "failed" {
		parsedLog.Message = logEntry.Content
		parsedLog.ParsedTime = logEntry.Timestamp
		parsedLog.ParserUsed = "none"
	}

	return parsedLog
}

func (s *LogParserService) publishParsedLog(parsedLog ParsedLog) error {
	data, err := json.Marshal(parsedLog)
	if err != nil {
		return fmt.Errorf("failed to marshal parsed log: %w", err)
	}

	// Publish to tenant-specific subject
	subject := fmt.Sprintf("logs.%s.parsed", parsedLog.TenantID)
	if parsedLog.ParseStatus == "failed" {
		subject = fmt.Sprintf("logs.%s.failed", parsedLog.TenantID)
	}

	_, err = s.js.Publish(context.Background(), subject, data)
	if err != nil {
		return fmt.Errorf("failed to publish to NATS: %w", err)
	}

	return nil
}

func (s *LogParserService) publishParsedLogBatch(parsedLogs []ParsedLog) error {
	// Group logs by tenant
	tenantLogs := make(map[string][]ParsedLog)
	for _, log := range parsedLogs {
		tenantLogs[log.TenantID] = append(tenantLogs[log.TenantID], log)
	}

	// Publish each tenant's logs separately
	for tenantID, logs := range tenantLogs {
		data, err := json.Marshal(logs)
		if err != nil {
			return fmt.Errorf("failed to marshal parsed logs for tenant %s: %w", tenantID, err)
		}

		subject := fmt.Sprintf("logs.%s.structured", tenantID)
		_, err = s.js.Publish(context.Background(), subject, data)
		if err != nil {
			return fmt.Errorf("failed to publish to NATS for tenant %s: %w", tenantID, err)
		}
	}

	return nil
}

func (s *LogParserService) startTenantRegistrationListener() {
	ctx := context.Background()

	// Create consumer for tenant management events
	consumer, err := s.js.CreateOrUpdateConsumer(ctx, "TENANT_MANAGEMENT", jetstream.ConsumerConfig{
		Name:          "log-parser-tenant-registration",
		FilterSubject: "tenant.register.log-parser-service",
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
		case <-s.shutdownCh:
			iter.Stop()
			return
		default:
			msg, err := iter.Next()
			if err != nil {
				log.Printf("Error getting next tenant registration message: %v", err)
				continue
			}
			s.handleTenantRegistration(msg)
		}
	}
}

func (s *LogParserService) handleTenantRegistration(msg jetstream.Msg) {
	var req TenantRegistrationRequest
	if err := json.Unmarshal(msg.Data(), &req); err != nil {
		log.Printf("Failed to unmarshal tenant registration request: %v", err)
		msg.Ack()
		return
	}

	log.Printf("Processing tenant registration: %s action for tenant %s", req.Action, req.TenantID)

	switch req.Action {
	case "register":
		if err := s.createTenantConsumer(req.TenantID, req.StreamName); err != nil {
			log.Printf("Failed to create tenant consumer for %s: %v", req.TenantID, err)
		} else {
			log.Printf("Successfully created consumer for tenant %s", req.TenantID)
		}
	case "unregister":
		if err := s.removeTenantConsumer(req.TenantID); err != nil {
			log.Printf("Failed to remove tenant consumer for %s: %v", req.TenantID, err)
		} else {
			log.Printf("Successfully removed consumer for tenant %s", req.TenantID)
		}
	default:
		log.Printf("Unknown tenant registration action: %s", req.Action)
	}

	msg.Ack()
}

func (s *LogParserService) createTenantConsumer(tenantID, streamName string) error {
	ctx := context.Background()
	consumerName := fmt.Sprintf("log-parser-%s", tenantID)

	// Create consumer for tenant's raw log events
	consumer, err := s.js.CreateOrUpdateConsumer(ctx, streamName, jetstream.ConsumerConfig{
		Name:          consumerName,
		FilterSubject: fmt.Sprintf("logs.%s.raw", tenantID),
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		return fmt.Errorf("failed to create consumer for tenant %s: %w", tenantID, err)
	}

	// Store the consumer
	s.mu.Lock()
	s.tenantConsumers[tenantID] = consumer
	s.mu.Unlock()

	// Start processing raw logs for this tenant
	go s.processTenantRawLogs(tenantID, consumer)

	return nil
}

func (s *LogParserService) removeTenantConsumer(tenantID string) error {
	s.mu.Lock()
	consumer, exists := s.tenantConsumers[tenantID]
	if exists {
		delete(s.tenantConsumers, tenantID)
	}
	s.mu.Unlock()

	if exists {
		// Consumer cleanup is handled by NATS when the service shuts down
		_ = consumer // Acknowledge the consumer variable
		log.Printf("Removed consumer tracking for tenant %s", tenantID)
	}

	return nil
}

func (s *LogParserService) processTenantRawLogs(tenantID string, consumer jetstream.Consumer) {
	iter, err := consumer.Messages()
	if err != nil {
		log.Printf("Failed to get messages from tenant %s raw log consumer: %v", tenantID, err)
		return
	}

	log.Printf("Started processing raw logs for tenant %s", tenantID)

	for {
		select {
		case <-s.shutdownCh:
			iter.Stop()
			return
		default:
			msg, err := iter.Next()
			if err != nil {
				log.Printf("Error getting next raw log message for tenant %s: %v", tenantID, err)
				continue
			}
			s.processTenantRawLogMessage(tenantID, msg)
		}
	}
}

func (s *LogParserService) processTenantRawLogMessage(tenantID string, msg jetstream.Msg) {
	var logEntry LogEntry
	if err := json.Unmarshal(msg.Data(), &logEntry); err != nil {
		log.Printf("Failed to unmarshal raw log for tenant %s: %v", tenantID, err)
		msg.Ack()
		return
	}

	// Parse the log entry
	parsedLog := s.parseLogEntry(logEntry)

	// Publish parsed log
	if err := s.publishParsedLog(parsedLog); err != nil {
		log.Printf("Failed to publish parsed log for tenant %s: %v", tenantID, err)
		// Don't ack if we can't publish
		return
	}

	msg.Ack()
}

func (s *LogParserService) Shutdown() {
	close(s.shutdownCh)

	// Close NATS connection
	if s.natsConn != nil {
		s.natsConn.Close()
	}

	// Wait for all goroutines to finish
	s.wg.Wait()
	log.Println("Log parser service shut down complete")
}

// Parser implementations

func NewCommonLogParser(name, pattern string, fields []string, timeIdx, levelIdx, msgIdx int) *CommonLogParser {
	return &CommonLogParser{
		name:     name,
		pattern:  regexp.MustCompile(pattern),
		fields:   fields,
		timeIdx:  timeIdx,
		levelIdx: levelIdx,
		msgIdx:   msgIdx,
	}
}

func (p *CommonLogParser) Name() string {
	return p.name
}

func (p *CommonLogParser) CanParse(content string) bool {
	return p.pattern.MatchString(content)
}

func (p *CommonLogParser) Parse(content string) (*ParsedLog, error) {
	matches := p.pattern.FindStringSubmatch(content)
	if matches == nil {
		return nil, fmt.Errorf("pattern did not match")
	}

	parsedLog := &ParsedLog{
		ParsedFields: make(map[string]string),
	}

	// Extract fields
	for i, field := range p.fields {
		if i+1 < len(matches) {
			parsedLog.ParsedFields[field] = matches[i+1]
		}
	}

	// Extract timestamp
	if p.timeIdx >= 0 && p.timeIdx+1 < len(matches) {
		if parsedTime, err := parseTimestamp(matches[p.timeIdx+1]); err == nil {
			parsedLog.ParsedTime = parsedTime
		}
	}

	// Extract log level
	if p.levelIdx >= 0 && p.levelIdx+1 < len(matches) {
		parsedLog.LogLevel = strings.ToUpper(matches[p.levelIdx+1])
	}

	// Extract message
	if p.msgIdx >= 0 && p.msgIdx+1 < len(matches) {
		parsedLog.Message = matches[p.msgIdx+1]
	} else {
		parsedLog.Message = content
	}

	return parsedLog, nil
}

func (p *JSONLogParser) Name() string {
	return "json"
}

func (p *JSONLogParser) CanParse(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "{") && strings.HasSuffix(content, "}")
}

func (p *JSONLogParser) Parse(content string) (*ParsedLog, error) {
	var jsonData map[string]interface{}
	if err := json.Unmarshal([]byte(content), &jsonData); err != nil {
		return nil, err
	}

	parsedLog := &ParsedLog{
		ParsedFields: make(map[string]string),
	}

	// Extract common fields
	for key, value := range jsonData {
		parsedLog.ParsedFields[key] = fmt.Sprintf("%v", value)

		// Special handling for common fields
		switch strings.ToLower(key) {
		case "timestamp", "time", "@timestamp":
			if timeStr, ok := value.(string); ok {
				if parsedTime, err := parseTimestamp(timeStr); err == nil {
					parsedLog.ParsedTime = parsedTime
				}
			}
		case "level", "severity", "loglevel":
			parsedLog.LogLevel = strings.ToUpper(fmt.Sprintf("%v", value))
		case "message", "msg", "text":
			parsedLog.Message = fmt.Sprintf("%v", value)
		}
	}

	if parsedLog.Message == "" {
		parsedLog.Message = content
	}

	return parsedLog, nil
}

func (p *SyslogParser) Name() string {
	return "syslog"
}

func (p *SyslogParser) CanParse(content string) bool {
	// Basic syslog pattern: <priority>timestamp hostname process[pid]: message
	syslogPattern := regexp.MustCompile(`^<\d+>`)
	return syslogPattern.MatchString(content)
}

func (p *SyslogParser) Parse(content string) (*ParsedLog, error) {
	// RFC3164 syslog pattern
	pattern := regexp.MustCompile(`^<(\d+)>(\w+\s+\d+\s+\d+:\d+:\d+)\s+(\S+)\s+(\S+)(?:\[(\d+)\])?\s*:\s*(.*)`)
	matches := pattern.FindStringSubmatch(content)

	if matches == nil {
		return nil, fmt.Errorf("invalid syslog format")
	}

	parsedLog := &ParsedLog{
		ParsedFields: make(map[string]string),
	}

	// Parse priority
	if len(matches) > 1 {
		if priority, err := strconv.Atoi(matches[1]); err == nil {
			facility := priority / 8
			severity := priority % 8
			parsedLog.ParsedFields["priority"] = matches[1]
			parsedLog.ParsedFields["facility"] = strconv.Itoa(facility)
			parsedLog.ParsedFields["severity"] = strconv.Itoa(severity)

			// Map severity to log level
			severityMap := []string{"EMERG", "ALERT", "CRIT", "ERR", "WARNING", "NOTICE", "INFO", "DEBUG"}
			if severity < len(severityMap) {
				parsedLog.LogLevel = severityMap[severity]
			}
		}
	}

	// Parse timestamp
	if len(matches) > 2 {
		parsedLog.ParsedFields["syslog_timestamp"] = matches[2]
		if parsedTime, err := parseTimestamp(matches[2]); err == nil {
			parsedLog.ParsedTime = parsedTime
		}
	}

	// Parse hostname
	if len(matches) > 3 {
		parsedLog.ParsedFields["hostname"] = matches[3]
	}

	// Parse process
	if len(matches) > 4 {
		parsedLog.ParsedFields["process"] = matches[4]
	}

	// Parse PID
	if len(matches) > 5 && matches[5] != "" {
		parsedLog.ParsedFields["pid"] = matches[5]
	}

	// Parse message
	if len(matches) > 6 {
		parsedLog.Message = matches[6]
	}

	return parsedLog, nil
}

func (p *NginxLogParser) Name() string {
	return "nginx"
}

func (p *NginxLogParser) CanParse(content string) bool {
	// Nginx access log pattern
	nginxPattern := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+ - - \[`)
	return nginxPattern.MatchString(content)
}

func (p *NginxLogParser) Parse(content string) (*ParsedLog, error) {
	// Nginx combined log format
	pattern := regexp.MustCompile(`^(\S+) \S+ \S+ \[(.*?)\] "(\S+) (.*?) (\S+)" (\d+) (\S+) "(.*?)" "(.*?)"`)
	matches := pattern.FindStringSubmatch(content)

	if matches == nil {
		return nil, fmt.Errorf("invalid nginx log format")
	}

	parsedLog := &ParsedLog{
		ParsedFields: make(map[string]string),
		Message:      content,
	}

	fields := []string{"ip", "timestamp", "method", "path", "protocol", "status", "size", "referer", "user_agent"}
	for i, field := range fields {
		if i+1 < len(matches) {
			parsedLog.ParsedFields[field] = matches[i+1]
		}
	}

	// Parse timestamp
	if len(matches) > 2 {
		if parsedTime, err := parseTimestamp(matches[2]); err == nil {
			parsedLog.ParsedTime = parsedTime
		}
	}

	// Set log level based on status code
	if len(matches) > 6 {
		if status, err := strconv.Atoi(matches[6]); err == nil {
			if status >= 400 {
				parsedLog.LogLevel = "ERROR"
			} else if status >= 300 {
				parsedLog.LogLevel = "WARN"
			} else {
				parsedLog.LogLevel = "INFO"
			}
		}
	}

	return parsedLog, nil
}

func (p *ApacheLogParser) Name() string {
	return "apache"
}

func (p *ApacheLogParser) CanParse(content string) bool {
	// Apache access log pattern (similar to nginx but with slight differences)
	apachePattern := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+ - - \[`)
	return apachePattern.MatchString(content)
}

func (p *ApacheLogParser) Parse(content string) (*ParsedLog, error) {
	// Apache combined log format
	pattern := regexp.MustCompile(`^(\S+) \S+ \S+ \[(.*?)\] "(\S+) (.*?) (\S+)" (\d+) (\S+) "(.*?)" "(.*?)"`)
	matches := pattern.FindStringSubmatch(content)

	if matches == nil {
		return nil, fmt.Errorf("invalid apache log format")
	}

	parsedLog := &ParsedLog{
		ParsedFields: make(map[string]string),
		Message:      content,
	}

	fields := []string{"ip", "timestamp", "method", "path", "protocol", "status", "size", "referer", "user_agent"}
	for i, field := range fields {
		if i+1 < len(matches) {
			parsedLog.ParsedFields[field] = matches[i+1]
		}
	}

	// Parse timestamp
	if len(matches) > 2 {
		if parsedTime, err := parseTimestamp(matches[2]); err == nil {
			parsedLog.ParsedTime = parsedTime
		}
	}

	// Set log level based on status code
	if len(matches) > 6 {
		if status, err := strconv.Atoi(matches[6]); err == nil {
			if status >= 400 {
				parsedLog.LogLevel = "ERROR"
			} else if status >= 300 {
				parsedLog.LogLevel = "WARN"
			} else {
				parsedLog.LogLevel = "INFO"
			}
		}
	}

	return parsedLog, nil
}

func parseTimestamp(timeStr string) (time.Time, error) {
	// Try various timestamp formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"Jan 2 15:04:05",
		"02/Jan/2006:15:04:05 -0700",
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
	}

	for _, format := range formats {
		if parsedTime, err := time.Parse(format, timeStr); err == nil {
			return parsedTime, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", timeStr)
}
