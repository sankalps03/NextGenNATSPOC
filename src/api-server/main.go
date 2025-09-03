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

type Tenant struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
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

type CreateTenantRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type UpdateTenantRequest struct {
	Name   *string `json:"name,omitempty"`
	Email  *string `json:"email,omitempty"`
	Status *string `json:"status,omitempty"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

type APIHandler struct {
	natsManager *NATSManager
	tenantKV    jetstream.KeyValue
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

	// Validate tenant exists first
	if !h.validateTenant(tenant) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   "invalid_tenant",
			Message: fmt.Sprintf("Tenant %s does not exist or is not active", tenant),
		})
		return
	}

	var req CreateTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid_json", Message: err.Error()})
		return
	}

	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.CreatedBy) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   "validation_failed",
			Message: "title and created_by are required and cannot be empty",
		})
		return
	}

	if len(req.Title) > 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   "validation_failed",
			Message: "title must be 200 characters or less",
		})
		return
	}

	if len(req.Description) > 2000 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   "validation_failed",
			Message: "description must be 2000 characters or less",
		})
		return
	}

	requestData := map[string]interface{}{
		"action": "create",
		"tenant": tenant,
		"data":   req,
	}

	response, err := h.sendNATSRequest("ticket.service", requestData, 5*time.Second)
	if err != nil {
		log.Printf("ERROR: Failed to communicate with ticket service: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "service_unavailable", Message: "Ticket service unavailable"})
		return
	}

	var ticket Ticket
	if err := json.Unmarshal(response, &ticket); err != nil {
		log.Printf("ERROR: Failed to unmarshal ticket response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "internal_error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(ticket)
}

func (h *APIHandler) ListTickets(w http.ResponseWriter, r *http.Request) {
	tenant := strings.ToLower(r.Context().Value(TenantContextKey).(string))

	requestData := map[string]interface{}{
		"action": "list",
		"tenant": tenant,
	}

	response, err := h.sendNATSRequest("ticket.service", requestData, 5*time.Second)
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

	response, err := h.sendNATSRequest("ticket.service", requestData, 5*time.Second)
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

	var req UpdateTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid_json", Message: err.Error()})
		return
	}

	if req.Title != nil && len(*req.Title) > 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   "validation_failed",
			Message: "title must be 200 characters or less",
		})
		return
	}

	if req.Description != nil && len(*req.Description) > 2000 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   "validation_failed",
			Message: "description must be 2000 characters or less",
		})
		return
	}

	requestData := map[string]interface{}{
		"action":    "update",
		"tenant":    tenant,
		"ticket_id": ticketID,
		"data":      req,
	}

	response, err := h.sendNATSRequest("ticket.service", requestData, 5*time.Second)
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

	response, err := h.sendNATSRequest("ticket.service", requestData, 5*time.Second)
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

	response, err := h.sendNATSRequest("notification.service", requestData, 5*time.Second)
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

	response, err := h.sendNATSRequest("notification.service", requestData, 5*time.Second)
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

func (h *APIHandler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var req CreateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid_json", Message: err.Error()})
		return
	}

	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Email) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   "validation_failed",
			Message: "name and email are required and cannot be empty",
		})
		return
	}

	tenantID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	tenant := Tenant{
		ID:        tenantID,
		Name:      req.Name,
		Email:     req.Email,
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}

	tenantData, err := json.Marshal(tenant)
	if err != nil {
		log.Printf("ERROR: Failed to marshal tenant: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "internal_error"})
		return
	}

	_, err = h.tenantKV.Put(r.Context(), tenantID, tenantData)
	if err != nil {
		log.Printf("ERROR: Failed to store tenant in KV: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "storage_error"})
		return
	}

	// Create tenant through tenant manager service
	if err := h.createTenantViaTenantManager(r.Context(), tenantID, &tenant); err != nil {
		log.Printf("ERROR: Failed to create tenant via tenant manager: %v", err)

		if deleteErr := h.tenantKV.Delete(r.Context(), tenantID); deleteErr != nil {
			log.Printf("ERROR: Failed to cleanup tenant KV entry after tenant creation failure: %v", deleteErr)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "tenant_creation_failed", Message: "Failed to create tenant resources"})
		return
	}

	// Note: Event publishing is now handled by the tenant manager service
	// No need to publish duplicate events here

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tenant)
}

func (h *APIHandler) GetTenant(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	tenantID := vars["id"]

	entry, err := h.tenantKV.Get(r.Context(), tenantID)
	if err != nil {
		if err == jetstream.ErrKeyNotFound {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "tenant_not_found", Message: "Tenant not found"})
			return
		}
		log.Printf("ERROR: Failed to get tenant from KV: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "storage_error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(entry.Value())
}

func (h *APIHandler) UpdateTenant(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	tenantID := vars["id"]

	var req UpdateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid_json", Message: err.Error()})
		return
	}

	entry, err := h.tenantKV.Get(r.Context(), tenantID)
	if err != nil {
		if err == jetstream.ErrKeyNotFound {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "tenant_not_found", Message: "Tenant not found"})
			return
		}
		log.Printf("ERROR: Failed to get tenant from KV: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "storage_error"})
		return
	}

	var tenant Tenant
	if err := json.Unmarshal(entry.Value(), &tenant); err != nil {
		log.Printf("ERROR: Failed to unmarshal tenant: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "internal_error"})
		return
	}

	if req.Name != nil {
		tenant.Name = *req.Name
	}
	if req.Email != nil {
		tenant.Email = *req.Email
	}
	if req.Status != nil {
		tenant.Status = *req.Status
	}
	tenant.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	tenantData, err := json.Marshal(tenant)
	if err != nil {
		log.Printf("ERROR: Failed to marshal updated tenant: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "internal_error"})
		return
	}

	_, err = h.tenantKV.Put(r.Context(), tenantID, tenantData)
	if err != nil {
		log.Printf("ERROR: Failed to update tenant in KV: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "storage_error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tenant)
}

func (h *APIHandler) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	tenantID := vars["id"]

	entry, err := h.tenantKV.Get(r.Context(), tenantID)
	if err != nil {
		if err == jetstream.ErrKeyNotFound {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(ErrorResponse{Error: "tenant_not_found", Message: "Tenant not found"})
			return
		}
		log.Printf("ERROR: Failed to get tenant from KV: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "storage_error"})
		return
	}

	var tenant Tenant
	if err := json.Unmarshal(entry.Value(), &tenant); err != nil {
		log.Printf("ERROR: Failed to unmarshal tenant: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "internal_error"})
		return
	}

	err = h.tenantKV.Delete(r.Context(), tenantID)
	if err != nil {
		log.Printf("ERROR: Failed to delete tenant from KV: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "storage_error"})
		return
	}

	// Delete tenant through tenant manager service
	if err := h.deleteTenantViaTenantManager(r.Context(), tenantID, &tenant); err != nil {
		log.Printf("ERROR: Failed to delete tenant via tenant manager: %v", err)
		// Continue with API response even if tenant manager call fails
	}

	// Note: Event publishing is now handled by the tenant manager service
	// No need to publish duplicate events here

	w.WriteHeader(http.StatusNoContent)
}

func (h *APIHandler) ListTenants(w http.ResponseWriter, r *http.Request) {
	keys, err := h.tenantKV.Keys(r.Context())
	if err != nil {
		log.Printf("ERROR: Failed to list tenant keys: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "storage_error"})
		return
	}

	var tenants []Tenant
	for _, key := range keys {
		entry, err := h.tenantKV.Get(r.Context(), key)
		if err != nil {
			log.Printf("WARN: Failed to get tenant %s: %v", key, err)
			continue
		}

		var tenant Tenant
		if err := json.Unmarshal(entry.Value(), &tenant); err != nil {
			log.Printf("WARN: Failed to unmarshal tenant %s: %v", key, err)
			continue
		}
		tenants = append(tenants, tenant)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tenants)
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

func (h *APIHandler) validateTenant(tenantID string) bool {
	// Validate tenant directly from tenant KV store
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := h.tenantKV.Get(ctx, tenantID)
	if err != nil {
		if err == jetstream.ErrKeyNotFound {
			log.Printf("DEBUG: Tenant %s not found in KV store", tenantID)
			return false
		}
		log.Printf("ERROR: Failed to validate tenant %s from KV store: %v", tenantID, err)
		return false
	}

	log.Printf("DEBUG: Tenant %s validated successfully from KV store", tenantID)
	return true
}

func (h *APIHandler) createTenantViaTenantManager(ctx context.Context, tenantID string, tenant *Tenant) error {
	// Create tenant creation event that tenant manager will process
	eventData := map[string]interface{}{
		"event_id":   uuid.New().String(),
		"event_type": "tenant.created",
		"tenant_id":  tenantID,
		"name":       tenant.Name,
		"email":      tenant.Email,
		"status":     "active",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	}

	eventPayload, err := json.Marshal(eventData)
	if err != nil {
		return fmt.Errorf("failed to marshal tenant creation event: %w", err)
	}

	// Publish to tenant events stream for tenant manager to process
	natsMsg := &nats.Msg{
		Subject: "tenant.created",
		Data:    eventPayload,
		Header:  make(nats.Header),
	}
	natsMsg.Header.Set("Tenant-ID", tenantID)
	natsMsg.Header.Set("Event-Type", "tenant.created")
	natsMsg.Header.Set("Content-Type", "application/json")
	natsMsg.Header.Set("Timestamp", time.Now().UTC().Format(time.RFC3339))

	if _, err := h.natsManager.js.PublishMsg(ctx, natsMsg); err != nil {
		return fmt.Errorf("failed to publish tenant creation event: %w", err)
	}

	log.Printf("Published tenant.created event for tenant: %s", tenantID)
	return nil
}

func (h *APIHandler) deleteTenantViaTenantManager(ctx context.Context, tenantID string, tenant *Tenant) error {
	// Create tenant deletion event that tenant manager will process
	eventData := map[string]interface{}{
		"event_id":   uuid.New().String(),
		"event_type": "tenant.deleted",
		"tenant_id":  tenantID,
		"name":       tenant.Name,
		"email":      tenant.Email,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	}

	eventPayload, err := json.Marshal(eventData)
	if err != nil {
		return fmt.Errorf("failed to marshal tenant deletion event: %w", err)
	}

	// Publish to tenant events stream for tenant manager to process
	natsMsg := &nats.Msg{
		Subject: "tenant.deleted",
		Data:    eventPayload,
		Header:  make(nats.Header),
	}
	natsMsg.Header.Set("Tenant-ID", tenantID)
	natsMsg.Header.Set("Event-Type", "tenant.deleted")
	natsMsg.Header.Set("Content-Type", "application/json")
	natsMsg.Header.Set("Timestamp", time.Now().UTC().Format(time.RFC3339))

	if _, err := h.natsManager.js.PublishMsg(ctx, natsMsg); err != nil {
		return fmt.Errorf("failed to publish tenant deletion event: %w", err)
	}

	log.Printf("Published tenant.deleted event for tenant: %s", tenantID)
	return nil
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

func createTenantKV(js jetstream.JetStream) (jetstream.KeyValue, error) {
	ctx := context.Background()
	bucketName := "tenants"

	kv, err := js.KeyValue(ctx, bucketName)
	if err != nil {
		if err == jetstream.ErrBucketNotFound {
			log.Printf("Creating KV bucket: %s", bucketName)
			kv, err = js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
				Bucket:      bucketName,
				Description: "Tenant management storage",
				History:     3,
				TTL:         0,
				MaxBytes:    -1,
				Storage:     jetstream.FileStorage,
				Replicas:    1,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create KV bucket %s: %w", bucketName, err)
			}
			log.Printf("Successfully created KV bucket: %s", bucketName)
		} else {
			return nil, fmt.Errorf("failed to access KV bucket %s: %w", bucketName, err)
		}
	} else {
		log.Printf("Using existing KV bucket: %s", bucketName)
	}

	return kv, nil
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
	api.Use(tenantMiddleware)

	api.HandleFunc("/tickets", handler.CreateTicket).Methods("POST")
	api.HandleFunc("/tickets", handler.ListTickets).Methods("GET")
	api.HandleFunc("/tickets/{id}", handler.GetTicket).Methods("GET")
	api.HandleFunc("/tickets/{id}", handler.UpdateTicket).Methods("PUT")
	api.HandleFunc("/tickets/{id}", handler.DeleteTicket).Methods("DELETE")

	api.HandleFunc("/notifications", handler.ListNotifications).Methods("GET")
	api.HandleFunc("/notifications/{id}", handler.GetNotification).Methods("GET")

	r.HandleFunc("/api/v1/tenants", handler.CreateTenant).Methods("POST")
	r.HandleFunc("/api/v1/tenants", handler.ListTenants).Methods("GET")
	r.HandleFunc("/api/v1/tenants/{id}", handler.GetTenant).Methods("GET")
	r.HandleFunc("/api/v1/tenants/{id}", handler.UpdateTenant).Methods("PUT")
	r.HandleFunc("/api/v1/tenants/{id}", handler.DeleteTenant).Methods("DELETE")

	return r
}

func main() {
	fmt.Println("Starting API Server...")
	config := loadConfig()

	natsManager, err := connectNATS(config.NATSUrl)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}

	tenantKV, err := createTenantKV(natsManager.js)
	if err != nil {
		log.Fatalf("Failed to create tenant KV: %v", err)
	}

	handler := &APIHandler{
		natsManager: natsManager,
		tenantKV:    tenantKV,
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
