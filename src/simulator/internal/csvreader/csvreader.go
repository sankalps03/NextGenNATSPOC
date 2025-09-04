package csvreader

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CSVReader handles reading and parsing CSV data for ticket generation
type CSVReader struct {
	filePath      string
	headers       []string
	records       []map[string]interface{}
	excludeFields []string
	mu            sync.RWMutex
	rand          *rand.Rand
}

// TicketData represents a ticket record from CSV
type TicketData struct {
	Fields map[string]interface{} `json:"fields"`
}

// New creates a new CSV reader instance
func New(filePath string) (*CSVReader, error) {
	reader := &CSVReader{
		filePath:      filePath,
		excludeFields: []string{"id"}, // Always exclude ID field
		rand:          rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	if err := reader.loadCSV(); err != nil {
		return nil, fmt.Errorf("failed to load CSV file: %w", err)
	}

	log.Printf("CSV Reader initialized: loaded %d records from %s", len(reader.records), filePath)
	return reader, nil
}

// loadCSV reads and parses the CSV file
func (r *CSVReader) loadCSV() error {
	file, err := os.Open(r.filePath)
	if err != nil {
		return fmt.Errorf("failed to open CSV file %s: %w", r.filePath, err)
	}
	defer file.Close()

	csvReader := csv.NewReader(file)
	csvReader.LazyQuotes = true
	csvReader.TrimLeadingSpace = true

	// Read headers
	headers, err := csvReader.Read()
	if err != nil {
		return fmt.Errorf("failed to read CSV headers: %w", err)
	}

	r.headers = headers
	log.Printf("CSV headers loaded: %v", headers)

	// Read all records
	var records []map[string]interface{}
	lineNumber := 1 // Start from 1 since we already read headers

	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Warning: failed to read CSV line %d: %v", lineNumber+1, err)
			lineNumber++
			continue
		}

		// Convert record to map with proper type conversion
		recordMap := make(map[string]interface{})
		for i, value := range record {
			if i < len(headers) {
				fieldName := strings.TrimSpace(headers[i])
				fieldName = strings.Trim(fieldName, `"`) // Remove quotes from field names

				// Skip excluded fields
				if r.isExcludedField(fieldName) {
					continue
				}

				// Convert value to appropriate type
				convertedValue := r.convertValue(strings.TrimSpace(value))
				recordMap[fieldName] = convertedValue
			}
		}

		if len(recordMap) > 0 {
			records = append(records, recordMap)
		}
		lineNumber++
	}

	r.mu.Lock()
	r.records = records
	r.mu.Unlock()

	log.Printf("Successfully loaded %d records from CSV", len(records))
	return nil
}

// convertValue attempts to convert string values to appropriate types
func (r *CSVReader) convertValue(value string) interface{} {
	// Remove quotes if present
	value = strings.Trim(value, `"`)

	// Handle empty values
	if value == "" {
		return ""
	}

	// Try to convert to integer
	if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
		return intVal
	}

	// Try to convert to float
	if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
		return floatVal
	}

	// Try to convert to boolean
	if boolVal, err := strconv.ParseBool(value); err == nil {
		return boolVal
	}

	// Return as string
	return value
}

// isExcludedField checks if a field should be excluded
func (r *CSVReader) isExcludedField(fieldName string) bool {
	fieldName = strings.ToLower(fieldName)
	for _, excluded := range r.excludeFields {
		if strings.ToLower(excluded) == fieldName {
			return true
		}
	}
	return false
}

// GetRandomTicket returns a random ticket record from the CSV data
func (r *CSVReader) GetRandomTicket() (*TicketData, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.records) == 0 {
		return nil, fmt.Errorf("no records available")
	}

	// Select random record
	randomIndex := r.rand.Intn(len(r.records))
	record := r.records[randomIndex]

	// Create a copy to avoid modifying original data
	fields := make(map[string]interface{})
	for k, v := range record {
		fields[k] = v
	}

	return &TicketData{Fields: fields}, nil
}

// GetRandomTicketWithFields returns a random ticket with only specified fields
func (r *CSVReader) GetRandomTicketWithFields(includeFields []string) (*TicketData, error) {
	ticket, err := r.GetRandomTicket()
	if err != nil {
		return nil, err
	}

	if len(includeFields) == 0 {
		return ticket, nil
	}

	// Filter fields
	filteredFields := make(map[string]interface{})
	for _, field := range includeFields {
		if value, exists := ticket.Fields[field]; exists {
			filteredFields[field] = value
		}
	}

	return &TicketData{Fields: filteredFields}, nil
}

// GetFieldValues returns all unique values for a specific field
func (r *CSVReader) GetFieldValues(fieldName string) []interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	valueSet := make(map[interface{}]bool)
	var values []interface{}

	for _, record := range r.records {
		if value, exists := record[fieldName]; exists {
			if !valueSet[value] {
				valueSet[value] = true
				values = append(values, value)
			}
		}
	}

	return values
}

// GetRandomFieldValue returns a random value for a specific field
func (r *CSVReader) GetRandomFieldValue(fieldName string) (interface{}, error) {
	values := r.GetFieldValues(fieldName)
	if len(values) == 0 {
		return nil, fmt.Errorf("no values found for field %s", fieldName)
	}

	randomIndex := r.rand.Intn(len(values))
	return values[randomIndex], nil
}

// GetHeaders returns the CSV headers (excluding excluded fields)
func (r *CSVReader) GetHeaders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var filteredHeaders []string
	for _, header := range r.headers {
		if !r.isExcludedField(header) {
			filteredHeaders = append(filteredHeaders, header)
		}
	}
	return filteredHeaders
}

// GetRecordCount returns the number of loaded records
func (r *CSVReader) GetRecordCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.records)
}

// SetExcludeFields sets the fields to exclude from ticket generation
func (r *CSVReader) SetExcludeFields(fields []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.excludeFields = fields
}

// AddExcludeField adds a field to the exclude list
func (r *CSVReader) AddExcludeField(field string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.excludeFields = append(r.excludeFields, field)
}

// GetSampleRecord returns the first record for inspection
func (r *CSVReader) GetSampleRecord() (*TicketData, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.records) == 0 {
		return nil, fmt.Errorf("no records available")
	}

	// Create a copy of the first record
	fields := make(map[string]interface{})
	for k, v := range r.records[0] {
		fields[k] = v
	}

	return &TicketData{Fields: fields}, nil
}
