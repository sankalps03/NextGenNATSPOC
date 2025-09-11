package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"bytes"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"io"
)

type Config struct {
	NATSUrl      string
	Port         string
	ServiceName  string
	LogLevel     string
	TenantHeader string
}

type NATSManager struct {
	conn     *nats.Conn
	js       jetstream.JetStream
	kv       jetstream.KeyValue
	objStore jetstream.ObjectStore
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
	ParseStatus  string            `json:"parse_status"`
	Metadata     map[string]string `json:"metadata"`
	Timestamp    time.Time         `json:"timestamp"`
}

type LogsResponse struct {
	Logs  interface{} `json:"logs"`
	Total int         `json:"total"`
	Page  int         `json:"page"`
	Size  int         `json:"size"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

type APIHandler struct {
	natsManager *NATSManager
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := uuid.New().String()
		w.Header().Set("X-Correlation-ID", correlationID)

		start := time.Now()
		next.ServeHTTP(w, r)

		log.Printf("method=%s path=%s correlation_id=%s duration=%v",
			r.Method, r.URL.Path, correlationID, time.Since(start))
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Tenant-ID")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (h *APIHandler) GetRawLogs(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	tenantID := r.URL.Query().Get("tenant_id")
	pageStr := r.URL.Query().Get("page")
	sizeStr := r.URL.Query().Get("size")
	source := r.URL.Query().Get("source")

	// Set defaults
	page := 1
	size := 50

	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	if sizeStr != "" {
		if s, err := strconv.Atoi(sizeStr); err == nil && s > 0 && s <= 1000 {
			size = s
		}
	}

	// Create consumer to read raw logs from JetStream
	ctx := context.Background()
	consumer, err := h.natsManager.js.CreateOrUpdateConsumer(ctx, "LOG_EVENTS", jetstream.ConsumerConfig{
		Name:          "api-raw-logs-reader",
		FilterSubject: "log.raw",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		log.Printf("ERROR: Failed to create raw logs consumer: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Unable to access log storage"})
		return
	}

	// Fetch logs
	logs := []LogEntry{}
	iter, err := consumer.Messages()
	if err != nil {
		log.Printf("ERROR: Failed to get messages iterator: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable"})
		return
	}

	// Collect logs with pagination
	count := 0
	skip := (page - 1) * size
	collected := 0

	for {
		msg, err := iter.Next()
		if err != nil {
			break
		}

		var logEntry LogEntry
		if err := json.Unmarshal(msg.Data(), &logEntry); err != nil {
			msg.Ack()
			continue
		}

		// Apply filters
		if tenantID != "" && logEntry.TenantID != tenantID {
			msg.Ack()
			continue
		}

		if source != "" && logEntry.Source != source {
			msg.Ack()
			continue
		}

		if count < skip {
			count++
			msg.Ack()
			continue
		}

		if collected >= size {
			msg.Ack()
			break
		}

		logs = append(logs, logEntry)
		collected++
		count++
		msg.Ack()
	}

	iter.Stop()

	response := LogsResponse{
		Logs:  logs,
		Total: len(logs),
		Page:  page,
		Size:  size,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *APIHandler) IngestRawLogs(w http.ResponseWriter, r *http.Request) {
	correlationID := uuid.New().String()
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	log.Printf("INFO: Raw log ingestion request tenant=%s correlation_id=%s", tenantID, correlationID)

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("ERROR: Failed to read request body: %v correlation_id=%s", err, correlationID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "bad_request", Message: "Unable to read request body"})
		return
	}

	// Store raw logs in Object Store
	objectName := fmt.Sprintf("%s/%s/%d", tenantID, time.Now().Format("2006/01/02"), time.Now().UnixNano())
	objectMeta := jetstream.ObjectMeta{
		Name: objectName,
		Headers: map[string][]string{
			"tenant-id":      {tenantID},
			"correlation-id": {correlationID},
			"content-type":   {r.Header.Get("Content-Type")},
			"ingestion-time": {time.Now().Format(time.RFC3339)},
		},
	}
	_, err = h.natsManager.objStore.Put(context.Background(), objectMeta, bytes.NewReader(body))
	if err != nil {
		log.Printf("ERROR: Failed to store raw log in object store: %v correlation_id=%s", err, correlationID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "storage_error", Message: "Failed to store raw log"})
		return
	}

	// Create log entry for processing
	logEntry := LogEntry{
		ID:       uuid.New().String(),
		TenantID: tenantID,
		Source:   "rest_api",
		Content:  string(body),
		Metadata: map[string]string{
			"correlation_id": correlationID,
			"object_name":    objectName,
			"client_ip":      r.RemoteAddr,
			"user_agent":     r.Header.Get("User-Agent"),
			"content_type":   r.Header.Get("Content-Type"),
		},
		Timestamp: time.Now().UTC(),
	}

	// Publish to JetStream for processing
	data, err := json.Marshal(logEntry)
	if err != nil {
		log.Printf("ERROR: Failed to marshal log entry: %v correlation_id=%s", err, correlationID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "serialization_error"})
		return
	}

	_, err = h.natsManager.js.Publish(context.Background(), "log.raw", data)
	if err != nil {
		log.Printf("ERROR: Failed to publish to JetStream: %v correlation_id=%s", err, correlationID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "publish_error", Message: "Failed to queue log for processing"})
		return
	}

	log.Printf("INFO: Raw log ingested successfully tenant=%s log_id=%s correlation_id=%s", tenantID, logEntry.ID, correlationID)

	response := map[string]string{
		"status":         "accepted",
		"log_id":         logEntry.ID,
		"correlation_id": correlationID,
		"object_name":    objectName,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Correlation-ID", correlationID)
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(response)
}

func (h *APIHandler) GetParsedLogs(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	tenantID := r.URL.Query().Get("tenant_id")
	pageStr := r.URL.Query().Get("page")
	sizeStr := r.URL.Query().Get("size")
	logLevel := r.URL.Query().Get("log_level")
	parseStatus := r.URL.Query().Get("parse_status")
	source := r.URL.Query().Get("source")
	startTime := r.URL.Query().Get("start_time")
	endTime := r.URL.Query().Get("end_time")

	// Set defaults
	page := 1
	size := 50

	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	if sizeStr != "" {
		if s, err := strconv.Atoi(sizeStr); err == nil && s > 0 && s <= 1000 {
			size = s
		}
	}

	log.Printf("INFO: Parsed logs query tenant=%s source=%s level=%s status=%s page=%d size=%d",
		tenantID, source, logLevel, parseStatus, page, size)

	// Try to get from KV store first for faster lookup by source
	if source != "" && tenantID != "" {
		kvKey := fmt.Sprintf("%s:source:%s", tenantID, source)
		entry, err := h.natsManager.kv.Get(context.Background(), kvKey)
		if err == nil {
			var parsedLogs []ParsedLog
			if err := json.Unmarshal(entry.Value(), &parsedLogs); err == nil {
				// Apply additional filters
				filteredLogs := []ParsedLog{}
				for _, log := range parsedLogs {
					if logLevel != "" && log.LogLevel != strings.ToUpper(logLevel) {
						continue
					}
					if parseStatus != "" && log.ParseStatus != parseStatus {
						continue
					}
					filteredLogs = append(filteredLogs, log)
				}

				// Apply pagination
				start := (page - 1) * size
				end := start + size
				if start >= len(filteredLogs) {
					filteredLogs = []ParsedLog{}
				} else if end > len(filteredLogs) {
					filteredLogs = filteredLogs[start:]
				} else {
					filteredLogs = filteredLogs[start:end]
				}

				response := LogsResponse{
					Logs:  filteredLogs,
					Total: len(filteredLogs),
					Page:  page,
					Size:  size,
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
				return
			}
		}
	}

	// Fallback to JetStream query
	ctx := context.Background()
	consumer, err := h.natsManager.js.CreateOrUpdateConsumer(ctx, "PARSED_LOGS", jetstream.ConsumerConfig{
		Name:          "api-parsed-logs-reader",
		FilterSubject: "log.parsed",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		log.Printf("ERROR: Failed to create parsed logs consumer: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Unable to access log storage"})
		return
	}

	// Fetch logs
	logs := []ParsedLog{}
	iter, err := consumer.Messages()
	if err != nil {
		log.Printf("ERROR: Failed to get messages iterator: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable"})
		return
	}

	// Parse time filters
	var startTimeFilter, endTimeFilter time.Time
	if startTime != "" {
		startTimeFilter, _ = time.Parse(time.RFC3339, startTime)
	}
	if endTime != "" {
		endTimeFilter, _ = time.Parse(time.RFC3339, endTime)
	}

	// Collect logs with pagination
	count := 0
	skip := (page - 1) * size
	collected := 0

	for {
		msg, err := iter.Next()
		if err != nil {
			break
		}

		var parsedLog ParsedLog
		if err := json.Unmarshal(msg.Data(), &parsedLog); err != nil {
			msg.Ack()
			continue
		}

		// Apply filters
		if tenantID != "" && parsedLog.TenantID != tenantID {
			msg.Ack()
			continue
		}

		if source != "" && parsedLog.Source != source {
			msg.Ack()
			continue
		}

		if logLevel != "" && parsedLog.LogLevel != strings.ToUpper(logLevel) {
			msg.Ack()
			continue
		}

		if parseStatus != "" && parsedLog.ParseStatus != parseStatus {
			msg.Ack()
			continue
		}

		// Time range filters
		if !startTimeFilter.IsZero() && parsedLog.Timestamp.Before(startTimeFilter) {
			msg.Ack()
			continue
		}
		if !endTimeFilter.IsZero() && parsedLog.Timestamp.After(endTimeFilter) {
			msg.Ack()
			continue
		}

		if count < skip {
			count++
			msg.Ack()
			continue
		}

		if collected >= size {
			msg.Ack()
			break
		}

		logs = append(logs, parsedLog)
		collected++
		count++
		msg.Ack()
	}

	iter.Stop()

	response := LogsResponse{
		Logs:  logs,
		Total: len(logs),
		Page:  page,
		Size:  size,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *APIHandler) GetParsedLogBySource(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	source := vars["source"]
	tenantID := r.URL.Query().Get("tenant_id")

	if tenantID == "" {
		tenantID = "default"
	}

	log.Printf("INFO: Single parsed log query source=%s tenant=%s", source, tenantID)

	// Get from KV store
	kvKey := fmt.Sprintf("%s:source:%s", tenantID, source)
	entry, err := h.natsManager.kv.Get(context.Background(), kvKey)
	if err != nil {
		log.Printf("ERROR: Failed to get log from KV store: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "not_found", Message: "Log not found"})
		return
	}

	var parsedLogs []ParsedLog
	if err := json.Unmarshal(entry.Value(), &parsedLogs); err != nil {
		log.Printf("ERROR: Failed to unmarshal KV data: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "data_error"})
		return
	}

	// Return the latest log for this source
	if len(parsedLogs) > 0 {
		latestLog := parsedLogs[len(parsedLogs)-1]
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(latestLog)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "not_found", Message: "No logs found for this source"})
	}
}

func (h *APIHandler) GetParserHealth(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"parser":    map[string]interface{}{},
	}

	// Check parser service health via consumer lag
	parserConsumerInfo, err := h.natsManager.js.Consumer(ctx, "LOG_EVENTS", "log-parser-processor")
	if err != nil {
		health["status"] = "unhealthy"
		health["parser"] = map[string]interface{}{
			"status": "consumer_not_found",
			"error":  err.Error(),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(health)
		return
	}

	info, err := parserConsumerInfo.Info(ctx)
	if err != nil {
		health["status"] = "unhealthy"
		health["parser"] = map[string]interface{}{
			"status": "info_unavailable",
			"error":  err.Error(),
		}
	} else {
		lag := info.NumPending
		health["parser"] = map[string]interface{}{
			"status":           "healthy",
			"consumer_lag":     lag,
			"messages_pending": info.NumPending,
			"messages_acked":   info.NumAckPending,
			"delivered_count":  info.Delivered.Consumer,
			"last_active":      info.Delivered.Last,
		}

		// Consider unhealthy if lag is too high
		if lag > 1000 {
			health["status"] = "degraded"
			health["parser"].(map[string]interface{})["status"] = "high_lag"
		}
	}

	// Check KV store availability
	_, err = h.natsManager.kv.Status(ctx)
	if err != nil {
		health["status"] = "unhealthy"
		health["kv_store"] = map[string]interface{}{
			"status": "unavailable",
			"error":  err.Error(),
		}
	} else {
		health["kv_store"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// Check Object Store availability
	objStatus, err := h.natsManager.objStore.Status(ctx)
	if err != nil {
		health["status"] = "unhealthy"
		health["object_store"] = map[string]interface{}{
			"status": "unavailable",
			"error":  err.Error(),
		}
	} else {
		health["object_store"] = map[string]interface{}{
			"status": "healthy",
			"size":   objStatus.Size(),
		}
	}

	statusCode := http.StatusOK
	if health["status"] == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	} else if health["status"] == "degraded" {
		statusCode = http.StatusOK // Still ok, just degraded
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(health)
}

func (h *APIHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	health := map[string]string{"status": "healthy"}

	if h.natsManager.conn == nil || !h.natsManager.conn.IsConnected() {
		health["status"] = "unhealthy"
		health["nats"] = "disconnected"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func connectNATS(urls string) (*NATSManager, error) {
	serverList := strings.Split(urls, ",")
	for i, url := range serverList {
		serverList[i] = strings.TrimSpace(url)
	}

	opts := []nats.Option{
		nats.Name("api-server"),
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

	// Initialize KV store for parsed logs
	kv, err := js.CreateOrUpdateKeyValue(context.Background(), jetstream.KeyValueConfig{
		Bucket:      "parsed_logs",
		Description: "Key-Value store for parsed logs indexed by source",
		TTL:         24 * time.Hour,
		Storage:     jetstream.FileStorage,
		Replicas:    1,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create KV store: %w", err)
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

	log.Printf("Successfully connected to NATS cluster: %v (active: %s)", serverList, conn.ConnectedUrl())
	return &NATSManager{conn: conn, js: js, kv: kv, objStore: objStore}, nil
}

// Stream management is now handled by the tenant-manager-service
// These functions are no longer needed

func loadConfig() *Config {
	return &Config{
		NATSUrl:      getEnv("NATS_URL", "nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224"),
		Port:         getEnv("PORT", "8082"),
		ServiceName:  getEnv("SERVICE_NAME", "api-server"),
		LogLevel:     getEnv("LOG_LEVEL", "info"),
		TenantHeader: getEnv("X_TENANT_HEADER", "X-Tenant-ID"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func setupRouter(handler *APIHandler) *mux.Router {
	r := mux.NewRouter()

	r.Use(corsMiddleware)
	r.Use(loggingMiddleware)

	r.HandleFunc("/health", handler.HealthCheck).Methods("GET")

	api := r.PathPrefix("/api/v1").Subrouter()

	// Log retrieval endpoints
	// Raw log ingestion
	api.HandleFunc("/logs/raw", handler.IngestRawLogs).Methods("POST")
	// Log retrieval endpoints
	api.HandleFunc("/logs/raw", handler.GetRawLogs).Methods("GET")
	api.HandleFunc("/logs/parsed", handler.GetParsedLogs).Methods("GET")
	api.HandleFunc("/logs/parsed/{source}", handler.GetParsedLogBySource).Methods("GET")
	// Health and monitoring
	api.HandleFunc("/parser/health", handler.GetParserHealth).Methods("GET")

	return r
}

func main() {
	fmt.Println("Starting API Server...")
	config := loadConfig()

	natsManager, err := connectNATS(config.NATSUrl)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}

	handler := &APIHandler{
		natsManager: natsManager,
	}

	router := setupRouter(handler)

	srv := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("API Server starting on port %s", config.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	if natsManager != nil && natsManager.conn != nil {
		natsManager.conn.Close()
		log.Println("NATS connection closed")
	}

	log.Println("Server exited")
}
