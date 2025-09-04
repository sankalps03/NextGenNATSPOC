package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"simulator/logger"
)

// Metrics collects and reports performance metrics for the simulator
type Metrics struct {
	mu     sync.RWMutex
	logger logger.Logger

	// Request counters
	createRequests int64
	searchRequests int64
	getRequests    int64
	updateRequests int64

	// Success counters
	createSuccess int64
	searchSuccess int64
	getSuccess    int64
	updateSuccess int64

	// Error counters
	createErrors int64
	searchErrors int64
	getErrors    int64
	updateErrors int64

	// Response time tracking
	createResponseTimes []time.Duration
	searchResponseTimes []time.Duration
	getResponseTimes    []time.Duration
	updateResponseTimes []time.Duration

	// Rate tracking
	startTime      time.Time
	lastReportTime time.Time

	// Current rates (requests per second)
	currentCreateRate float64
	currentSearchRate float64
	currentGetRate    float64
	currentUpdateRate float64
}

// MetricsSnapshot represents a point-in-time view of metrics
type MetricsSnapshot struct {
	Timestamp time.Time `json:"timestamp"`

	// Total counts
	TotalRequests int64 `json:"total_requests"`
	TotalSuccess  int64 `json:"total_success"`
	TotalErrors   int64 `json:"total_errors"`

	// Per-operation counts
	CreateRequests int64 `json:"create_requests"`
	SearchRequests int64 `json:"search_requests"`
	GetRequests    int64 `json:"get_requests"`
	UpdateRequests int64 `json:"update_requests"`

	CreateSuccess int64 `json:"create_success"`
	SearchSuccess int64 `json:"search_success"`
	GetSuccess    int64 `json:"get_success"`
	UpdateSuccess int64 `json:"update_success"`

	CreateErrors int64 `json:"create_errors"`
	SearchErrors int64 `json:"search_errors"`
	GetErrors    int64 `json:"get_errors"`
	UpdateErrors int64 `json:"update_errors"`

	// Success rates (percentage)
	CreateSuccessRate  float64 `json:"create_success_rate"`
	SearchSuccessRate  float64 `json:"search_success_rate"`
	GetSuccessRate     float64 `json:"get_success_rate"`
	UpdateSuccessRate  float64 `json:"update_success_rate"`
	OverallSuccessRate float64 `json:"overall_success_rate"`

	// Current rates (requests per second)
	CreateRate float64 `json:"create_rate"`
	SearchRate float64 `json:"search_rate"`
	GetRate    float64 `json:"get_rate"`
	UpdateRate float64 `json:"update_rate"`
	TotalRate  float64 `json:"total_rate"`

	// Average response times (milliseconds)
	AvgCreateResponseTime float64 `json:"avg_create_response_time_ms"`
	AvgSearchResponseTime float64 `json:"avg_search_response_time_ms"`
	AvgGetResponseTime    float64 `json:"avg_get_response_time_ms"`
	AvgUpdateResponseTime float64 `json:"avg_update_response_time_ms"`

	// Runtime information
	UptimeSeconds float64 `json:"uptime_seconds"`
}

// New creates a new metrics collector
func New() *Metrics {
	now := time.Now()
	return &Metrics{
		logger:         logger.NewLogger("metrics", "simulator"),
		startTime:      now,
		lastReportTime: now,
	}
}

// RecordCreateRequest records a create request
func (m *Metrics) RecordCreateRequest(duration time.Duration, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.createRequests++
	m.createResponseTimes = append(m.createResponseTimes, duration)

	if success {
		m.createSuccess++
	} else {
		m.createErrors++
	}
}

// RecordSearchRequest records a search request
func (m *Metrics) RecordSearchRequest(duration time.Duration, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.searchRequests++
	m.searchResponseTimes = append(m.searchResponseTimes, duration)

	if success {
		m.searchSuccess++
	} else {
		m.searchErrors++
	}
}

// RecordGetRequest records a get request
func (m *Metrics) RecordGetRequest(duration time.Duration, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.getRequests++
	m.getResponseTimes = append(m.getResponseTimes, duration)

	if success {
		m.getSuccess++
	} else {
		m.getErrors++
	}
}

// RecordUpdateRequest records an update request
func (m *Metrics) RecordUpdateRequest(duration time.Duration, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updateRequests++
	m.updateResponseTimes = append(m.updateResponseTimes, duration)

	if success {
		m.updateSuccess++
	} else {
		m.updateErrors++
	}
}

// GetSnapshot returns a snapshot of current metrics
func (m *Metrics) GetSnapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	uptime := now.Sub(m.startTime).Seconds()

	// Calculate totals
	totalRequests := m.createRequests + m.searchRequests + m.getRequests + m.updateRequests
	totalSuccess := m.createSuccess + m.searchSuccess + m.getSuccess + m.updateSuccess
	totalErrors := m.createErrors + m.searchErrors + m.getErrors + m.updateErrors

	// Calculate success rates
	createSuccessRate := calculateSuccessRate(m.createSuccess, m.createRequests)
	searchSuccessRate := calculateSuccessRate(m.searchSuccess, m.searchRequests)
	getSuccessRate := calculateSuccessRate(m.getSuccess, m.getRequests)
	updateSuccessRate := calculateSuccessRate(m.updateSuccess, m.updateRequests)
	overallSuccessRate := calculateSuccessRate(totalSuccess, totalRequests)

	// Calculate current rates
	createRate := float64(m.createRequests) / uptime
	searchRate := float64(m.searchRequests) / uptime
	getRate := float64(m.getRequests) / uptime
	updateRate := float64(m.updateRequests) / uptime
	totalRate := float64(totalRequests) / uptime

	// Calculate average response times
	avgCreateResponseTime := calculateAvgResponseTime(m.createResponseTimes)
	avgSearchResponseTime := calculateAvgResponseTime(m.searchResponseTimes)
	avgGetResponseTime := calculateAvgResponseTime(m.getResponseTimes)
	avgUpdateResponseTime := calculateAvgResponseTime(m.updateResponseTimes)

	return MetricsSnapshot{
		Timestamp: now,

		TotalRequests: totalRequests,
		TotalSuccess:  totalSuccess,
		TotalErrors:   totalErrors,

		CreateRequests: m.createRequests,
		SearchRequests: m.searchRequests,
		GetRequests:    m.getRequests,
		UpdateRequests: m.updateRequests,

		CreateSuccess: m.createSuccess,
		SearchSuccess: m.searchSuccess,
		GetSuccess:    m.getSuccess,
		UpdateSuccess: m.updateSuccess,

		CreateErrors: m.createErrors,
		SearchErrors: m.searchErrors,
		GetErrors:    m.getErrors,
		UpdateErrors: m.updateErrors,

		CreateSuccessRate:  createSuccessRate,
		SearchSuccessRate:  searchSuccessRate,
		GetSuccessRate:     getSuccessRate,
		UpdateSuccessRate:  updateSuccessRate,
		OverallSuccessRate: overallSuccessRate,

		CreateRate: createRate,
		SearchRate: searchRate,
		GetRate:    getRate,
		UpdateRate: updateRate,
		TotalRate:  totalRate,

		AvgCreateResponseTime: avgCreateResponseTime,
		AvgSearchResponseTime: avgSearchResponseTime,
		AvgGetResponseTime:    avgGetResponseTime,
		AvgUpdateResponseTime: avgUpdateResponseTime,

		UptimeSeconds: uptime,
	}
}

// StartReporting starts periodic metrics reporting
func (m *Metrics) StartReporting(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	m.logger.Info("Metrics reporting started")

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Metrics reporting stopped")
			return
		case <-ticker.C:
			snapshot := m.GetSnapshot()
			m.logMetrics(snapshot)
		}
	}
}

// logMetrics logs the current metrics snapshot
func (m *Metrics) logMetrics(snapshot MetricsSnapshot) {
	// Create a summary log entry
	m.logger.Info(fmt.Sprintf("METRICS SUMMARY - Total: %d req (%.1f/s), Success: %.1f%%, Errors: %d, Uptime: %.1fs",
		snapshot.TotalRequests,
		snapshot.TotalRate,
		snapshot.OverallSuccessRate,
		snapshot.TotalErrors,
		snapshot.UptimeSeconds))

	// Log detailed metrics as JSON for structured logging
	if jsonData, err := json.Marshal(snapshot); err == nil {
		m.logger.Debug(fmt.Sprintf("METRICS_DETAIL: %s", string(jsonData)))
	}
}

// Reset resets all metrics counters
func (m *Metrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.startTime = now
	m.lastReportTime = now

	m.createRequests = 0
	m.searchRequests = 0
	m.getRequests = 0
	m.updateRequests = 0

	m.createSuccess = 0
	m.searchSuccess = 0
	m.getSuccess = 0
	m.updateSuccess = 0

	m.createErrors = 0
	m.searchErrors = 0
	m.getErrors = 0
	m.updateErrors = 0

	m.createResponseTimes = nil
	m.searchResponseTimes = nil
	m.getResponseTimes = nil
	m.updateResponseTimes = nil
}

// Helper functions

func calculateSuccessRate(success, total int64) float64 {
	if total == 0 {
		return 0.0
	}
	return (float64(success) / float64(total)) * 100.0
}

func calculateAvgResponseTime(durations []time.Duration) float64 {
	if len(durations) == 0 {
		return 0.0
	}

	var total time.Duration
	for _, d := range durations {
		total += d
	}

	avgDuration := total / time.Duration(len(durations))
	return float64(avgDuration.Nanoseconds()) / 1000000.0 // Convert to milliseconds
}
