package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Config struct {
	NATSUrl      string
	Port         string
	ServiceName  string
	LogLevel     string
	TenantHeader string
}

type NATSManager struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

type contextKey string

const TenantContextKey = contextKey("tenant")

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

// Removed hardcoded request structs - now using dynamic field handling

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

type APIHandler struct {
	natsManager *NATSManager
}

func tenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		tenantID := r.Header.Get("X-Tenant-ID")
		if tenantID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(ErrorResponse{
				Error:   "missing_tenant_id",
				Message: "X-Tenant-ID header is required",
			})
			return
		}

		ctx := context.WithValue(r.Context(), TenantContextKey, tenantID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := uuid.New().String()
		w.Header().Set("X-Correlation-ID", correlationID)

		start := time.Now()
		next.ServeHTTP(w, r)

		log.Printf("method=%s path=%s correlation_id=%s duration=%v tenant=%s",
			r.Method, r.URL.Path, correlationID, time.Since(start), r.Header.Get("X-Tenant-ID"))
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Tenant-ID")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (h *APIHandler) CreateTicket(w http.ResponseWriter, r *http.Request) {
	tenant := strings.ToLower(r.Context().Value(TenantContextKey).(string))

	// Accept any JSON data for dynamic field handling
	var ticketData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&ticketData); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid_json", Message: err.Error()})
		return
	}

	requestData := map[string]interface{}{
		"action": "create",
		"tenant": tenant,
		"data":   ticketData,
	}

	response, err := h.sendNATSRequest("ticket.service", requestData, 60*time.Second)
	if err != nil {
		log.Printf("ERROR: Failed to communicate with ticket service: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Ticket service unavailable"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write(response)
}

func (h *APIHandler) ListTickets(w http.ResponseWriter, r *http.Request) {
	tenant := strings.ToLower(r.Context().Value(TenantContextKey).(string))

	requestData := map[string]interface{}{
		"action": "list",
		"tenant": tenant,
	}

	response, err := h.sendNATSRequest("ticket.service", requestData, 60*time.Second)
	if err != nil {
		log.Printf("ERROR: Failed to communicate with ticket service: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Ticket service unavailable"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(response)
}

func (h *APIHandler) GetTicket(w http.ResponseWriter, r *http.Request) {
	tenant := strings.ToLower(r.Context().Value(TenantContextKey).(string))
	vars := mux.Vars(r)
	ticketID := vars["id"]

	requestData := map[string]interface{}{
		"action":    "get",
		"tenant":    tenant,
		"ticket_id": ticketID,
	}

	response, err := h.sendNATSRequest("ticket.service", requestData, 60*time.Second)
	if err != nil {
		log.Printf("ERROR: Failed to communicate with ticket service: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Ticket service unavailable"})
		return
	}

	var errorResp map[string]interface{}
	if err := json.Unmarshal(response, &errorResp); err == nil {
		if errorVal, exists := errorResp["error"]; exists && errorVal == "ticket_not_found" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write(response)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(response)
}

func (h *APIHandler) UpdateTicket(w http.ResponseWriter, r *http.Request) {
	tenant := strings.ToLower(r.Context().Value(TenantContextKey).(string))
	vars := mux.Vars(r)
	ticketID := vars["id"]

	// Accept any JSON data for dynamic field handling
	var updateData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updateData); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid_json", Message: err.Error()})
		return
	}

	requestData := map[string]interface{}{
		"action":    "update",
		"tenant":    tenant,
		"ticket_id": ticketID,
		"data":      updateData,
	}

	response, err := h.sendNATSRequest("ticket.service", requestData, 60*time.Second)
	if err != nil {
		log.Printf("ERROR: Failed to communicate with ticket service: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Ticket service unavailable"})
		return
	}

	var errorResp map[string]interface{}
	if err := json.Unmarshal(response, &errorResp); err == nil {
		if errorVal, exists := errorResp["error"]; exists && errorVal == "ticket_not_found" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write(response)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(response)
}

func (h *APIHandler) DeleteTicket(w http.ResponseWriter, r *http.Request) {
	tenant := strings.ToLower(r.Context().Value(TenantContextKey).(string))
	vars := mux.Vars(r)
	ticketID := vars["id"]

	requestData := map[string]interface{}{
		"action":    "delete",
		"tenant":    tenant,
		"ticket_id": ticketID,
	}

	response, err := h.sendNATSRequest("ticket.service", requestData, 60*time.Second)
	if err != nil {
		log.Printf("ERROR: Failed to communicate with ticket service: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Ticket service unavailable"})
		return
	}

	var errorResp map[string]interface{}
	if err := json.Unmarshal(response, &errorResp); err == nil {
		if errorVal, exists := errorResp["error"]; exists && errorVal == "ticket_not_found" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write(response)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *APIHandler) SearchTickets(w http.ResponseWriter, r *http.Request) {
	tenant := strings.ToLower(r.Context().Value(TenantContextKey).(string))

	// Parse search request from request body (supports field projection)
	var searchRequest struct {
		Conditions      []map[string]interface{} `json:"conditions"`
		ProjectedFields []string                 `json:"projected_fields,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&searchRequest); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid_json", Message: err.Error()})
		return
	}

	// Check for fields parameter in query string (alternative to JSON body)
	fieldsParam := r.URL.Query().Get("fields")
	if fieldsParam != "" && len(searchRequest.ProjectedFields) == 0 {
		// Parse comma-separated fields from query parameter
		searchRequest.ProjectedFields = strings.Split(fieldsParam, ",")
		// Trim whitespace from each field
		for i, field := range searchRequest.ProjectedFields {
			searchRequest.ProjectedFields[i] = strings.TrimSpace(field)
		}
	}

	// Log field projection for debugging
	if len(searchRequest.ProjectedFields) > 0 {
		log.Printf("Search request with %d projected fields: %v (core fields id, tenant, created_at, updated_at will be included automatically)", len(searchRequest.ProjectedFields), searchRequest.ProjectedFields)
	}

	requestData := map[string]interface{}{
		"action": "search",
		"tenant": tenant,
		"data": map[string]interface{}{
			"conditions":       searchRequest.Conditions,
			"projected_fields": searchRequest.ProjectedFields,
		},
	}

	response, err := h.sendNATSRequest("ticket.service", requestData, 60*time.Second)
	if err != nil {
		log.Printf("ERROR: Failed to communicate with ticket service: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Ticket service unavailable"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(response)
}

func (h *APIHandler) ListNotifications(w http.ResponseWriter, r *http.Request) {
	tenant := strings.ToLower(r.Context().Value(TenantContextKey).(string))

	pageStr := r.URL.Query().Get("page")
	sizeStr := r.URL.Query().Get("size")
	status := r.URL.Query().Get("status")
	channel := r.URL.Query().Get("channel")

	requestData := map[string]interface{}{
		"action": "list",
		"tenant": tenant,
		"params": map[string]string{
			"page":    pageStr,
			"size":    sizeStr,
			"status":  status,
			"channel": channel,
		},
	}

	response, err := h.sendNATSRequest("notification.service", requestData, 60*time.Second)
	if err != nil {
		log.Printf("ERROR: Failed to communicate with notification service: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Notification service unavailable"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(response)
}

func (h *APIHandler) GetNotification(w http.ResponseWriter, r *http.Request) {
	tenant := strings.ToLower(r.Context().Value(TenantContextKey).(string))
	vars := mux.Vars(r)
	notificationID := vars["id"]

	requestData := map[string]interface{}{
		"action":          "get",
		"tenant":          tenant,
		"notification_id": notificationID,
	}

	response, err := h.sendNATSRequest("notification.service", requestData, 60*time.Second)
	if err != nil {
		log.Printf("ERROR: Failed to communicate with notification service: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Notification service unavailable"})
		return
	}

	var errorResp map[string]interface{}
	if err := json.Unmarshal(response, &errorResp); err == nil {
		if _, exists := errorResp["error"]; exists {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write(response)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(response)
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

func (h *APIHandler) sendNATSRequest(subject string, requestData interface{}, timeout time.Duration) ([]byte, error) {
	requestPayload, err := json.Marshal(requestData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	msg, err := h.natsManager.conn.Request(subject, requestPayload, timeout)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	return msg.Data, nil
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

	log.Printf("Successfully connected to NATS cluster: %v (active: %s)", serverList, conn.ConnectedUrl())
	return &NATSManager{conn: conn, js: js}, nil
}

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
	api.Use(tenantMiddleware)

	api.HandleFunc("/tickets", handler.CreateTicket).Methods("POST")
	api.HandleFunc("/tickets", handler.ListTickets).Methods("GET")
	api.HandleFunc("/tickets/search", handler.SearchTickets).Methods("POST")
	api.HandleFunc("/tickets/{id}", handler.GetTicket).Methods("GET")
	api.HandleFunc("/tickets/{id}", handler.UpdateTicket).Methods("PUT")
	api.HandleFunc("/tickets/{id}", handler.DeleteTicket).Methods("DELETE")

	api.HandleFunc("/notifications", handler.ListNotifications).Methods("GET")
	api.HandleFunc("/notifications/{id}", handler.GetNotification).Methods("GET")

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
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
