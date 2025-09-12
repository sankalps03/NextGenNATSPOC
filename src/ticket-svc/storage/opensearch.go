package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

type OpenSearchStorage struct {
	client    *http.Client
	endpoint  string
	indexName string
}

var staticFields = map[string]struct{}{
	"requesterid":            {},
	"technicianid":           {},
	"groupid":                {},
	"statusid":               {},
	"priorityid":             {},
	"urgencyid":              {},
	"categoryid":             {},
	"companyid":              {},
	"departmentid":           {},
	"locationid":             {},
	"createdtime":            {},
	"updatedtime":            {},
	"lastresolvedtime":       {},
	"lastclosedtime":         {},
	"dueby":                  {},
	"oladueby":               {},
	"ucdueby":                {},
	"lastviolationtime":      {},
	"violatedslaid":          {},
	"updatedbyid":            {},
	"createdbyid":            {},
	"removedbyid":            {},
	"impactid":               {},
	"resolutionduelevel":     {},
	"responseduelevel":       {},
	"templateid":             {},
	"emailreadconfigid":      {},
	"requesttype":            {},
	"servicecatalogid":       {},
	"sourceid":               {},
	"groupchangedtime":       {},
	"oladuelevel":            {},
	"suggestedcategoryid":    {},
	"suggestedgroupid":       {},
	"closedby":               {},
	"resolvedby":             {},
	"vendorid":               {},
	"violateducid":           {},
	"transitionmodelid":      {},
	"messengerconfigid":      {},
	"removedtime":            {},
	"lastopenedtime":         {},
	"olddueby":               {},
	"oldresponsedue":         {},
	"responsedue":            {},
	"responseescalationtime": {},
	"statuschangedtime":      {},
	"olaescalationtime":      {},
	"askfeedbackdate":        {},
	"firstfeedbackdate":      {},
	"lastucviolationtime":    {},
	"lastapproveddate":       {},
}

func NewOpenSearchStorage(ctx context.Context, endpoint, indexName string) (*OpenSearchStorage, error) {
	if endpoint == "" {
		endpoint = "http://localhost:9200"
	}
	if indexName == "" {
		indexName = "tickets"
	}

	storage := &OpenSearchStorage{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		endpoint:  strings.TrimSuffix(endpoint, "/"),
		indexName: indexName,
	}

	// Create index with mappings if it doesn't exist
	if err := storage.createIndexIfNotExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to create index: %w", err)
	}

	return storage, nil
}

func (storage *OpenSearchStorage) createIndexIfNotExists(ctx context.Context) error {
	// Check if index exists
	req, err := http.NewRequestWithContext(ctx, "HEAD", fmt.Sprintf("%s/%s", storage.endpoint, storage.indexName), nil)
	if err != nil {
		return err
	}

	resp, err := storage.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Index already exists
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	// Create index with static mapping for predefined fields and dynamic mapping for others
	indexMapping := map[string]interface{}{
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type": "keyword",
				},
				"created_at": map[string]interface{}{
					"type": "date",
				},
				"updated_at": map[string]interface{}{
					"type": "date",
				},
				"fields": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						// Predefined static fields
						"requesterid": map[string]interface{}{
							"type": "keyword",
						},
						"technicianid": map[string]interface{}{
							"type": "keyword",
						},
						"groupid": map[string]interface{}{
							"type": "keyword",
						},
						"statusid": map[string]interface{}{
							"type": "keyword",
						},
						"priorityid": map[string]interface{}{
							"type": "keyword",
						},
						"urgencyid": map[string]interface{}{
							"type": "keyword",
						},
						"categoryid": map[string]interface{}{
							"type": "keyword",
						},
						"companyid": map[string]interface{}{
							"type": "keyword",
						},
						"departmentid": map[string]interface{}{
							"type": "keyword",
						},
						"locationid": map[string]interface{}{
							"type": "keyword",
						},
						"createdtime": map[string]interface{}{
							"type": "date",
						},
						"updatedtime": map[string]interface{}{
							"type": "date",
						},
						"lastresolvedtime": map[string]interface{}{
							"type": "date",
						},
						"lastclosedtime": map[string]interface{}{
							"type": "date",
						},
						"dueby": map[string]interface{}{
							"type": "date",
						},
						"oladueby": map[string]interface{}{
							"type": "date",
						},
						"ucdueby": map[string]interface{}{
							"type": "date",
						},
						"lastviolationtime": map[string]interface{}{
							"type": "date",
						},
						"violatedslaid": map[string]interface{}{
							"type": "keyword",
						},
						"updatedbyid": map[string]interface{}{
							"type": "keyword",
						},
						"createdbyid": map[string]interface{}{
							"type": "keyword",
						},
						"removedbyid": map[string]interface{}{
							"type": "keyword",
						},
						"impactid": map[string]interface{}{
							"type": "keyword",
						},
						"resolutionduelevel": map[string]interface{}{
							"type": "keyword",
						},
						"responseduelevel": map[string]interface{}{
							"type": "keyword",
						},
						"templateid": map[string]interface{}{
							"type": "keyword",
						},
						"emailreadconfigid": map[string]interface{}{
							"type": "keyword",
						},
						"requesttype": map[string]interface{}{
							"type": "keyword",
						},
						"servicecatalogid": map[string]interface{}{
							"type": "keyword",
						},
						"sourceid": map[string]interface{}{
							"type": "keyword",
						},
						"groupchangedtime": map[string]interface{}{
							"type": "date",
						},
						"oladuelevel": map[string]interface{}{
							"type": "keyword",
						},
						"suggestedcategoryid": map[string]interface{}{
							"type": "keyword",
						},
						"suggestedgroupid": map[string]interface{}{
							"type": "keyword",
						},
						"closedby": map[string]interface{}{
							"type": "keyword",
						},
						"resolvedby": map[string]interface{}{
							"type": "keyword",
						},
						"vendorid": map[string]interface{}{
							"type": "keyword",
						},
						"violateducid": map[string]interface{}{
							"type": "keyword",
						},
						"transitionmodelid": map[string]interface{}{
							"type": "keyword",
						},
						"messengerconfigid": map[string]interface{}{
							"type": "keyword",
						},
						"removedtime": map[string]interface{}{
							"type": "date",
						},
						"lastopenedtime": map[string]interface{}{
							"type": "date",
						},
						"olddueby": map[string]interface{}{
							"type": "date",
						},
						"oldresponsedue": map[string]interface{}{
							"type": "date",
						},
						"responsedue": map[string]interface{}{
							"type": "date",
						},
						"responseescalationtime": map[string]interface{}{
							"type": "date",
						},
						"statuschangedtime": map[string]interface{}{
							"type": "date",
						},
						"olaescalationtime": map[string]interface{}{
							"type": "date",
						},
						"askfeedbackdate": map[string]interface{}{
							"type": "date",
						},
						"firstfeedbackdate": map[string]interface{}{
							"type": "date",
						},
						"lastucviolationtime": map[string]interface{}{
							"type": "date",
						},
						"lastapproveddate": map[string]interface{}{
							"type": "date",
						},
					},
					"dynamic": true, // Allow dynamic fields for the remaining 100-200 fields
				},
			},
		},
	}

	body, err := json.Marshal(indexMapping)
	if err != nil {
		return err
	}

	req, err = http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s/%s", storage.endpoint, storage.indexName), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = storage.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create index: %s", string(body))
	}

	log.Printf("OpenSearch index '%s' created successfully", storage.indexName)
	return nil
}

func (storage *OpenSearchStorage) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	doc, kvDocs := storage.ticketToDocument(ticketData)

	body, err := json.Marshal(doc)
	if err != nil {
		return err, kvDocs
	}

	url := fmt.Sprintf("%s/%s/_doc/%s", storage.endpoint, storage.indexName, ticketData.Id)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return err, kvDocs
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := storage.client.Do(req)
	if err != nil {
		return err, kvDocs
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create ticket: %s", string(body)), kvDocs
	}

	return nil, kvDocs
}

func (storage *OpenSearchStorage) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	url := fmt.Sprintf("%s/%s/_doc/%s", storage.endpoint, storage.indexName, id)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return nil, false
	}

	resp, err := storage.client.Do(req)
	if err != nil {
		log.Printf("Error fetching ticket: %v", err)
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response: %v", err)
		return nil, false
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("Error unmarshaling response: %v", err)
		return nil, false
	}

	source, ok := result["_source"].(map[string]interface{})
	if !ok {
		return nil, false
	}

	var entries jetstream.KeyValueEntry

	err = nil

	if store != nil {

		entries, err = store.Get(context.Background(), id)
	}

	if err == nil && entries != nil {

		var docs map[string]interface{}

		json.Unmarshal(entries.Value(), &docs)

		for key, value := range docs {

			source[key] = value
		}
	}

	return storage.documentToTicket(source), true
}

func (storage *OpenSearchStorage) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	// Verify ticket exists
	_, found := storage.GetTicket(ticketData.Id, nil)
	if !found {
		return false
	}

	doc, _ := storage.ticketToDocument(ticketData)

	body, err := json.Marshal(doc)
	if err != nil {
		log.Printf("Error marshaling ticket: %v", err)
		return false
	}

	url := fmt.Sprintf("%s/%s/_doc/%s", storage.endpoint, storage.indexName, ticketData.Id)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := storage.client.Do(req)
	if err != nil {
		log.Printf("Error updating ticket: %v", err)
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated
}

func (storage *OpenSearchStorage) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
	// Get ticket first to return it
	ticket, found := storage.GetTicket(id, nil)
	if !found {
		return nil, false
	}

	url := fmt.Sprintf("%s/%s/_doc/%s", storage.endpoint, storage.indexName, id)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return nil, false
	}

	resp, err := storage.client.Do(req)
	if err != nil {
		log.Printf("Error deleting ticket: %v", err)
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return nil, false
	}

	return ticket, true
}

func (storage *OpenSearchStorage) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"match_all": map[string]interface{}{},
		},
		"size": 10000, // Max results
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/%s/_search", storage.endpoint, storage.indexName)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := storage.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	hits, ok := result["hits"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	hitsArray, ok := hits["hits"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid hits format")
	}

	var tickets []*ticketpb.TicketData
	for _, hit := range hitsArray {
		hitMap, ok := hit.(map[string]interface{})
		if !ok {
			continue
		}
		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			continue
		}

		var entries jetstream.KeyValueEntry

		err = nil

		if store != nil {

			entries, err = store.Get(context.Background(), source["id"].(string))
		}

		if err == nil && entries != nil {

			json.Unmarshal(entries.Value(), &source)
		}
		tickets = append(tickets, storage.documentToTicket(source))
	}

	return tickets, nil
}

func (storage *OpenSearchStorage) SearchTickets(searchRequest SearchRequest) ([]*ticketpb.TicketData, error) {

	conditions := searchRequest.Conditions

	query := storage.buildSearchQuery(conditions)

	// Add sorting if specified
	if len(searchRequest.SortFields) > 0 {
		query["sort"] = storage.buildSortQuery(searchRequest.SortFields)
	} else {

		// Default sort by created_at desc for regular search
		query["sort"] = []map[string]interface{}{
			{
				"created_at": map[string]interface{}{
					"order": "desc",
				},
			},
		}
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/%s/_search", storage.endpoint, storage.indexName)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := storage.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	hits, ok := result["hits"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	hitsArray, ok := hits["hits"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid hits format")
	}

	var tickets []*ticketpb.TicketData
	for _, hit := range hitsArray {
		hitMap, ok := hit.(map[string]interface{})
		if !ok {
			continue
		}
		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			continue
		}
		tickets = append(tickets, storage.documentToTicket(source))
	}

	return tickets, nil
}

func (storage *OpenSearchStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	query := storage.buildSearchQuery(request.Conditions)

	// Add field projection if specified
	if len(request.ProjectedFields) > 0 {
		// Start with system fields
		projectedFields := []string{"id", "created_at", "updated_at"}

		// Add custom fields with proper prefixing
		for _, field := range request.ProjectedFields {
			if field == "id" || field == "created_at" || field == "updated_at" {
				// System field, already included
				continue
			} else {
				// Custom field, add with fields prefix
				projectedFields = append(projectedFields, "fields."+field)
			}
		}

		query["_source"] = projectedFields
	}

	// Add sorting if specified
	if len(request.SortFields) > 0 {
		query["sort"] = storage.buildSortQuery(request.SortFields)
	} else {

		// Default sort by created_at desc for regular search
		query["sort"] = []map[string]interface{}{
			{
				"created_at": map[string]interface{}{
					"order": "desc",
				},
			},
		}
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/%s/_search", storage.endpoint, storage.indexName)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := storage.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	hits, ok := result["hits"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	hitsArray, ok := hits["hits"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid hits format")
	}

	var tickets []*ticketpb.TicketData
	for _, hit := range hitsArray {
		hitMap, ok := hit.(map[string]interface{})
		if !ok {
			continue
		}
		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			continue
		}
		tickets = append(tickets, storage.documentToTicket(source))
	}

	return tickets, nil
}

func (storage *OpenSearchStorage) buildSearchQuery(conditions []SearchCondition) map[string]interface{} {
	must := []map[string]interface{}{}

	for _, cond := range conditions {
		fieldPath := cond.Operand
		// Handle nested fields
		if !strings.HasPrefix(fieldPath, "fields.") && fieldPath != "id" &&
			fieldPath != "created_at" && fieldPath != "updated_at" {
			fieldPath = "fields." + fieldPath
		}

		switch cond.Operator {
		case "eq", "=":
			must = append(must, map[string]interface{}{
				"term": map[string]interface{}{
					fieldPath: cond.Value,
				},
			})
		case "ne", "!=", "<>":
			must = append(must, map[string]interface{}{
				"bool": map[string]interface{}{
					"must_not": map[string]interface{}{
						"term": map[string]interface{}{
							fieldPath: cond.Value,
						},
					},
				},
			})
		case "gt", ">":
			must = append(must, map[string]interface{}{
				"range": map[string]interface{}{
					fieldPath: map[string]interface{}{
						"gt": cond.Value,
					},
				},
			})
		case "lt", "<":
			must = append(must, map[string]interface{}{
				"range": map[string]interface{}{
					fieldPath: map[string]interface{}{
						"lt": cond.Value,
					},
				},
			})
		case "gte", ">=":
			must = append(must, map[string]interface{}{
				"range": map[string]interface{}{
					fieldPath: map[string]interface{}{
						"gte": cond.Value,
					},
				},
			})
		case "lte", "<=":
			must = append(must, map[string]interface{}{
				"range": map[string]interface{}{
					fieldPath: map[string]interface{}{
						"lte": cond.Value,
					},
				},
			})
		case "contains":
			must = append(must, map[string]interface{}{
				"wildcard": map[string]interface{}{
					fieldPath: map[string]interface{}{
						"value": fmt.Sprintf("*%v*", cond.Value),
					},
				},
			})
		case "begins_with":
			must = append(must, map[string]interface{}{
				"prefix": map[string]interface{}{
					fieldPath: cond.Value,
				},
			})
		}
	}

	return map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": must,
			},
		},
		"size": 10000,
	}
}

func (storage *OpenSearchStorage) buildSortQuery(sortFields []SortField) []map[string]interface{} {
	var sort []map[string]interface{}

	for _, sortField := range sortFields {
		fieldPath := sortField.Field
		// Handle nested fields
		if !strings.HasPrefix(fieldPath, "fields.") && fieldPath != "id" &&
			fieldPath != "created_at" && fieldPath != "updated_at" {
			fieldPath = "fields." + fieldPath
		}

		order := "asc"
		if sortField.Order == "desc" || sortField.Order == "descending" || sortField.Order == "DESC" {
			order = "desc"
		}

		sort = append(sort, map[string]interface{}{
			fieldPath: map[string]interface{}{
				"order": order,
			},
		})
	}

	return sort
}

func (storage *OpenSearchStorage) ticketToDocument(ticket *ticketpb.TicketData) (map[string]interface{}, map[string]interface{}) {
	result := map[string]interface{}{
		"id":         ticket.Id,
		"created_at": ticket.CreatedAt,
		"updated_at": ticket.UpdatedAt,
	}

	// Convert protobuf fields to simple map
	fields := make(map[string]interface{})

	kvValues := make(map[string]interface{})

	for key, value := range ticket.Fields {

		if _, ok := staticFields[key]; ok {

			fields[key] = convertFieldValueToInterface(value)
		}

		kvValues[key] = convertFieldValueToInterface(value)
	}
	result["fields"] = fields

	return result, kvValues
}

func (storage *OpenSearchStorage) documentToTicket(doc map[string]interface{}) *ticketpb.TicketData {
	ticket := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	if id, ok := doc["id"].(string); ok {
		ticket.Id = id
	}

	if createdAt, ok := doc["created_at"].(string); ok {
		ticket.CreatedAt = createdAt
	}
	if updatedAt, ok := doc["updated_at"].(string); ok {
		ticket.UpdatedAt = updatedAt
	}

	// Convert fields back to protobuf format
	if fields, ok := doc["fields"].(map[string]interface{}); ok {
		for key, value := range fields {
			if fieldValue, err := convertToFieldValue(value); err == nil {
				ticket.Fields[key] = fieldValue
			}
		}
	}

	for key, value := range doc {

		if fieldValue, err := convertToFieldValue(value); err == nil {
			ticket.Fields[key] = fieldValue
		}
	}

	return ticket
}

func (storage *OpenSearchStorage) Close() error {
	// HTTP client doesn't need explicit close
	return nil
}

// Helper functions (these should be shared with main.go, but defined here for completeness)
func convertFieldValueToInterface(fieldValue *ticketpb.FieldValue) interface{} {
	if fieldValue == nil {
		return nil
	}

	switch v := fieldValue.Value.(type) {
	case *ticketpb.FieldValue_StringValue:
		return v.StringValue
	case *ticketpb.FieldValue_IntValue:
		return v.IntValue
	case *ticketpb.FieldValue_DoubleValue:
		return v.DoubleValue
	case *ticketpb.FieldValue_BoolValue:
		return v.BoolValue
	case *ticketpb.FieldValue_BytesValue:
		return v.BytesValue
	case *ticketpb.FieldValue_StringArray:
		return v.StringArray.Values
	default:
		return nil
	}
}

func convertToFieldValue(value interface{}) (*ticketpb.FieldValue, error) {
	switch v := value.(type) {
	case string:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: v},
		}, nil
	case int:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
		}, nil
	case int64:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_IntValue{IntValue: v},
		}, nil
	case float64:
		// Check if it's actually an integer
		if v == float64(int64(v)) {
			return &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
			}, nil
		}
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: v},
		}, nil
	case bool:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_BoolValue{BoolValue: v},
		}, nil
	case []string:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringArray{
				StringArray: &ticketpb.StringArray{Values: v},
			},
		}, nil
	case []interface{}:
		// Convert interface slice to string slice
		var stringArray []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				stringArray = append(stringArray, str)
			} else {
				stringArray = append(stringArray, fmt.Sprintf("%v", item))
			}
		}
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringArray{
				StringArray: &ticketpb.StringArray{Values: stringArray},
			},
		}, nil
	default:
		// Convert unknown types to string
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: fmt.Sprintf("%v", v)},
		}, nil
	}
}
