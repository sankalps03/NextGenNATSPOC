package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the simulator service
type Config struct {
	// API Configuration
	APIBaseURL string   `json:"api_base_url"`
	TenantIDs  []string `json:"tenant_ids"`

	// CSV Configuration
	CSVFilePath string `json:"csv_file_path"`

	// EPS (Events Per Second) Configuration
	EPS EPSConfig `json:"eps"`

	// HTTP Client Configuration
	HTTPClient HTTPClientConfig `json:"http_client"`

	// Metrics Configuration
	Metrics MetricsConfig `json:"metrics"`

	// API Operation Configuration
	Operations OperationsConfig `json:"operations"`

	// Logging
	LogLevel string `json:"log_level"`
}

// EPSConfig defines the events per second for different operations
type EPSConfig struct {
	Create float64 `json:"create"` // Tickets created per second
	Search float64 `json:"search"` // Search requests per second
	Get    float64 `json:"get"`    // Get requests per second
	Update float64 `json:"update"` // Update requests per second
}

// HTTPClientConfig defines HTTP client settings
type HTTPClientConfig struct {
	Timeout         time.Duration `json:"timeout"`
	MaxIdleConns    int           `json:"max_idle_conns"`
	MaxConnsPerHost int           `json:"max_conns_per_host"`
	RetryAttempts   int           `json:"retry_attempts"`
	RetryDelay      time.Duration `json:"retry_delay"`
}

// MetricsConfig defines metrics collection settings
type MetricsConfig struct {
	Enabled        bool          `json:"enabled"`
	ReportInterval time.Duration `json:"report_interval"`
	Port           int           `json:"port"`
}

// OperationsConfig defines configuration for different API operations
type OperationsConfig struct {
	Create CreateConfig `json:"create"`
	Search SearchConfig `json:"search"`
	Get    GetConfig    `json:"get"`
	Update UpdateConfig `json:"update"`
}

// CreateConfig defines ticket creation settings
type CreateConfig struct {
	Enabled           bool     `json:"enabled"`
	ExcludeFields     []string `json:"exclude_fields"`
	RequiredFields    []string `json:"required_fields"`
	RandomizeFields   []string `json:"randomize_fields"`
	DefaultFieldValue string   `json:"default_field_value"`
}

// SearchConfig defines search operation settings
type SearchConfig struct {
	Enabled          bool               `json:"enabled"`
	SearchConditions []SearchCondition  `json:"search_conditions"`
	ProjectedFields  []string           `json:"projected_fields"`
	RandomizeFields  bool               `json:"randomize_fields"`
	MaxResults       int                `json:"max_results"`
	ConditionWeights map[string]float64 `json:"condition_weights"`
}

// SearchCondition defines a search condition template
type SearchCondition struct {
	Field     string      `json:"field"`
	Operator  string      `json:"operator"` // eq, ne, gt, lt, gte, lte, contains, in
	Value     interface{} `json:"value"`
	ValueType string      `json:"value_type"` // string, number, boolean, array
	Weight    float64     `json:"weight"`     // Probability weight for this condition
}

// GetConfig defines get operation settings
type GetConfig struct {
	Enabled         bool     `json:"enabled"`
	UseCreatedIDs   bool     `json:"use_created_ids"`   // Use IDs from created tickets
	RandomTicketIDs []string `json:"random_ticket_ids"` // Fallback random IDs
	ProjectedFields []string `json:"projected_fields"`
}

// UpdateConfig defines update operation settings
type UpdateConfig struct {
	Enabled         bool                   `json:"enabled"`
	UseCreatedIDs   bool                   `json:"use_created_ids"`
	UpdateFields    []string               `json:"update_fields"`
	UpdateTemplates map[string]interface{} `json:"update_templates"`
	RandomizeValues bool                   `json:"randomize_values"`
}

// Load loads configuration from environment variables and .env file
func Load() (*Config, error) {
	// Try to load .env file (optional)
	_ = godotenv.Load()

	config := &Config{
		APIBaseURL:  getEnvOrDefault("API_BASE_URL", "http://localhost:8082"),
		TenantIDs:   getTenantIDs(),
		CSVFilePath: getEnvOrDefault("CSV_FILE_PATH", "./data/request_202502111503.csv"),
		LogLevel:    getEnvOrDefault("LOG_LEVEL", "info"),

		EPS: EPSConfig{
			Create: getEnvFloat("EPS_CREATE", 100),
			Search: getEnvFloat("EPS_SEARCH", 2),
			Get:    getEnvFloat("EPS_GET", 1),
			Update: getEnvFloat("EPS_UPDATE", 1),
		},

		HTTPClient: HTTPClientConfig{
			Timeout:         getEnvDuration("HTTP_TIMEOUT", 30*time.Second),
			MaxIdleConns:    getEnvInt("HTTP_MAX_IDLE_CONNS", 100),
			MaxConnsPerHost: getEnvInt("HTTP_MAX_CONNS_PER_HOST", 10),
			RetryAttempts:   getEnvInt("HTTP_RETRY_ATTEMPTS", 3),
			RetryDelay:      getEnvDuration("HTTP_RETRY_DELAY", 1*time.Second),
		},

		Metrics: MetricsConfig{
			Enabled:        getEnvBool("METRICS_ENABLED", true),
			ReportInterval: getEnvDuration("METRICS_REPORT_INTERVAL", 10*time.Second),
			Port:           getEnvInt("METRICS_PORT", 8083),
		},

		Operations: OperationsConfig{
			Create: CreateConfig{
				Enabled:           getEnvBool("CREATE_ENABLED", true),
				ExcludeFields:     getEnvStringSlice("CREATE_EXCLUDE_FIELDS", []string{"id"}),
				RequiredFields:    getEnvStringSlice("CREATE_REQUIRED_FIELDS", []string{"subject", "description"}),
				RandomizeFields:   getEnvStringSlice("CREATE_RANDOMIZE_FIELDS", []string{}),
				DefaultFieldValue: getEnvOrDefault("CREATE_DEFAULT_FIELD_VALUE", ""),
			},
			Search: SearchConfig{
				Enabled:          getEnvBool("SEARCH_ENABLED", true),
				ProjectedFields:  getEnvStringSlice("SEARCH_PROJECTED_FIELDS", []string{}),
				RandomizeFields:  getEnvBool("SEARCH_RANDOMIZE_FIELDS", true),
				MaxResults:       getEnvInt("SEARCH_MAX_RESULTS", 100),
				SearchConditions: getDefaultSearchConditions(),
				ConditionWeights: getDefaultConditionWeights(),
			},
			Get: GetConfig{
				Enabled:         getEnvBool("GET_ENABLED", true),
				UseCreatedIDs:   getEnvBool("GET_USE_CREATED_IDS", true),
				RandomTicketIDs: getEnvStringSlice("GET_RANDOM_TICKET_IDS", []string{}),
				ProjectedFields: getEnvStringSlice("GET_PROJECTED_FIELDS", []string{}),
			},
			Update: UpdateConfig{
				Enabled:         getEnvBool("UPDATE_ENABLED", true),
				UseCreatedIDs:   getEnvBool("UPDATE_USE_CREATED_IDS", true),
				UpdateFields:    getEnvStringSlice("UPDATE_FIELDS", []string{"description", "statusid", "priorityid"}),
				UpdateTemplates: getDefaultUpdateTemplates(),
				RandomizeValues: getEnvBool("UPDATE_RANDOMIZE_VALUES", true),
			},
		},
	}

	return config, config.validate()
}

// validate checks if the configuration is valid
func (c *Config) validate() error {
	if c.APIBaseURL == "" {
		return fmt.Errorf("API_BASE_URL is required")
	}
	if c.CSVFilePath == "" {
		return fmt.Errorf("CSV_FILE_PATH is required")
	}
	if len(c.TenantIDs) == 0 {
		return fmt.Errorf("at least one TENANT_ID is required")
	}
	return nil
}

// Helper functions for environment variable parsing
func getEnvOrDefault(key, defaultValue string) string {
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

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getEnvStringSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		// Split by comma and trim spaces
		parts := strings.Split(value, ",")
		var result []string
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return defaultValue
}

// getDefaultSearchConditions returns default search condition templates
func getDefaultSearchConditions() []SearchCondition {
	return []SearchCondition{
		// Core status and priority fields
		{
			Field:     "statusid",
			Operator:  "eq",
			Value:     12,
			ValueType: "number",
			Weight:    0.15,
		},
		{
			Field:     "priorityid",
			Operator:  "eq",
			Value:     1,
			ValueType: "number",
			Weight:    0.12,
		},
		{
			Field:     "urgencyid",
			Operator:  "gte",
			Value:     1,
			ValueType: "number",
			Weight:    0.10,
		},
		// Category and department
		{
			Field:     "categoryid",
			Operator:  "eq",
			Value:     534,
			ValueType: "number",
			Weight:    0.08,
		},
		{
			Field:     "departmentid",
			Operator:  "eq",
			Value:     0,
			ValueType: "number",
			Weight:    0.06,
		},
		// Assignment fields
		{
			Field:     "groupid",
			Operator:  "eq",
			Value:     54,
			ValueType: "number",
			Weight:    0.08,
		},
		{
			Field:     "technicianid",
			Operator:  "eq",
			Value:     2812,
			ValueType: "number",
			Weight:    0.06,
		},
		{
			Field:     "createdbyid",
			Operator:  "eq",
			Value:     7193,
			ValueType: "number",
			Weight:    0.05,
		},
		// Text fields
		{
			Field:     "subject",
			Operator:  "contains",
			Value:     "Extension",
			ValueType: "string",
			Weight:    0.08,
		},
		{
			Field:     "name",
			Operator:  "contains",
			Value:     "SR-",
			ValueType: "string",
			Weight:    0.06,
		},
		// Impact and location
		{
			Field:     "impactid",
			Operator:  "eq",
			Value:     1,
			ValueType: "number",
			Weight:    0.04,
		},
		{
			Field:     "locationid",
			Operator:  "eq",
			Value:     21,
			ValueType: "number",
			Weight:    0.03,
		},
		// Request type and source
		{
			Field:     "requesttype",
			Operator:  "eq",
			Value:     0,
			ValueType: "number",
			Weight:    0.03,
		},
		{
			Field:     "sourceid",
			Operator:  "eq",
			Value:     2,
			ValueType: "number",
			Weight:    0.03,
		},
		// Boolean flags
		{
			Field:     "removed",
			Operator:  "eq",
			Value:     false,
			ValueType: "boolean",
			Weight:    0.02,
		},
		{
			Field:     "spam",
			Operator:  "eq",
			Value:     false,
			ValueType: "boolean",
			Weight:    0.01,
		},
	}
}

// getDefaultConditionWeights returns default weights for condition selection
func getDefaultConditionWeights() map[string]float64 {
	return map[string]float64{
		"statusid":     0.15,
		"priorityid":   0.12,
		"urgencyid":    0.10,
		"categoryid":   0.08,
		"groupid":      0.08,
		"subject":      0.08,
		"departmentid": 0.06,
		"technicianid": 0.06,
		"name":         0.06,
		"createdbyid":  0.05,
		"impactid":     0.04,
		"locationid":   0.03,
		"requesttype":  0.03,
		"sourceid":     0.03,
		"removed":      0.02,
		"spam":         0.01,
	}
}

// getAllSearchableFields returns all searchable fields from the user's requirements
func getAllSearchableFields() []string {
	return []string{
		"id", "createdbyid", "name", "oobtype", "removed", "removedbyid",
		"approvalstatus", "approvaltype", "categoryid", "departmentid",
		"dueby", "duetimemanuallyupdated", "firstresponsetime", "groupid",
		"impactid", "lastclosedtime", "lastopenedtime", "lastresolvedtime",
		"lastviolationtime", "locationid", "olddueby", "oldresponsedue",
		"priorityid", "reopened", "requesterid", "resolutionduelevel",
		"resolutionescalationtime", "responsedue", "responseduelevel",
		"responsedueviolated", "responseescalationtime", "slaviolated",
		"statuschangedtime", "statusid", "subject", "supportlevel",
		"technicianid", "templateid", "totalonholdduration",
		"totalresolutiontime", "totalslapausetime", "totalworkingtime",
		"urgencyid", "violatedslaid", "callfrom", "emailreadconfigemail",
		"emailreadconfigid", "purchaserequest", "requesttype",
		"servicecatalogid", "sourceid", "spam", "viprequest",
		"groupchangedtime", "lastolaviolationtime", "oladueby",
		"olaviolated", "oldoladueby", "askfeedbackdate", "firstfeedbackdate",
		"oladuelevel", "olaescalationtime", "suggestedcategoryid",
		"suggestedgroupid", "companyid", "closedby", "resolvedby",
		"vendorid", "lastucviolationtime", "olducdueby", "totaluconholdduration",
		"totalucpausetime", "totalucworkingtime", "ucdueby", "ucduelevel",
		"ucescalationtime", "ucviolated", "violateducid",
		"totalucresolutiontime", "transitionmodelid", "migrated",
		"mergedrequest", "messengerconfigid", "lastapproveddate",
	}
}

// getDefaultUpdateTemplates returns default update templates
func getDefaultUpdateTemplates() map[string]interface{} {
	return map[string]interface{}{
		"description": []string{
			"Updated ticket description",
			"Modified by simulator",
			"Status changed by automation",
			"Priority updated",
		},
		"statusid":   []int{1, 2, 3, 12},
		"priorityid": []int{1, 2, 3, 4},
		"urgencyid":  []int{1, 2, 3, 4},
	}
}

// getTenantIDs parses tenant IDs from environment variables
func getTenantIDs() []string {
	// First try TENANT_IDS (plural) for multiple tenants
	if tenantIDs := getEnvStringSlice("TENANT_IDS", []string{}); len(tenantIDs) > 0 {
		return tenantIDs
	}

	// Fallback to TENANT_ID (singular) for backward compatibility
	if tenantID := getEnvOrDefault("TENANT_ID", ""); tenantID != "" {
		return []string{tenantID}
	}

	// Default tenants for testing
	return []string{
		"simulator-tenant-1",
		"simulator-tenant-2",
		"simulator-tenant-3",
	}
}
