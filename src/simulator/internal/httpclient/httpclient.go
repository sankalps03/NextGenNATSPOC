package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"time"

	"simulator/internal/config"
)

// HTTPClient provides HTTP client functionality with retry logic and connection pooling
type HTTPClient struct {
	client    *http.Client
	config    config.HTTPClientConfig
	baseURL   string
	tenantIDs []string
	rand      *rand.Rand
}

// APIResponse represents a generic API response
type APIResponse struct {
	StatusCode int                    `json:"status_code"`
	Body       map[string]interface{} `json:"body"`
	Headers    map[string]string      `json:"headers"`
	Duration   time.Duration          `json:"duration"`
	Error      string                 `json:"error,omitempty"`
}

// SearchRequest represents a search API request
type SearchRequest struct {
	Conditions      []SearchCondition `json:"conditions"`
	ProjectedFields []string          `json:"projected_fields,omitempty"`
}

// SearchCondition represents a search condition
type SearchCondition struct {
	Field    string      `json:"field"`
	Operator string      `json:"operator"`
	Value    interface{} `json:"value"`
}

// New creates a new HTTP client instance
func New(cfg config.HTTPClientConfig) *HTTPClient {
	transport := &http.Transport{
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxConnsPerHost,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
	}

	return &HTTPClient{
		client: client,
		config: cfg,
		rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// SetBaseURL sets the base URL for API requests
func (c *HTTPClient) SetBaseURL(baseURL string) {
	c.baseURL = baseURL
}

// SetTenantIDs sets the tenant IDs for API requests
func (c *HTTPClient) SetTenantIDs(tenantIDs []string) {
	c.tenantIDs = tenantIDs
}

// getRandomTenantID returns a random tenant ID from the configured list
func (c *HTTPClient) getRandomTenantID() string {
	if len(c.tenantIDs) == 0 {
		return "default-tenant"
	}
	if len(c.tenantIDs) == 1 {
		return c.tenantIDs[0]
	}
	return c.tenantIDs[c.rand.Intn(len(c.tenantIDs))]
}

// GetTenantIDs returns the list of configured tenant IDs
func (c *HTTPClient) GetTenantIDs() []string {
	return c.tenantIDs
}

// CreateTicket sends a POST request to create a new ticket
func (c *HTTPClient) CreateTicket(ctx context.Context, ticketData map[string]interface{}) (*APIResponse, error) {
	url := fmt.Sprintf("%s/api/v1/tickets", c.baseURL)
	return c.makeJSONRequest(ctx, "POST", url, ticketData)
}

// CreateTicketWithTenant sends a POST request to create a new ticket and returns the tenant ID used
func (c *HTTPClient) CreateTicketWithTenant(ctx context.Context, ticketData map[string]interface{}) (*APIResponse, string, error) {
	url := fmt.Sprintf("%s/api/v1/tickets", c.baseURL)
	tenantID := c.getRandomTenantID()
	response, err := c.makeJSONRequestWithTenant(ctx, "POST", url, ticketData, tenantID)
	return response, tenantID, err
}

// GetTicket sends a GET request to retrieve a specific ticket
func (c *HTTPClient) GetTicket(ctx context.Context, ticketID string, projectedFields []string) (*APIResponse, error) {
	url := fmt.Sprintf("%s/api/v1/tickets/%s", c.baseURL, ticketID)

	// Add projected fields as query parameters if specified
	if len(projectedFields) > 0 {
		// For simplicity, we'll add them as a comma-separated query parameter
		// In a real implementation, you might want to handle this differently
		url += "?fields=" + joinFields(projectedFields)
	}

	return c.makeRequest(ctx, "GET", url, nil)
}

// GetTicketWithTenant sends a GET request to retrieve a specific ticket with specific tenant ID
func (c *HTTPClient) GetTicketWithTenant(ctx context.Context, ticketID string, tenantID string, projectedFields []string) (*APIResponse, error) {
	url := fmt.Sprintf("%s/api/v1/tickets/%s", c.baseURL, ticketID)

	// Add projected fields as query parameters if specified
	if len(projectedFields) > 0 {
		url += "?fields=" + joinFields(projectedFields)
	}

	return c.makeRequestWithTenant(ctx, "GET", url, nil, tenantID)
}

// UpdateTicket sends a PUT request to update a specific ticket
func (c *HTTPClient) UpdateTicket(ctx context.Context, ticketID string, updateData map[string]interface{}) (*APIResponse, error) {
	url := fmt.Sprintf("%s/api/v1/tickets/%s", c.baseURL, ticketID)
	return c.makeJSONRequest(ctx, "PUT", url, updateData)
}

// UpdateTicketWithTenant sends a PUT request to update a specific ticket with specific tenant ID
func (c *HTTPClient) UpdateTicketWithTenant(ctx context.Context, ticketID string, tenantID string, updateData map[string]interface{}) (*APIResponse, error) {
	url := fmt.Sprintf("%s/api/v1/tickets/%s", c.baseURL, ticketID)
	return c.makeJSONRequestWithTenant(ctx, "PUT", url, updateData, tenantID)
}

// SearchTickets sends a POST request to search for tickets
func (c *HTTPClient) SearchTickets(ctx context.Context, searchRequest SearchRequest) (*APIResponse, error) {
	url := fmt.Sprintf("%s/api/v1/tickets/search", c.baseURL)
	return c.makeJSONRequest(ctx, "POST", url, searchRequest)
}

// SearchTicketsWithTenant sends a POST request to search for tickets with specific tenant ID
func (c *HTTPClient) SearchTicketsWithTenant(ctx context.Context, searchRequest SearchRequest, tenantID string) (*APIResponse, error) {
	url := fmt.Sprintf("%s/api/v1/tickets/search", c.baseURL)
	return c.makeJSONRequestWithTenant(ctx, "POST", url, searchRequest, tenantID)
}

// ListTickets sends a GET request to list tickets
func (c *HTTPClient) ListTickets(ctx context.Context) (*APIResponse, error) {
	url := fmt.Sprintf("%s/api/v1/tickets", c.baseURL)
	return c.makeRequest(ctx, "GET", url, nil)
}

// makeJSONRequest makes an HTTP request with JSON payload
func (c *HTTPClient) makeJSONRequest(ctx context.Context, method, url string, payload interface{}) (*APIResponse, error) {
	var body io.Reader

	if payload != nil {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal JSON payload: %w", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	return c.makeRequest(ctx, method, url, body)
}

// makeJSONRequestWithTenant makes an HTTP request with JSON payload and specific tenant ID
func (c *HTTPClient) makeJSONRequestWithTenant(ctx context.Context, method, url string, payload interface{}, tenantID string) (*APIResponse, error) {
	var body io.Reader

	if payload != nil {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal JSON payload: %w", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	return c.makeRequestWithTenant(ctx, method, url, body, tenantID)
}

// makeRequestWithTenant makes an HTTP request with specific tenant ID
func (c *HTTPClient) makeRequestWithTenant(ctx context.Context, method, url string, body io.Reader, tenantID string) (*APIResponse, error) {
	var lastErr error

	for attempt := 0; attempt < c.config.RetryAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.config.RetryDelay):
			}
		}

		response, err := c.doRequestWithTenant(ctx, method, url, body, tenantID)
		if err == nil {
			return response, nil
		}

		lastErr = err
		log.Printf("Request attempt %d failed: %v", attempt+1, err)
	}

	return nil, lastErr
}

// doRequestWithTenant performs a single HTTP request with specific tenant ID
func (c *HTTPClient) doRequestWithTenant(ctx context.Context, method, url string, body io.Reader, tenantID string) (*APIResponse, error) {
	startTime := time.Now()

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Use specific tenant ID
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse JSON response
	var jsonBody map[string]interface{}
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &jsonBody); err != nil {
			// If JSON parsing fails, store raw body as string
			jsonBody = map[string]interface{}{
				"raw_body": string(responseBody),
			}
		}
	}

	response := &APIResponse{
		StatusCode: resp.StatusCode,
		Body:       jsonBody,
		Duration:   time.Since(startTime),
	}

	// Set error message for non-2xx status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if errorMsg, exists := jsonBody["error"]; exists {
			if errorStr, ok := errorMsg.(string); ok {
				response.Error = errorStr
			}
		}
		if response.Error == "" {
			response.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}

	return response, nil
}

// makeRequest makes an HTTP request with retry logic
func (c *HTTPClient) makeRequest(ctx context.Context, method, url string, body io.Reader) (*APIResponse, error) {
	var lastErr error

	for attempt := 0; attempt <= c.config.RetryAttempts; attempt++ {
		if attempt > 0 {
			// Wait before retry
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.config.RetryDelay):
			}
			log.Printf("Retrying request (attempt %d/%d): %s %s", attempt+1, c.config.RetryAttempts+1, method, url)
		}

		response, err := c.doRequest(ctx, method, url, body)
		if err != nil {
			lastErr = err
			if attempt < c.config.RetryAttempts {
				continue
			}
			return nil, fmt.Errorf("request failed after %d attempts: %w", c.config.RetryAttempts+1, err)
		}

		// Check if we should retry based on status code
		if c.shouldRetry(response.StatusCode) && attempt < c.config.RetryAttempts {
			lastErr = fmt.Errorf("received status code %d", response.StatusCode)
			continue
		}

		return response, nil
	}

	return nil, lastErr
}

// doRequest performs a single HTTP request
func (c *HTTPClient) doRequest(ctx context.Context, method, url string, body io.Reader) (*APIResponse, error) {
	startTime := time.Now()

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Use random tenant ID for each request
	tenantID := c.getRandomTenantID()
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	duration := time.Since(startTime)

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response body as JSON
	var bodyMap map[string]interface{}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &bodyMap); err != nil {
			// If JSON parsing fails, store as raw string
			bodyMap = map[string]interface{}{
				"raw_response": string(respBody),
			}
		}
	}

	// Extract headers
	headers := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	response := &APIResponse{
		StatusCode: resp.StatusCode,
		Body:       bodyMap,
		Headers:    headers,
		Duration:   duration,
	}

	// Add error message for non-2xx status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		response.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	return response, nil
}

// shouldRetry determines if a request should be retried based on status code
func (c *HTTPClient) shouldRetry(statusCode int) bool {
	// Retry on server errors (5xx) and some client errors
	switch statusCode {
	case 429, // Too Many Requests
		500, 502, 503, 504: // Server errors
		return true
	default:
		return false
	}
}

// joinFields joins a slice of fields into a comma-separated string
func joinFields(fields []string) string {
	if len(fields) == 0 {
		return ""
	}

	result := fields[0]
	for i := 1; i < len(fields); i++ {
		result += "," + fields[i]
	}
	return result
}

// Close closes the HTTP client and cleans up resources
func (c *HTTPClient) Close() error {
	// Close idle connections
	c.client.CloseIdleConnections()
	return nil
}
