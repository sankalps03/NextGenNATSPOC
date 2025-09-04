package storage

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// GSIConfig represents a Global Secondary Index configuration
type GSIConfig struct {
	IndexName           string    // Name of the GSI
	HashKey             string    // Hash key attribute name
	RangeKey            string    // Range key attribute name (usually "pk")
	Priority            int       // Priority for index selection (lower = higher priority)
	EstimatedCost       float64   // Estimated cost for using this index
	SupportedOps        []string  // Supported operators for this index
	Status              string    // Index status: CREATING, ACTIVE, DELETING, etc.
	ProjectionType      string    // Projection type: ALL, KEYS_ONLY, INCLUDE
	ProjectedAttributes []string  // Projected attributes for INCLUDE projection
	CreatedAt           time.Time // When the index was created
	ExistsInDynamoDB    bool      // Whether the index actually exists in DynamoDB
}

// GSIQueryMetrics tracks performance metrics for GSI queries
type GSIQueryMetrics struct {
	IndexName     string
	QueryTime     time.Duration
	ResultCount   int
	ConditionUsed SearchCondition
	Success       bool
	ErrorMessage  string
}

// GSIQueryResult represents the result of a GSI query
type GSIQueryResult struct {
	TicketIDs []string                          // Extracted ticket IDs
	Items     []map[string]types.AttributeValue // Raw DynamoDB items
	Metrics   GSIQueryMetrics                   // Performance metrics for this query
}

// DynamoDBStorage implements tenant-aware dynamic ticket storage using AWS DynamoDB with separate field attributes
// Each tenant gets its own DynamoDB table for complete data isolation
type DynamoDBStorage struct {
	client        *dynamodb.Client
	baseTableName string
	tenantTables  sync.Map                        // tenant -> tableName mapping for performance
	tableMutex    sync.RWMutex                    // synchronizes table creation operations
	gsiConfig     map[string]map[string]GSIConfig // tenant -> field name -> GSI configuration mapping
	gsiMutex      sync.RWMutex                    // synchronizes GSI configuration operations
	enableGSI     bool                            // feature flag to enable/disable GSI usage
}

// fieldValueToDynamoDBAttribute converts a protobuf FieldValue to a DynamoDB AttributeValue
func fieldValueToDynamoDBAttribute(fieldValue *ticketpb.FieldValue) types.AttributeValue {
	if fieldValue == nil {
		return &types.AttributeValueMemberNULL{Value: true}
	}

	switch v := fieldValue.Value.(type) {
	case *ticketpb.FieldValue_StringValue:
		return &types.AttributeValueMemberS{Value: v.StringValue}
	case *ticketpb.FieldValue_IntValue:
		return &types.AttributeValueMemberN{Value: strconv.FormatInt(v.IntValue, 10)}
	case *ticketpb.FieldValue_DoubleValue:
		return &types.AttributeValueMemberN{Value: strconv.FormatFloat(v.DoubleValue, 'f', -1, 64)}
	case *ticketpb.FieldValue_BoolValue:
		return &types.AttributeValueMemberBOOL{Value: v.BoolValue}
	case *ticketpb.FieldValue_BytesValue:
		return &types.AttributeValueMemberB{Value: v.BytesValue}
	case *ticketpb.FieldValue_StringArray:
		var listItems []types.AttributeValue
		for _, str := range v.StringArray.Values {
			listItems = append(listItems, &types.AttributeValueMemberS{Value: str})
		}
		return &types.AttributeValueMemberL{Value: listItems}
	default:
		return &types.AttributeValueMemberNULL{Value: true}
	}
}

// dynamoDBAttributeToFieldValue converts a DynamoDB AttributeValue to a protobuf FieldValue
func dynamoDBAttributeToFieldValue(attr types.AttributeValue) *ticketpb.FieldValue {
	switch v := attr.(type) {
	case *types.AttributeValueMemberS:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: v.Value},
		}
	case *types.AttributeValueMemberN:
		// Try to parse as int first, then as float
		if intVal, err := strconv.ParseInt(v.Value, 10, 64); err == nil {
			return &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_IntValue{IntValue: intVal},
			}
		}
		if floatVal, err := strconv.ParseFloat(v.Value, 64); err == nil {
			return &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: floatVal},
			}
		}
		// Fallback to string if parsing fails
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: v.Value},
		}
	case *types.AttributeValueMemberBOOL:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_BoolValue{BoolValue: v.Value},
		}
	case *types.AttributeValueMemberB:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_BytesValue{BytesValue: v.Value},
		}
	case *types.AttributeValueMemberL:
		var stringValues []string
		for _, item := range v.Value {
			if strItem, ok := item.(*types.AttributeValueMemberS); ok {
				stringValues = append(stringValues, strItem.Value)
			}
		}
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringArray{
				StringArray: &ticketpb.StringArray{Values: stringValues},
			},
		}
	default:
		return nil
	}
}

// protobufToDynamoDBItem converts a TicketData protobuf to a DynamoDB item with separate field attributes
func protobufToDynamoDBItem(ticketData *ticketpb.TicketData) map[string]types.AttributeValue {
	item := make(map[string]types.AttributeValue)

	// Store core fields as top-level attributes
	item["pk"] = &types.AttributeValueMemberS{Value: ticketData.Id}
	item["tenant"] = &types.AttributeValueMemberS{Value: ticketData.Tenant}
	item["created_at"] = &types.AttributeValueMemberS{Value: ticketData.CreatedAt}
	item["updated_at"] = &types.AttributeValueMemberS{Value: ticketData.UpdatedAt}

	// Store dynamic fields with "field_" prefix to avoid naming conflicts
	for fieldName, fieldValue := range ticketData.Fields {
		attributeName := "field_" + fieldName

		// For GSI key fields, force string type to match GSI attribute definitions
		if isGSIKeyField(fieldName) {
			item[attributeName] = forceStringAttribute(fieldValue)
		} else {
			item[attributeName] = fieldValueToDynamoDBAttribute(fieldValue)
		}
	}

	return item
}

// isGSIKeyField checks if a field is used as a GSI key and should be stored as string
func isGSIKeyField(fieldName string) bool {
	// Define the 19 business-critical fields that have GSI indexes (must match generateBusinessGSIDefinitions and CSV structure)
	gsiKeyFields := map[string]bool{
		"requesterid":       true, // High - Many unique users → frequent search filter
		"technicianid":      true, // High - For assignment filtering and reporting
		"groupid":           true, // High - Workgroup-based queries
		"statusid":          true, // Medium - Filter tickets by status efficiently
		"priorityid":        true, // Medium - Common SLA-related queries
		"urgencyid":         true, // Medium - Helps with SLA escalation checks
		"categoryid":        true, // High - Frequent search filter for service categorization
		"companyid":         true, // High - Multi-tenant isolation and reporting
		"departmentid":      true, // High - Department-based filtering
		"locationid":        true, // High - Filter tickets by location
		"createdtime":       true, // High - Sorting and time-based range queries
		"updatedtime":       true, // High - Recent activity queries
		"lastresolvedtime":  true, // High - SLA performance tracking
		"lastclosedtime":    true, // High - Closure analytics
		"dueby":             true, // High - SLA calculations and escalation jobs
		"oladueby":          true, // High - OLA tracking for internal teams
		"ucdueby":           true, // High - UC SLA tracking
		"lastviolationtime": true, // High - SLA/OLA violation tracking and reporting
		"violatedslaid":     true, // Medium - Specific violated SLA record lookup
	}
	return gsiKeyFields[fieldName]
}

// forceStringAttribute converts any field value to a string attribute for GSI compatibility
func forceStringAttribute(fieldValue *ticketpb.FieldValue) types.AttributeValue {
	if fieldValue == nil {
		return &types.AttributeValueMemberS{Value: ""}
	}

	switch v := fieldValue.Value.(type) {
	case *ticketpb.FieldValue_StringValue:
		return &types.AttributeValueMemberS{Value: v.StringValue}
	case *ticketpb.FieldValue_IntValue:
		return &types.AttributeValueMemberS{Value: strconv.FormatInt(v.IntValue, 10)}
	case *ticketpb.FieldValue_DoubleValue:
		return &types.AttributeValueMemberS{Value: strconv.FormatFloat(v.DoubleValue, 'f', -1, 64)}
	case *ticketpb.FieldValue_BoolValue:
		return &types.AttributeValueMemberS{Value: strconv.FormatBool(v.BoolValue)}
	case *ticketpb.FieldValue_BytesValue:
		return &types.AttributeValueMemberS{Value: string(v.BytesValue)}
	case *ticketpb.FieldValue_StringArray:
		// Convert array to comma-separated string for GSI compatibility
		var values []string
		for _, str := range v.StringArray.Values {
			values = append(values, str)
		}
		return &types.AttributeValueMemberS{Value: strings.Join(values, ",")}
	default:
		return &types.AttributeValueMemberS{Value: ""}
	}
}

// dynamoDBItemToProtobuf converts a DynamoDB item back to a TicketData protobuf
func dynamoDBItemToProtobuf(item map[string]types.AttributeValue) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Extract core fields
	if pkAttr, ok := item["pk"]; ok {
		if pkVal, ok := pkAttr.(*types.AttributeValueMemberS); ok {
			ticketData.Id = pkVal.Value
		}
	}
	if tenantAttr, ok := item["tenant"]; ok {
		if tenantVal, ok := tenantAttr.(*types.AttributeValueMemberS); ok {
			ticketData.Tenant = tenantVal.Value
		}
	}
	if createdAttr, ok := item["created_at"]; ok {
		if createdVal, ok := createdAttr.(*types.AttributeValueMemberS); ok {
			ticketData.CreatedAt = createdVal.Value
		}
	}
	if updatedAttr, ok := item["updated_at"]; ok {
		if updatedVal, ok := updatedAttr.(*types.AttributeValueMemberS); ok {
			ticketData.UpdatedAt = updatedVal.Value
		}
	}

	// Extract dynamic fields (those with "field_" prefix)
	for attributeName, attributeValue := range item {
		if strings.HasPrefix(attributeName, "field_") {
			fieldName := strings.TrimPrefix(attributeName, "field_")
			fieldValue := dynamoDBAttributeToFieldValue(attributeValue)
			if fieldValue != nil {
				ticketData.Fields[fieldName] = fieldValue
			}
		}
	}

	return ticketData
}

// dynamoDBItemToProtobufWithProjection converts a DynamoDB item to TicketData with field projection
func (d *DynamoDBStorage) dynamoDBItemToProtobufWithProjection(item map[string]types.AttributeValue, projectedFields []string) *ticketpb.TicketData {
	// If no projection specified, use the standard conversion
	if len(projectedFields) == 0 {
		return dynamoDBItemToProtobuf(item)
	}

	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Create a set of projected fields for quick lookup
	projectedFieldsSet := make(map[string]bool)
	// Always include essential core fields by default
	projectedFieldsSet["id"] = true
	projectedFieldsSet["tenant"] = true
	projectedFieldsSet["created_at"] = true
	projectedFieldsSet["updated_at"] = true

	for _, field := range projectedFields {
		projectedFieldsSet[field] = true
	}

	// Always extract core fields (required for record identification and metadata)
	if pkAttr, ok := item["pk"]; ok {
		if pkVal, ok := pkAttr.(*types.AttributeValueMemberS); ok {
			ticketData.Id = pkVal.Value
		}
	}

	if tenantAttr, ok := item["tenant"]; ok {
		if tenantVal, ok := tenantAttr.(*types.AttributeValueMemberS); ok {
			ticketData.Tenant = tenantVal.Value
		}
	}

	if createdAttr, ok := item["created_at"]; ok {
		if createdVal, ok := createdAttr.(*types.AttributeValueMemberS); ok {
			ticketData.CreatedAt = createdVal.Value
		}
	}

	if updatedAttr, ok := item["updated_at"]; ok {
		if updatedVal, ok := updatedAttr.(*types.AttributeValueMemberS); ok {
			ticketData.UpdatedAt = updatedVal.Value
		}
	}

	// Extract dynamic fields only if they are in the projection
	for attributeName, attributeValue := range item {
		if strings.HasPrefix(attributeName, "field_") {
			fieldName := strings.TrimPrefix(attributeName, "field_")
			if projectedFieldsSet[fieldName] {
				fieldValue := dynamoDBAttributeToFieldValue(attributeValue)
				if fieldValue != nil {
					ticketData.Fields[fieldName] = fieldValue
				}
			}
		}
	}

	return ticketData
}

// initializeDefaultGSIConfig sets up the default GSI configuration mapping with enhanced metadata
func initializeDefaultGSIConfig() map[string]GSIConfig {
	now := time.Now()
	gsiConfig := map[string]GSIConfig{
		"statusid": {
			IndexName:           "statusid-index",
			HashKey:             "field_statusid",
			RangeKey:            "pk",
			Priority:            1, // High priority for status queries
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false, // Will be updated when fetching actual indexes
		},
		"priorityid": {
			IndexName:           "priorityid-index",
			HashKey:             "field_priorityid",
			RangeKey:            "pk",
			Priority:            2,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"urgencyid": {
			IndexName:           "urgencyid-index",
			HashKey:             "field_urgencyid",
			RangeKey:            "pk",
			Priority:            3,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"categoryid": {
			IndexName:           "categoryid-index",
			HashKey:             "field_categoryid",
			RangeKey:            "pk",
			Priority:            4,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"departmentid": {
			IndexName:           "departmentid-index",
			HashKey:             "field_departmentid",
			RangeKey:            "pk",
			Priority:            5,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"groupid": {
			IndexName:           "groupid-index",
			HashKey:             "field_groupid",
			RangeKey:            "pk",
			Priority:            6,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"technicianid": {
			IndexName:           "technicianid-index",
			HashKey:             "field_technicianid",
			RangeKey:            "pk",
			Priority:            7,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"createdbyid": {
			IndexName:           "createdbyid-index",
			HashKey:             "field_createdbyid",
			RangeKey:            "pk",
			Priority:            8,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"impactid": {
			IndexName:           "impactid-index",
			HashKey:             "field_impactid",
			RangeKey:            "pk",
			Priority:            9,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"locationid": {
			IndexName:           "locationid-index",
			HashKey:             "field_locationid",
			RangeKey:            "pk",
			Priority:            10,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"requesttype": {
			IndexName:           "requesttype-index",
			HashKey:             "field_requesttype",
			RangeKey:            "pk",
			Priority:            11,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"sourceid": {
			IndexName:           "sourceid-index",
			HashKey:             "field_sourceid",
			RangeKey:            "pk",
			Priority:            12,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"subject": {
			IndexName:           "subject-index",
			HashKey:             "field_subject",
			RangeKey:            "pk",
			Priority:            13,
			EstimatedCost:       1.5,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "begins_with", "contains"},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"name": {
			IndexName:           "name-index",
			HashKey:             "field_name",
			RangeKey:            "pk",
			Priority:            14,
			EstimatedCost:       1.5,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "begins_with", "contains"},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"assignee": {
			IndexName:           "assignee-index",
			HashKey:             "field_assignee",
			RangeKey:            "pk",
			Priority:            2, // Medium priority for assignee queries
			EstimatedCost:       1.1,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "begins_with"},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"removed": {
			IndexName:           "removed-index",
			HashKey:             "field_removed",
			RangeKey:            "pk",
			Priority:            15,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"spam": {
			IndexName:           "spam-index",
			HashKey:             "field_spam",
			RangeKey:            "pk",
			Priority:            16,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"viprequest": {
			IndexName:           "viprequest-index",
			HashKey:             "field_viprequest",
			RangeKey:            "pk",
			Priority:            17,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"reopened": {
			IndexName:           "reopened-index",
			HashKey:             "field_reopened",
			RangeKey:            "pk",
			Priority:            18,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"approvalstatus": {
			IndexName:           "approvalstatus-index",
			HashKey:             "field_approvalstatus",
			RangeKey:            "pk",
			Priority:            19,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"approvaltype": {
			IndexName:           "approvaltype-index",
			HashKey:             "field_approvaltype",
			RangeKey:            "pk",
			Priority:            20,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"created_at": {
			IndexName:           "created-at-index",
			HashKey:             "created_at",
			RangeKey:            "pk",
			Priority:            21, // Lower priority for time-based queries
			EstimatedCost:       1.5,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">=", "begins_with"},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"updated_at": {
			IndexName:           "updated-at-index",
			HashKey:             "updated_at",
			RangeKey:            "pk",
			Priority:            3, // Lower priority for time-based queries
			EstimatedCost:       1.5,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">=", "begins_with"},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"updatedtime": {
			IndexName:           "updatedtime-index",
			HashKey:             "field_updatedtime",
			RangeKey:            "pk",
			Priority:            3, // High priority for time-based queries
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"createdtime": {
			IndexName:           "createdtime-index",
			HashKey:             "field_createdtime",
			RangeKey:            "pk",
			Priority:            2, // High priority for time-based queries
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"lastresolvedtime": {
			IndexName:           "lastresolvedtime-index",
			HashKey:             "field_lastresolvedtime",
			RangeKey:            "pk",
			Priority:            4, // High priority for SLA tracking
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"lastclosedtime": {
			IndexName:           "lastclosedtime-index",
			HashKey:             "field_lastclosedtime",
			RangeKey:            "pk",
			Priority:            5, // High priority for closure analytics
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"dueby": {
			IndexName:           "dueby-index",
			HashKey:             "field_dueby",
			RangeKey:            "pk",
			Priority:            6, // High priority for SLA calculations
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"oladueby": {
			IndexName:           "oladueby-index",
			HashKey:             "field_oladueby",
			RangeKey:            "pk",
			Priority:            7, // High priority for OLA tracking
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"ucdueby": {
			IndexName:           "ucdueby-index",
			HashKey:             "field_ucdueby",
			RangeKey:            "pk",
			Priority:            8, // High priority for UC SLA tracking
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"lastviolationtime": {
			IndexName:           "lastviolationtime-index",
			HashKey:             "field_lastviolationtime",
			RangeKey:            "pk",
			Priority:            9, // High priority for violation tracking
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"violatedslaid": {
			IndexName:           "violatedslaid-index",
			HashKey:             "field_violatedslaid",
			RangeKey:            "pk",
			Priority:            10, // Medium priority for SLA record lookup
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
	}

	// Add additional searchable fields
	addAdditionalSearchableFields(gsiConfig)

	// Create GSI configurations for all remaining searchable fields
	createGSIForAllSearchableFields(gsiConfig)

	return gsiConfig
}

// addAdditionalSearchableFields adds GSI configurations for all remaining searchable fields
func addAdditionalSearchableFields(gsiConfig map[string]GSIConfig) {
	now := time.Now()

	// Additional searchable fields with their GSI configurations
	additionalFields := map[string]GSIConfig{
		"requesterid": {
			IndexName:           "requesterid-index",
			HashKey:             "field_requesterid",
			RangeKey:            "pk",
			Priority:            22,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"templateid": {
			IndexName:           "templateid-index",
			HashKey:             "field_templateid",
			RangeKey:            "pk",
			Priority:            23,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"supportlevel": {
			IndexName:           "supportlevel-index",
			HashKey:             "field_supportlevel",
			RangeKey:            "pk",
			Priority:            24,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"servicecatalogid": {
			IndexName:           "servicecatalogid-index",
			HashKey:             "field_servicecatalogid",
			RangeKey:            "pk",
			Priority:            25,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"companyid": {
			IndexName:           "companyid-index",
			HashKey:             "field_companyid",
			RangeKey:            "pk",
			Priority:            26,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"closedby": {
			IndexName:           "closedby-index",
			HashKey:             "field_closedby",
			RangeKey:            "pk",
			Priority:            27,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"resolvedby": {
			IndexName:           "resolvedby-index",
			HashKey:             "field_resolvedby",
			RangeKey:            "pk",
			Priority:            28,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
		"vendorid": {
			IndexName:           "vendorid-index",
			HashKey:             "field_vendorid",
			RangeKey:            "pk",
			Priority:            29,
			EstimatedCost:       1.0,
			SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
			Status:              "ACTIVE",
			ProjectionType:      "ALL",
			ProjectedAttributes: []string{},
			CreatedAt:           now,
			ExistsInDynamoDB:    false,
		},
	}

	// Add all additional fields to the main config
	for fieldName, config := range additionalFields {
		if _, exists := gsiConfig[fieldName]; !exists {
			gsiConfig[fieldName] = config
		}
	}
}

// createGSIForAllSearchableFields creates GSI configurations for all searchable fields
func createGSIForAllSearchableFields(gsiConfig map[string]GSIConfig) {
	now := time.Now()

	// All searchable fields from the user's requirements
	allSearchableFields := []string{
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

	// Create GSI config for any missing fields
	priority := 30 // Start from priority 30 for auto-generated indexes
	for _, fieldName := range allSearchableFields {
		if _, exists := gsiConfig[fieldName]; !exists {
			// Determine supported operations based on field name patterns
			supportedOps := []string{"eq", "=", "ne", "!="}

			// Add comparison operators for numeric-looking fields
			if strings.HasSuffix(fieldName, "id") ||
				strings.HasSuffix(fieldName, "time") ||
				strings.HasSuffix(fieldName, "level") ||
				strings.HasSuffix(fieldName, "duration") ||
				strings.Contains(fieldName, "due") ||
				strings.Contains(fieldName, "escalation") {
				supportedOps = append(supportedOps, "lt", "<", "lte", "<=", "gt", ">", "gte", ">=")
			}

			// Add string operations for text fields
			if strings.Contains(fieldName, "name") ||
				strings.Contains(fieldName, "subject") ||
				strings.Contains(fieldName, "email") ||
				strings.Contains(fieldName, "config") {
				supportedOps = append(supportedOps, "begins_with", "contains")
			}

			gsiConfig[fieldName] = GSIConfig{
				IndexName:           fieldName + "-index",
				HashKey:             "field_" + fieldName,
				RangeKey:            "pk",
				Priority:            priority,
				EstimatedCost:       1.0,
				SupportedOps:        supportedOps,
				Status:              "ACTIVE",
				ProjectionType:      "ALL",
				ProjectedAttributes: []string{},
				CreatedAt:           now,
				ExistsInDynamoDB:    false,
			}
			priority++
		}
	}
}

// getGSIForField returns the GSI configuration for a given field name and tenant with enhanced logging
func (d *DynamoDBStorage) getGSIForField(tenantID, fieldName string) (*GSIConfig, bool) {
	if !d.enableGSI {
		log.Printf("GSI disabled, cannot use index for field: %s", fieldName)
		return nil, false
	}

	d.gsiMutex.RLock()
	defer d.gsiMutex.RUnlock()

	tenantGSI, tenantExists := d.gsiConfig[tenantID]
	if !tenantExists {
		log.Printf("No GSI configuration found for tenant: %s", tenantID)
		return nil, false
	}

	gsi, exists := tenantGSI[fieldName]
	if !exists {
		log.Printf("No GSI mapping found for field: %s in tenant: %s", fieldName, tenantID)
		return nil, false
	}

	if !gsi.ExistsInDynamoDB {
		log.Printf("GSI for field '%s' exists in config but not in DynamoDB for tenant: %s", fieldName, tenantID)
		log.Printf("Index status: %s, IndexName: %s", gsi.Status, gsi.IndexName)
		return nil, false
	}

	if gsi.Status != "ACTIVE" {
		log.Printf("GSI for field '%s' exists but is not ACTIVE (status: %s) for tenant: %s", fieldName, gsi.Status, tenantID)
		return nil, false
	}

	log.Printf("Selected GSI for field '%s' in tenant '%s': index=%s, priority=%d, cost=%.2f, status=%s",
		fieldName, tenantID, gsi.IndexName, gsi.Priority, gsi.EstimatedCost, gsi.Status)
	return &gsi, true
}

// canUseGSIForCondition checks if a search condition can use GSI query with enhanced validation
func (d *DynamoDBStorage) canUseGSIForCondition(tenantID string, condition SearchCondition) bool {
	// Check if GSI exists for this field
	gsiConfig, hasGSI := d.getGSIForField(tenantID, condition.Operand)
	if !hasGSI {
		return false
	}

	// Check if operator is supported by this specific GSI
	operatorLower := strings.ToLower(condition.Operator)
	for _, supportedOp := range gsiConfig.SupportedOps {
		if supportedOp == operatorLower {
			log.Printf("GSI condition validated: tenant=%s, field=%s, operator=%s, index=%s",
				tenantID, condition.Operand, condition.Operator, gsiConfig.IndexName)
			return true
		}
	}

	log.Printf("Operator '%s' not supported by GSI '%s' for field '%s' in tenant '%s'",
		condition.Operator, gsiConfig.IndexName, condition.Operand, tenantID)
	return false
}

// NewDynamoDBStorage creates a new tenant-aware DynamoDB storage instance with connection pooling
// Each tenant will get its own DynamoDB table for complete data isolation
func NewDynamoDBStorage(ctx context.Context, baseTableName, region, dbURL, dbAddress string) (*DynamoDBStorage, error) {
	// Clean up any quotes from environment variables
	dbURL = strings.Trim(dbURL, "\"'")
	dbAddress = strings.Trim(dbAddress, "\"'")

	// Determine the endpoint URL to use
	var endpointURL string
	if dbURL != "" {
		endpointURL = dbURL
		log.Printf("Using DynamoDB URL from DYNAMODB_URL: %s", dbURL)
	} else if dbAddress != "" {
		// If address is provided without protocol, assume http
		if !strings.HasPrefix(dbAddress, "http://") && !strings.HasPrefix(dbAddress, "https://") {
			endpointURL = "http://" + dbAddress
		} else {
			endpointURL = dbAddress
		}
		log.Printf("Using DynamoDB address from DYNAMODB_ADDRESS: %s", endpointURL)
	}

	// Load AWS configuration with connection pooling optimizations
	configOptions := []func(*config.LoadOptions) error{
		config.WithRegion(region),
		config.WithRetryMaxAttempts(3),
		config.WithRetryMode(aws.RetryModeAdaptive),
	}

	// For local DynamoDB, use static credentials if endpoint is configured
	if endpointURL != "" {
		log.Printf("Local DynamoDB detected, using static credentials")
		configOptions = append(configOptions, config.WithCredentialsProvider(
			aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
				return aws.Credentials{
					AccessKeyID:     "dummy",
					SecretAccessKey: "dummy",
					Source:          "Hardcoded",
				}, nil
			})),
		))
	}

	// Add custom endpoint resolver if URL/address is provided
	if endpointURL != "" {
		log.Printf("Configuring DynamoDB client with endpoint: %s", endpointURL)
		configOptions = append(configOptions, config.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				if service == dynamodb.ServiceID {
					log.Printf("Resolving DynamoDB endpoint: %s for region: %s", endpointURL, region)
					return aws.Endpoint{
						URL:           endpointURL,
						SigningRegion: region,
					}, nil
				}
				return aws.Endpoint{}, fmt.Errorf("unknown endpoint requested")
			}),
		))
	} else {
		log.Printf("No custom DynamoDB endpoint configured, using AWS default")
	}

	cfg, err := config.LoadDefaultConfig(ctx, configOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create DynamoDB client with connection pooling
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		// Configure HTTP client with connection pooling
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		}

		o.HTTPClient = &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		}
	})

	// Check if GSI is enabled via environment variable
	enableGSI := strings.ToLower(os.Getenv("DYNAMODB_ENABLE_GSI")) == "true"
	if enableGSI {
		log.Printf("GSI (Global Secondary Index) support is ENABLED")
	} else {
		log.Printf("GSI (Global Secondary Index) support is DISABLED")
	}

	storage := &DynamoDBStorage{
		client:        client,
		baseTableName: baseTableName,
		tenantTables:  sync.Map{},
		gsiConfig:     make(map[string]map[string]GSIConfig), // tenant -> field -> GSI config
		enableGSI:     enableGSI,
	}

	// Test the connection by listing tables
	log.Printf("Testing DynamoDB connection...")
	testCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	_, err = client.ListTables(testCtx, &dynamodb.ListTablesInput{})
	if err != nil {
		log.Printf("ERROR: Failed to connect to DynamoDB: %v", err)
		log.Printf("Make sure DynamoDB is running and accessible at the configured endpoint")
		if endpointURL != "" {
			log.Printf("Configured endpoint: %s", endpointURL)
		}
		return nil, fmt.Errorf("failed to connect to DynamoDB: %w", err)
	}

	log.Printf("DynamoDB connection test successful!")
	if endpointURL != "" {
		log.Printf("Successfully initialized tenant-aware DynamoDB storage with base table: %s in region: %s at endpoint: %s", baseTableName, region, endpointURL)
	} else {
		log.Printf("Successfully initialized tenant-aware DynamoDB storage with base table: %s in region: %s", baseTableName, region)
	}
	return storage, nil
}

// mapAPIFieldToDynamoDBAttribute maps API field names to DynamoDB attribute names
func (d *DynamoDBStorage) mapAPIFieldToDynamoDBAttribute(field string) string {
	// Core fields mapping
	switch field {
	case "id":
		return "pk"
	case "tenant":
		return "tenant"
	case "created_at":
		return "created_at"
	case "updated_at":
		return "updated_at"
	default:
		// Dynamic fields are prefixed with "field_"
		return "field_" + field
	}
}

// buildProjectionExpression builds DynamoDB ProjectionExpression and ExpressionAttributeNames
func (d *DynamoDBStorage) buildProjectionExpression(projectedFields []string) (string, map[string]string) {
	if len(projectedFields) == 0 {
		return "", nil // No projection, return all fields
	}

	// Always include essential core fields by default
	fieldsToProject := make(map[string]bool)
	fieldsToProject["id"] = true         // Always include ID for record identification
	fieldsToProject["tenant"] = true     // Always include tenant for multi-tenancy
	fieldsToProject["created_at"] = true // Always include created timestamp
	fieldsToProject["updated_at"] = true // Always include updated timestamp

	// Add user-specified fields
	for _, field := range projectedFields {
		fieldsToProject[field] = true
	}

	var projectionParts []string
	expressionAttributeNames := make(map[string]string)
	i := 0

	for field := range fieldsToProject {
		attributeName := d.mapAPIFieldToDynamoDBAttribute(field)
		nameKey := fmt.Sprintf("#proj%d", i)

		expressionAttributeNames[nameKey] = attributeName
		projectionParts = append(projectionParts, nameKey)
		i++
	}

	projectionExpression := strings.Join(projectionParts, ", ")
	log.Printf("Built projection expression with %d fields (including core fields: id, tenant, created_at, updated_at): %s", len(fieldsToProject), projectionExpression)
	return projectionExpression, expressionAttributeNames
}

// sanitizeTenantID converts tenant ID to a valid DynamoDB table name component
func (d *DynamoDBStorage) sanitizeTenantID(tenantID string) string {
	// Convert to lowercase and replace invalid characters with underscores
	sanitized := strings.ToLower(tenantID)
	reg := regexp.MustCompile(`[^a-z0-9_-]`)
	sanitized = reg.ReplaceAllString(sanitized, "_")

	// Ensure it doesn't start with a number
	if len(sanitized) > 0 && sanitized[0] >= '0' && sanitized[0] <= '9' {
		sanitized = "t_" + sanitized
	}

	// Limit length to avoid exceeding DynamoDB table name limits
	if len(sanitized) > 200 { // Leave room for base table name and separator
		sanitized = sanitized[:200]
	}

	return sanitized
}

// getTenantTableName generates the table name for a specific tenant
func (d *DynamoDBStorage) getTenantTableName(tenantID string) string {
	sanitizedTenant := d.sanitizeTenantID(tenantID)
	return fmt.Sprintf("%s_%s", d.baseTableName, sanitizedTenant)
}

// ensureTenantTable ensures that a table exists for the given tenant
// Uses in-memory map to avoid repeated table existence checks
func (d *DynamoDBStorage) ensureTenantTable(ctx context.Context, tenantID string) error {
	tableName := d.getTenantTableName(tenantID)

	// Check if table is already known to exist
	if _, exists := d.tenantTables.Load(tenantID); exists {
		return nil
	}

	// Use mutex to prevent concurrent table creation for the same tenant
	d.tableMutex.Lock()
	defer d.tableMutex.Unlock()

	// Double-check after acquiring lock
	if _, exists := d.tenantTables.Load(tenantID); exists {
		return nil
	}

	// Check if table exists in DynamoDB
	_, err := d.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(tableName),
	})

	if err != nil {
		// Table doesn't exist, create it
		if err := d.createTableIfNotExists(ctx, tableName); err != nil {
			return fmt.Errorf("failed to create table for tenant %s: %w", tenantID, err)
		}
		log.Printf("Created new DynamoDB table for tenant %s: %s", tenantID, tableName)
	} else {
		log.Printf("Found existing DynamoDB table for tenant %s: %s", tenantID, tableName)
	}

	// Store in map to avoid future checks
	d.tenantTables.Store(tenantID, tableName)

	// Initialize GSI configuration for this tenant (all indexes are already created with the table)
	d.initializeTenantGSIConfig(tenantID)

	return nil
}

// validateSearchFields validates that all search conditions use only supported business-critical fields
func (d *DynamoDBStorage) validateSearchFields(conditions []SearchCondition) error {
	// Define exactly the same 19 business-critical fields that have GSI indexes (matching CSV structure)
	supportedFields := map[string]bool{
		"requesterid":       true, // High - Many unique users → frequent search filter
		"technicianid":      true, // High - For assignment filtering and reporting
		"groupid":           true, // High - Workgroup-based queries
		"statusid":          true, // Medium - Filter tickets by status efficiently
		"priorityid":        true, // Medium - Common SLA-related queries
		"urgencyid":         true, // Medium - Helps with SLA escalation checks
		"categoryid":        true, // High - Frequent search filter for service categorization
		"companyid":         true, // High - Multi-tenant isolation and reporting
		"departmentid":      true, // High - Department-based filtering
		"locationid":        true, // High - Filter tickets by location
		"createdtime":       true, // High - Sorting and time-based range queries
		"updatedtime":       true, // High - Recent activity queries
		"lastresolvedtime":  true, // High - SLA performance tracking
		"lastclosedtime":    true, // High - Closure analytics
		"dueby":             true, // High - SLA calculations and escalation jobs
		"oladueby":          true, // High - OLA tracking for internal teams
		"ucdueby":           true, // High - UC SLA tracking
		"lastviolationtime": true, // High - SLA/OLA violation tracking and reporting
		"violatedslaid":     true, // Medium - Specific violated SLA record lookup
	}

	// Check each condition
	for i, condition := range conditions {
		// Check for empty field name
		if condition.Operand == "" {
			return fmt.Errorf("search condition %d has empty field name. All conditions must specify a valid field name. Supported fields: %v",
				i+1, d.getSupportedFieldsList())
		}

		// Check if field is supported
		if !supportedFields[condition.Operand] {
			return fmt.Errorf("field '%s' is not supported for searching. Supported fields: %v",
				condition.Operand, d.getSupportedFieldsList())
		}
	}

	return nil
}

// getSupportedFieldsList returns a sorted list of supported business-critical search fields (matching CSV structure)
func (d *DynamoDBStorage) getSupportedFieldsList() []string {
	return []string{
		"categoryid", "companyid", "createdtime", "departmentid", "dueby",
		"groupid", "lastclosedtime", "lastresolvedtime", "lastviolationtime", "locationid",
		"oladueby", "priorityid", "requesterid", "statusid", "technicianid",
		"ucdueby", "updatedtime", "urgencyid", "violatedslaid",
	}
}

// initializeTenantGSIConfig initializes the GSI configuration for a tenant (business-critical fields only)
func (d *DynamoDBStorage) initializeTenantGSIConfig(tenantID string) {
	d.gsiMutex.Lock()
	defer d.gsiMutex.Unlock()

	// Initialize tenant GSI config if not exists
	if d.gsiConfig[tenantID] == nil {
		d.gsiConfig[tenantID] = make(map[string]GSIConfig)
	}

	// Define exactly the same 19 business-critical fields that are created with the table (matching CSV structure)
	businessFields := map[string]bool{
		"requesterid":       true, // High - Many unique users → frequent search filter
		"technicianid":      true, // High - For assignment filtering and reporting
		"groupid":           true, // High - Workgroup-based queries
		"statusid":          true, // Medium - Filter tickets by status efficiently
		"priorityid":        true, // Medium - Common SLA-related queries
		"urgencyid":         true, // Medium - Helps with SLA escalation checks
		"categoryid":        true, // High - Frequent search filter for service categorization
		"companyid":         true, // High - Multi-tenant isolation and reporting
		"departmentid":      true, // High - Department-based filtering
		"locationid":        true, // High - Filter tickets by location
		"createdtime":       true, // High - Sorting and time-based range queries
		"updatedtime":       true, // High - Recent activity queries
		"lastresolvedtime":  true, // High - SLA performance tracking
		"lastclosedtime":    true, // High - Closure analytics
		"dueby":             true, // High - SLA calculations and escalation jobs
		"oladueby":          true, // High - OLA tracking for internal teams
		"ucdueby":           true, // High - UC SLA tracking
		"lastviolationtime": true, // High - SLA/OLA violation tracking and reporting
		"violatedslaid":     true, // Medium - Specific violated SLA record lookup
	}

	// Load the default GSI configuration but only configure the business-critical fields
	defaultGSIConfig := initializeDefaultGSIConfig()
	configuredCount := 0

	for fieldName, config := range defaultGSIConfig {
		if businessFields[fieldName] {
			// Mark as existing (created with table)
			config.ExistsInDynamoDB = true
			config.Status = "ACTIVE"
			d.gsiConfig[tenantID][fieldName] = config
			configuredCount++
		}
		// Skip all other fields - they are not supported for searching
	}

	log.Printf("Initialized GSI configuration for tenant %s: %d indexes configured (business-critical fields only)",
		tenantID, configuredCount)
}

// generateBusinessGSIDefinitions creates attribute definitions and GSI indexes for business-critical searchable fields
func (d *DynamoDBStorage) generateBusinessGSIDefinitions() ([]types.AttributeDefinition, []types.GlobalSecondaryIndex) {
	// Define exactly 19 business-critical fields for GSI creation based on actual CSV data structure
	// These are the ONLY fields that will support efficient searching
	businessFields := []string{
		"requesterid",       // High - Many unique users → frequent search filter
		"technicianid",      // High - For assignment filtering and reporting
		"groupid",           // High - Workgroup-based queries
		"statusid",          // Medium - Filter tickets by status efficiently
		"priorityid",        // Medium - Common SLA-related queries
		"urgencyid",         // Medium - Helps with SLA escalation checks
		"categoryid",        // High - Frequent search filter for service categorization
		"companyid",         // High - Multi-tenant isolation and reporting
		"departmentid",      // High - Department-based filtering
		"locationid",        // High - Filter tickets by location
		"createdtime",       // High - Sorting and time-based range queries
		"updatedtime",       // High - Recent activity queries
		"lastresolvedtime",  // High - SLA performance tracking
		"lastclosedtime",    // High - Closure analytics
		"dueby",             // High - SLA calculations and escalation jobs
		"oladueby",          // High - OLA tracking for internal teams
		"ucdueby",           // High - UC SLA tracking
		"lastviolationtime", // High - SLA/OLA violation tracking and reporting
		"violatedslaid",     // Medium - Specific violated SLA record lookup
	}

	// Get the default GSI configuration
	defaultGSIConfig := initializeDefaultGSIConfig()

	// Track unique attributes to avoid duplicates
	attributeMap := make(map[string]types.ScalarAttributeType)
	var globalSecondaryIndexes []types.GlobalSecondaryIndex

	// Process exactly 19 business-critical fields
	for _, fieldName := range businessFields {
		gsiConfig, exists := defaultGSIConfig[fieldName]
		if !exists {
			log.Printf("Warning: Business field %s not found in GSI config", fieldName)
			continue
		}

		// Determine the attribute type based on the actual hash key field name
		attributeType := d.determineAttributeType(gsiConfig.HashKey)

		// Add to attribute map (will deduplicate automatically)
		attributeMap[gsiConfig.HashKey] = attributeType

		// Create the GSI definition
		gsi := types.GlobalSecondaryIndex{
			IndexName: aws.String(gsiConfig.IndexName),
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String(gsiConfig.HashKey),
					KeyType:       types.KeyTypeHash,
				},
				{
					AttributeName: aws.String(gsiConfig.RangeKey),
					KeyType:       types.KeyTypeRange,
				},
			},
			Projection: &types.Projection{
				ProjectionType: types.ProjectionTypeAll,
			},
			ProvisionedThroughput: &types.ProvisionedThroughput{
				ReadCapacityUnits:  aws.Int64(5),
				WriteCapacityUnits: aws.Int64(5),
			},
		}

		globalSecondaryIndexes = append(globalSecondaryIndexes, gsi)
	}

	// Convert attribute map to slice
	var attributeDefinitions []types.AttributeDefinition
	for attrName, attrType := range attributeMap {
		attributeDefinitions = append(attributeDefinitions, types.AttributeDefinition{
			AttributeName: aws.String(attrName),
			AttributeType: attrType,
		})
	}

	log.Printf("Generated %d attribute definitions and exactly %d business-critical GSI indexes (DynamoDB limit: 20)",
		len(attributeDefinitions), len(globalSecondaryIndexes))

	return attributeDefinitions, globalSecondaryIndexes
}

// determineAttributeType determines the DynamoDB attribute type based on field name patterns
func (d *DynamoDBStorage) determineAttributeType(fieldName string) types.ScalarAttributeType {
	// Most fields are strings by default to match CSV data
	// Only use Number type for fields that are guaranteed to be numeric

	// Remove "field_" prefix if present for pattern matching
	cleanFieldName := fieldName
	if strings.HasPrefix(fieldName, "field_") {
		cleanFieldName = strings.TrimPrefix(fieldName, "field_")
	}

	// Fields that should be Number type (rare, only if we're sure they're always numeric)
	numericFields := map[string]bool{
		// Add specific fields here if they're guaranteed to be numeric
		// "numeric_score": true,
	}

	if numericFields[cleanFieldName] {
		return types.ScalarAttributeTypeN
	}

	// Default to String type for maximum compatibility with CSV data
	// This includes all ID fields (statusid, priorityid, etc.) since CSV data contains them as strings
	return types.ScalarAttributeTypeS
}

// createTableIfNotExists creates a new DynamoDB table with the standard schema
func (d *DynamoDBStorage) createTableIfNotExists(ctx context.Context, tableName string) error {
	// Create table with fresh timeout (don't inherit parent context limitations)
	createCtx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Base attribute definitions
	attributeDefinitions := []types.AttributeDefinition{
		{
			AttributeName: aws.String("pk"),
			AttributeType: types.ScalarAttributeTypeS,
		},
	}

	// Add GSI attribute definitions if GSI is enabled
	var globalSecondaryIndexes []types.GlobalSecondaryIndex
	if d.enableGSI {
		// Generate exactly 19 business-critical GSI attributes and indexes
		gsiAttributes, gsiIndexes := d.generateBusinessGSIDefinitions()
		attributeDefinitions = append(attributeDefinitions, gsiAttributes...)
		globalSecondaryIndexes = gsiIndexes

		log.Printf("Creating table %s with %d GSI indexes", tableName, len(globalSecondaryIndexes))
	} else {
		log.Printf("Creating table %s without GSI indexes (GSI disabled)", tableName)
	}

	input := &dynamodb.CreateTableInput{
		TableName:            aws.String(tableName),
		AttributeDefinitions: attributeDefinitions,
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("pk"),
				KeyType:       types.KeyTypeHash,
			},
		},
		BillingMode: types.BillingModeProvisioned,
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(5),
			WriteCapacityUnits: aws.Int64(5),
		},
	}

	// Add GSI definitions if enabled
	if d.enableGSI && len(globalSecondaryIndexes) > 0 {
		input.GlobalSecondaryIndexes = globalSecondaryIndexes
	}

	_, err := d.client.CreateTable(createCtx, input)
	if err != nil {
		// Check if table already exists (race condition)
		if strings.Contains(err.Error(), "ResourceInUseException") ||
			strings.Contains(err.Error(), "Table already exists") {
			log.Printf("Table %s already exists (race condition), continuing", tableName)
			return nil
		}
		return fmt.Errorf("failed to create table %s: %w", tableName, err)
	}

	// Wait for table to become active with fresh context
	waiter := dynamodb.NewTableExistsWaiter(d.client)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer waitCancel()

	err = waiter.Wait(waitCtx, &dynamodb.DescribeTableInput{
		TableName: aws.String(tableName),
	}, 180*time.Second)

	if err != nil {
		return fmt.Errorf("table %s creation timed out: %w", tableName, err)
	}

	return nil
}

// CreateTicket stores a new ticket in the tenant-specific DynamoDB table with separate field attributes
func (d *DynamoDBStorage) CreateTicket(tenant string, ticketData *ticketpb.TicketData) error {
	// Use longer timeout for table creation scenarios
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Ensure tenant table exists (this may take time for first-time table creation)
	if err := d.ensureTenantTable(ctx, tenant); err != nil {
		return fmt.Errorf("failed to ensure tenant table: %w", err)
	}

	// Create a separate shorter context for the actual PutItem operation
	putCtx, putCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer putCancel()

	// Convert protobuf to DynamoDB item with separate field attributes
	item := protobufToDynamoDBItem(ticketData)

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	// Store in tenant-specific DynamoDB table with separate field attributes
	_, err := d.client.PutItem(putCtx, &dynamodb.PutItemInput{
		TableName:           aws.String(tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})

	if err != nil {
		return fmt.Errorf("failed to create ticket in tenant table %s: %w", tableName, err)
	}

	log.Printf("Created ticket %s for tenant %s in table %s with separate field attributes", ticketData.Id, tenant, tableName)
	return nil
}

// GetTicket retrieves a ticket by tenant and ID from the tenant-specific DynamoDB table
func (d *DynamoDBStorage) GetTicket(tenant, id string) (*ticketpb.TicketData, bool) {
	// Use longer timeout for table creation scenarios
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Ensure tenant table exists (this may take time for first-time table creation)
	if err := d.ensureTenantTable(ctx, tenant); err != nil {
		log.Printf("ERROR: Failed to ensure tenant table for %s: %v", tenant, err)
		return nil, false
	}

	// Create a separate shorter context for the actual GetItem operation
	getCtx, getCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer getCancel()

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	result, err := d.client.GetItem(getCtx, &dynamodb.GetItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: id},
		},
	})

	if err != nil {
		log.Printf("ERROR: Failed to get ticket from tenant table %s: %v", tableName, err)
		return nil, false
	}

	if result.Item == nil {
		return nil, false
	}

	// Convert DynamoDB item back to protobuf with separate field attributes
	ticketData := dynamoDBItemToProtobuf(result.Item)
	return ticketData, true
}

// UpdateTicket updates an existing ticket in the tenant-specific DynamoDB table
func (d *DynamoDBStorage) UpdateTicket(tenant string, ticketData *ticketpb.TicketData) bool {
	// Use longer timeout for table creation scenarios
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Ensure tenant table exists (this may take time for first-time table creation)
	if err := d.ensureTenantTable(ctx, tenant); err != nil {
		log.Printf("ERROR: Failed to ensure tenant table for %s: %v", tenant, err)
		return false
	}

	// Create a separate shorter context for the actual PutItem operation
	putCtx, putCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer putCancel()

	// Convert protobuf to DynamoDB item with separate field attributes
	item := protobufToDynamoDBItem(ticketData)

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	// Replace the entire item with condition that it exists
	_, err := d.client.PutItem(putCtx, &dynamodb.PutItemInput{
		TableName:           aws.String(tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})

	if err != nil {
		log.Printf("ERROR: Failed to update ticket in tenant table %s: %v", tableName, err)
		return false
	}

	log.Printf("Updated ticket %s for tenant %s in table %s with separate field attributes", ticketData.Id, tenant, tableName)
	return true
}

// DeleteTicket removes a ticket from the tenant-specific DynamoDB table and returns it
func (d *DynamoDBStorage) DeleteTicket(tenant, id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// First get the ticket to return it
	ticketData, found := d.GetTicket(tenant, id)
	if !found {
		return nil, false
	}

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	// Delete the item
	_, err := d.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: id},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})

	if err != nil {
		log.Printf("ERROR: Failed to delete ticket from tenant table %s: %v", tableName, err)
		return nil, false
	}

	log.Printf("Deleted ticket %s for tenant %s from table %s", id, tenant, tableName)
	return ticketData, true
}

// ListTickets retrieves all tickets for a tenant from the tenant-specific DynamoDB table
func (d *DynamoDBStorage) ListTickets(tenant string) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Ensure tenant table exists
	if err := d.ensureTenantTable(ctx, tenant); err != nil {
		log.Printf("ERROR: Failed to ensure tenant table for %s: %v", tenant, err)
		return nil, fmt.Errorf("failed to ensure tenant table: %w", err)
	}

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	// Scan the entire tenant table (since all items belong to this tenant)
	result, err := d.client.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String(tableName),
	})

	var tickets []*ticketpb.TicketData

	if err != nil {
		log.Printf("ERROR: Failed to list tickets from tenant table %s: %v", tableName, err)
		return tickets, err
	}

	// Convert DynamoDB items to tickets using separate field attributes
	for _, item := range result.Items {
		// Convert DynamoDB item back to protobuf with separate field attributes
		ticketData := dynamoDBItemToProtobuf(item)
		tickets = append(tickets, ticketData)
	}

	log.Printf("Listed %d tickets for tenant %s from table %s with separate field attributes", len(tickets), tenant, tableName)
	return tickets, nil
}

// listTicketsWithProjection retrieves all tickets for a tenant with field projection
func (d *DynamoDBStorage) listTicketsWithProjection(ctx context.Context, tableName string, projectedFields []string) ([]*ticketpb.TicketData, error) {
	// Build projection expression
	projectionExpression, expressionAttributeNames := d.buildProjectionExpression(projectedFields)

	// Prepare scan input
	scanInput := &dynamodb.ScanInput{
		TableName: aws.String(tableName),
	}

	// Add projection if specified
	if projectionExpression != "" {
		scanInput.ProjectionExpression = aws.String(projectionExpression)
		scanInput.ExpressionAttributeNames = expressionAttributeNames
	}

	// Scan the entire tenant table
	result, err := d.client.Scan(ctx, scanInput)
	if err != nil {
		log.Printf("ERROR: Failed to list tickets with projection from table %s: %v", tableName, err)
		return nil, err
	}

	// Convert DynamoDB items to tickets with projection awareness
	var tickets []*ticketpb.TicketData
	for _, item := range result.Items {
		ticketData := d.dynamoDBItemToProtobufWithProjection(item, projectedFields)
		tickets = append(tickets, ticketData)
	}

	log.Printf("Listed %d tickets with %d projected fields from table %s", len(tickets), len(projectedFields), tableName)
	return tickets, nil
}

// SearchTickets searches for tickets based on conditions with operand, operator, and value
// Uses GSI optimization when available, falls back to scan when necessary
// Only supports searching on the top 20 configured fields
func (d *DynamoDBStorage) SearchTickets(tenant string, conditions []SearchCondition) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()

	// Validate that all search fields are supported (top 20 only)
	if err := d.validateSearchFields(conditions); err != nil {
		return nil, fmt.Errorf("invalid search fields: %w", err)
	}

	// Ensure tenant table exists
	if err := d.ensureTenantTable(ctx, tenant); err != nil {
		log.Printf("ERROR: Failed to ensure tenant table for %s: %v", tenant, err)
		return nil, fmt.Errorf("failed to ensure tenant table: %w", err)
	}

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	// Analyze conditions to determine optimal search strategy
	return d.executeOptimizedSearch(ctx, tableName, tenant, conditions)
}

// SearchTicketsWithProjection searches for tickets with optional field projection
// Uses GSI optimization when available, falls back to scan when necessary
// Only supports searching on the top 20 configured fields
func (d *DynamoDBStorage) SearchTicketsWithProjection(tenant string, request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()

	// Validate that all search fields are supported (top 20 only)
	if err := d.validateSearchFields(request.Conditions); err != nil {
		return nil, fmt.Errorf("invalid search fields: %w", err)
	}

	// Ensure tenant table exists
	if err := d.ensureTenantTable(ctx, tenant); err != nil {
		log.Printf("ERROR: Failed to ensure tenant table for %s: %v", tenant, err)
		return nil, fmt.Errorf("failed to ensure tenant table: %w", err)
	}

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	// Analyze conditions to determine optimal search strategy with projection
	return d.executeOptimizedSearchWithProjection(ctx, tableName, tenant, request.Conditions, request.ProjectedFields)
}

// executeOptimizedSearch determines the best search strategy and executes it
func (d *DynamoDBStorage) executeOptimizedSearch(ctx context.Context, tableName, tenant string, conditions []SearchCondition) ([]*ticketpb.TicketData, error) {
	if len(conditions) == 0 {
		// No conditions, return all tickets
		return d.ListTickets(tenant)
	}

	// Separate conditions into GSI-supported and non-GSI-supported
	var gsiConditions []SearchCondition
	var scanConditions []SearchCondition

	for _, condition := range conditions {
		if d.canUseGSIForCondition(tenant, condition) {
			gsiConditions = append(gsiConditions, condition)
		} else {
			scanConditions = append(scanConditions, condition)
		}
	}

	log.Printf("Search strategy: %d GSI conditions, %d scan conditions", len(gsiConditions), len(scanConditions))

	// Strategy 1: All conditions can use GSI
	if len(gsiConditions) == len(conditions) {
		return d.executeGSIOnlySearch(ctx, tableName, tenant, gsiConditions)
	}

	// Strategy 2: Some conditions can use GSI, others need scan
	if len(gsiConditions) > 0 {
		return d.executeHybridSearch(ctx, tableName, tenant, gsiConditions, scanConditions)
	}

	// Strategy 3: No GSI support, fallback to scan
	return d.executeScanSearch(ctx, tableName, conditions)
}

// executeOptimizedSearchWithProjection determines the best search strategy with field projection
func (d *DynamoDBStorage) executeOptimizedSearchWithProjection(ctx context.Context, tableName, tenant string, conditions []SearchCondition, projectedFields []string) ([]*ticketpb.TicketData, error) {
	if len(conditions) == 0 {
		// No conditions, return all tickets with projection
		return d.listTicketsWithProjection(ctx, tableName, projectedFields)
	}

	// Separate conditions into GSI-supported and non-GSI-supported
	var gsiConditions []SearchCondition
	var scanConditions []SearchCondition

	for _, condition := range conditions {
		if d.canUseGSIForCondition(tenant, condition) {
			gsiConditions = append(gsiConditions, condition)
		} else {
			scanConditions = append(scanConditions, condition)
		}
	}

	log.Printf("Search strategy with projection: %d GSI conditions, %d scan conditions, %d projected fields",
		len(gsiConditions), len(scanConditions), len(projectedFields))

	// Strategy 1: All conditions can use GSI
	if len(gsiConditions) == len(conditions) {
		return d.executeGSIOnlySearchWithProjection(ctx, tableName, tenant, gsiConditions, projectedFields)
	}

	// Strategy 2: Some conditions can use GSI, others need scan
	if len(gsiConditions) > 0 {
		return d.executeHybridSearchWithProjection(ctx, tableName, tenant, gsiConditions, scanConditions, projectedFields)
	}

	// Strategy 3: No GSI support, fallback to scan
	return d.executeScanSearchWithProjection(ctx, tableName, conditions, projectedFields)
}

// executeGSIOnlySearch handles cases where all conditions can use GSI
func (d *DynamoDBStorage) executeGSIOnlySearch(ctx context.Context, tableName, tenant string, conditions []SearchCondition) ([]*ticketpb.TicketData, error) {
	if len(conditions) == 1 {
		// Single condition - use single GSI query
		condition := conditions[0]
		gsiConfig, _ := d.getGSIForField(tenant, condition.Operand)

		log.Printf("Executing single GSI query for field '%s' using index '%s'",
			condition.Operand, gsiConfig.IndexName)

		result, err := d.executeGSIQuery(ctx, tableName, *gsiConfig, condition)
		if err != nil {
			log.Printf("GSI query failed, falling back to scan: %v", err)
			return d.executeScanSearch(ctx, tableName, conditions)
		}

		// Log single query metrics
		d.logGSIQueryMetrics([]GSIQueryResult{*result})

		return d.getTicketsByIDs(ctx, tableName, result.TicketIDs)
	}

	// Multiple conditions - execute multiple GSI queries concurrently and intersect
	log.Printf("Executing %d GSI queries concurrently for multiple field search", len(conditions))
	gsiResults, err := d.executeGSIQueriesConcurrently(ctx, tableName, tenant, conditions)
	if err != nil {
		log.Printf("Concurrent GSI queries failed, falling back to scan: %v", err)
		return d.executeScanSearch(ctx, tableName, conditions)
	}

	// Log GSI query metrics
	d.logGSIQueryMetrics(gsiResults)

	// Find intersection of all results
	commonTicketIDs := d.intersectTicketIDs(gsiResults)
	log.Printf("Found %d common ticket IDs from intersection of %d GSI query results",
		len(commonTicketIDs), len(gsiResults))

	return d.getTicketsByIDs(ctx, tableName, commonTicketIDs)
}

// executeGSIOnlySearchWithProjection handles GSI-only search with field projection
func (d *DynamoDBStorage) executeGSIOnlySearchWithProjection(ctx context.Context, tableName, tenant string, conditions []SearchCondition, projectedFields []string) ([]*ticketpb.TicketData, error) {
	if len(conditions) == 1 {
		// Single condition - use single GSI query with projection
		condition := conditions[0]
		gsiConfig, _ := d.getGSIForField(tenant, condition.Operand)

		log.Printf("Executing single GSI query with projection for field '%s' using index '%s'",
			condition.Operand, gsiConfig.IndexName)

		result, err := d.executeGSIQueryWithProjection(ctx, tableName, *gsiConfig, condition, projectedFields)
		if err != nil {
			log.Printf("GSI query with projection failed, falling back to scan: %v", err)
			return d.executeScanSearchWithProjection(ctx, tableName, conditions, projectedFields)
		}

		// Log single query metrics
		d.logGSIQueryMetrics([]GSIQueryResult{*result})

		return d.getTicketsByIDsWithProjection(ctx, tableName, result.TicketIDs, projectedFields)
	}

	// Multiple conditions - execute multiple GSI queries concurrently and intersect
	log.Printf("Executing %d GSI queries concurrently with projection for multiple field search", len(conditions))
	gsiResults, err := d.executeGSIQueriesConcurrentlyWithProjection(ctx, tableName, tenant, conditions, projectedFields)
	if err != nil {
		log.Printf("Concurrent GSI queries with projection failed, falling back to scan: %v", err)
		return d.executeScanSearchWithProjection(ctx, tableName, conditions, projectedFields)
	}

	// Log GSI query metrics
	d.logGSIQueryMetrics(gsiResults)

	// Find intersection of all results
	commonTicketIDs := d.intersectTicketIDs(gsiResults)
	log.Printf("Found %d common ticket IDs from intersection of %d GSI query results with projection",
		len(commonTicketIDs), len(gsiResults))

	return d.getTicketsByIDsWithProjection(ctx, tableName, commonTicketIDs, projectedFields)
}

// executeHybridSearchWithProjection handles mixed GSI and scan conditions with projection
func (d *DynamoDBStorage) executeHybridSearchWithProjection(ctx context.Context, tableName, tenant string, gsiConditions, scanConditions []SearchCondition, projectedFields []string) ([]*ticketpb.TicketData, error) {
	// First, get results from GSI conditions with projection
	gsiTickets, err := d.executeGSIOnlySearchWithProjection(ctx, tableName, tenant, gsiConditions, projectedFields)
	if err != nil {
		log.Printf("GSI search with projection failed in hybrid mode, falling back to full scan: %v", err)
		allConditions := append(gsiConditions, scanConditions...)
		return d.executeScanSearchWithProjection(ctx, tableName, allConditions, projectedFields)
	}

	// Then filter GSI results with scan conditions
	return d.filterTicketsWithConditions(gsiTickets, scanConditions), nil
}

// executeHybridSearch handles cases with mixed GSI and scan conditions
func (d *DynamoDBStorage) executeHybridSearch(ctx context.Context, tableName, tenant string, gsiConditions, scanConditions []SearchCondition) ([]*ticketpb.TicketData, error) {
	// First, get results from GSI conditions
	gsiTickets, err := d.executeGSIOnlySearch(ctx, tableName, tenant, gsiConditions)
	if err != nil {
		log.Printf("GSI search failed in hybrid mode, falling back to full scan: %v", err)
		allConditions := append(gsiConditions, scanConditions...)
		return d.executeScanSearch(ctx, tableName, allConditions)
	}

	// Then filter GSI results with scan conditions
	return d.filterTicketsWithConditions(gsiTickets, scanConditions), nil
}

// executeScanSearch falls back to the original scan-based approach
func (d *DynamoDBStorage) executeScanSearch(ctx context.Context, tableName string, conditions []SearchCondition) ([]*ticketpb.TicketData, error) {
	log.Printf("Using scan-based search for %d conditions", len(conditions))

	// Build filter expression from conditions
	filterExpression, expressionAttributeNames, expressionAttributeValues, err := d.buildFilterExpression(conditions)
	if err != nil {
		return nil, fmt.Errorf("failed to build filter expression: %w", err)
	}

	// Prepare scan input
	scanInput := &dynamodb.ScanInput{
		TableName: aws.String(tableName),
	}

	// Add filter expression if conditions exist
	if filterExpression != "" {
		scanInput.FilterExpression = aws.String(filterExpression)
		scanInput.ExpressionAttributeNames = expressionAttributeNames
		scanInput.ExpressionAttributeValues = expressionAttributeValues
	}

	// Execute scan
	result, err := d.client.Scan(ctx, scanInput)
	if err != nil {
		log.Printf("ERROR: Failed to search tickets from tenant table %s: %v", tableName, err)
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}

	// Convert DynamoDB items to tickets using separate field attributes
	var tickets []*ticketpb.TicketData
	for _, item := range result.Items {
		ticketData := dynamoDBItemToProtobuf(item)
		tickets = append(tickets, ticketData)
	}

	log.Printf("Scan search found %d tickets matching conditions", len(tickets))
	return tickets, nil
}

// executeScanSearchWithProjection performs scan-based search with field projection
func (d *DynamoDBStorage) executeScanSearchWithProjection(ctx context.Context, tableName string, conditions []SearchCondition, projectedFields []string) ([]*ticketpb.TicketData, error) {
	log.Printf("Using scan-based search for %d conditions with %d projected fields", len(conditions), len(projectedFields))

	// Build filter expression from conditions
	filterExpression, filterAttributeNames, expressionAttributeValues, err := d.buildFilterExpression(conditions)
	if err != nil {
		return nil, fmt.Errorf("failed to build filter expression: %w", err)
	}

	// Build projection expression
	projectionExpression, projectionAttributeNames := d.buildProjectionExpression(projectedFields)

	// Merge expression attribute names from filter and projection
	expressionAttributeNames := make(map[string]string)
	for k, v := range filterAttributeNames {
		expressionAttributeNames[k] = v
	}
	for k, v := range projectionAttributeNames {
		expressionAttributeNames[k] = v
	}

	// Prepare scan input
	scanInput := &dynamodb.ScanInput{
		TableName: aws.String(tableName),
	}

	// Add filter expression if conditions exist
	if filterExpression != "" {
		scanInput.FilterExpression = aws.String(filterExpression)
		scanInput.ExpressionAttributeValues = expressionAttributeValues
	}

	// Add projection expression if specified
	if projectionExpression != "" {
		scanInput.ProjectionExpression = aws.String(projectionExpression)
	}

	// Add expression attribute names if any exist
	if len(expressionAttributeNames) > 0 {
		scanInput.ExpressionAttributeNames = expressionAttributeNames
	}

	// Execute scan
	result, err := d.client.Scan(ctx, scanInput)
	if err != nil {
		log.Printf("ERROR: Failed to search tickets with projection from table %s: %v", tableName, err)
		return nil, fmt.Errorf("failed to search tickets with projection: %w", err)
	}

	// Convert DynamoDB items to tickets with projection awareness
	var tickets []*ticketpb.TicketData
	for _, item := range result.Items {
		ticketData := d.dynamoDBItemToProtobufWithProjection(item, projectedFields)
		tickets = append(tickets, ticketData)
	}

	log.Printf("Scan search with projection found %d tickets matching conditions", len(tickets))
	return tickets, nil
}

// filterTicketsWithConditions applies scan conditions to a list of tickets
func (d *DynamoDBStorage) filterTicketsWithConditions(tickets []*ticketpb.TicketData, conditions []SearchCondition) []*ticketpb.TicketData {
	if len(conditions) == 0 {
		return tickets
	}

	var filtered []*ticketpb.TicketData
	for _, ticket := range tickets {
		if d.ticketMatchesConditions(ticket, conditions) {
			filtered = append(filtered, ticket)
		}
	}

	log.Printf("Filtered %d tickets to %d tickets using scan conditions", len(tickets), len(filtered))
	return filtered
}

// ticketMatchesConditions checks if a ticket matches all given conditions
func (d *DynamoDBStorage) ticketMatchesConditions(ticket *ticketpb.TicketData, conditions []SearchCondition) bool {
	for _, condition := range conditions {
		if !d.ticketMatchesCondition(ticket, condition) {
			return false
		}
	}
	return true
}

// ticketMatchesCondition checks if a ticket matches a single condition
func (d *DynamoDBStorage) ticketMatchesCondition(ticket *ticketpb.TicketData, condition SearchCondition) bool {
	// Get the field value from the ticket
	var fieldValue interface{}

	// Check core fields first
	switch condition.Operand {
	case "id":
		fieldValue = ticket.Id
	case "tenant":
		fieldValue = ticket.Tenant
	case "created_at":
		fieldValue = ticket.CreatedAt
	case "updated_at":
		fieldValue = ticket.UpdatedAt
	default:
		// Check dynamic fields
		if field, exists := ticket.Fields[condition.Operand]; exists {
			fieldValue = d.extractFieldValue(field)
		} else {
			return false // Field doesn't exist
		}
	}

	// Compare values based on operator
	return d.compareValues(fieldValue, condition.Operator, condition.Value)
}

// extractFieldValue extracts the actual value from a FieldValue protobuf
func (d *DynamoDBStorage) extractFieldValue(field *ticketpb.FieldValue) interface{} {
	if field == nil {
		return nil
	}

	switch v := field.Value.(type) {
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

// compareValues compares two values using the specified operator
func (d *DynamoDBStorage) compareValues(fieldValue interface{}, operator string, conditionValue interface{}) bool {
	// Convert both values to strings for comparison if they're not the same type
	fieldStr := fmt.Sprintf("%v", fieldValue)
	conditionStr := fmt.Sprintf("%v", conditionValue)

	switch strings.ToLower(operator) {
	case "eq", "=":
		return fieldStr == conditionStr
	case "ne", "!=", "<>":
		return fieldStr != conditionStr
	case "contains":
		return strings.Contains(fieldStr, conditionStr)
	case "begins_with":
		return strings.HasPrefix(fieldStr, conditionStr)
	case "gt", ">":
		return fieldStr > conditionStr
	case "lt", "<":
		return fieldStr < conditionStr
	case "gte", ">=":
		return fieldStr >= conditionStr
	case "lte", "<=":
		return fieldStr <= conditionStr
	default:
		return false
	}
}

// buildFilterExpression builds DynamoDB filter expression from search conditions
func (d *DynamoDBStorage) buildFilterExpression(conditions []SearchCondition) (string, map[string]string, map[string]types.AttributeValue, error) {
	if len(conditions) == 0 {
		return "", nil, nil, nil
	}

	var filterParts []string
	expressionAttributeNames := make(map[string]string)
	expressionAttributeValues := make(map[string]types.AttributeValue)

	// Core fields that don't need field_ prefix
	coreFields := map[string]bool{
		"id":         true,
		"tenant":     true,
		"created_at": true,
		"updated_at": true,
	}

	for i, condition := range conditions {
		// Determine the actual attribute name
		var attributeName string
		if coreFields[condition.Operand] {
			if condition.Operand == "id" {
				attributeName = "pk" // id is stored as pk in DynamoDB
			} else {
				attributeName = condition.Operand
			}
		} else {
			attributeName = "field_" + condition.Operand
		}

		// Create placeholders for attribute names and values
		nameKey := fmt.Sprintf("#attr%d", i)
		valueKey := fmt.Sprintf(":val%d", i)

		expressionAttributeNames[nameKey] = attributeName

		// Convert value to appropriate DynamoDB attribute value
		// For GSI fields, use string conversion to match GSI key types
		var attributeValue types.AttributeValue
		var err error

		if isGSIKeyField(condition.Operand) {
			// GSI fields are stored as strings, so use string conversion
			attributeValue, err = d.convertValueToGSIAttribute(condition.Value)
		} else {
			// Non-GSI fields can use their natural types
			attributeValue, err = d.convertValueToDynamoDBAttribute(condition.Value)
		}

		if err != nil {
			return "", nil, nil, fmt.Errorf("failed to convert value for condition %d: %w", i, err)
		}
		expressionAttributeValues[valueKey] = attributeValue

		// Build the condition expression based on operator
		var conditionExpr string
		switch strings.ToLower(condition.Operator) {
		case "eq", "=":
			conditionExpr = fmt.Sprintf("%s = %s", nameKey, valueKey)
		case "ne", "!=", "<>":
			conditionExpr = fmt.Sprintf("%s <> %s", nameKey, valueKey)
		case "gt", ">":
			conditionExpr = fmt.Sprintf("%s > %s", nameKey, valueKey)
		case "lt", "<":
			conditionExpr = fmt.Sprintf("%s < %s", nameKey, valueKey)
		case "gte", ">=":
			conditionExpr = fmt.Sprintf("%s >= %s", nameKey, valueKey)
		case "lte", "<=":
			conditionExpr = fmt.Sprintf("%s <= %s", nameKey, valueKey)
		case "contains":
			conditionExpr = fmt.Sprintf("contains(%s, %s)", nameKey, valueKey)
		case "begins_with":
			conditionExpr = fmt.Sprintf("begins_with(%s, %s)", nameKey, valueKey)
		default:
			return "", nil, nil, fmt.Errorf("unsupported operator: %s", condition.Operator)
		}

		filterParts = append(filterParts, conditionExpr)
	}

	// Join all conditions with AND
	filterExpression := strings.Join(filterParts, " AND ")

	return filterExpression, expressionAttributeNames, expressionAttributeValues, nil
}

// convertValueToDynamoDBAttribute converts a search value to appropriate DynamoDB AttributeValue
func (d *DynamoDBStorage) convertValueToDynamoDBAttribute(value interface{}) (types.AttributeValue, error) {
	switch v := value.(type) {
	case string:
		return &types.AttributeValueMemberS{Value: v}, nil
	case int:
		return &types.AttributeValueMemberN{Value: strconv.Itoa(v)}, nil
	case int64:
		return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)}, nil
	case float64:
		return &types.AttributeValueMemberN{Value: strconv.FormatFloat(v, 'f', -1, 64)}, nil
	case bool:
		return &types.AttributeValueMemberBOOL{Value: v}, nil
	case []byte:
		return &types.AttributeValueMemberB{Value: v}, nil
	default:
		// Try to convert to string as fallback
		return &types.AttributeValueMemberS{Value: fmt.Sprintf("%v", v)}, nil
	}
}

// convertValueToGSIAttribute converts a search value to String AttributeValue for GSI queries
// All GSI keys are defined as String type to handle CSV data consistently
func (d *DynamoDBStorage) convertValueToGSIAttribute(value interface{}) (types.AttributeValue, error) {
	// Convert all values to string for GSI compatibility
	switch v := value.(type) {
	case string:
		return &types.AttributeValueMemberS{Value: v}, nil
	case int:
		return &types.AttributeValueMemberS{Value: strconv.Itoa(v)}, nil
	case int64:
		return &types.AttributeValueMemberS{Value: strconv.FormatInt(v, 10)}, nil
	case float64:
		return &types.AttributeValueMemberS{Value: strconv.FormatFloat(v, 'f', -1, 64)}, nil
	case bool:
		return &types.AttributeValueMemberS{Value: strconv.FormatBool(v)}, nil
	case []byte:
		return &types.AttributeValueMemberS{Value: string(v)}, nil
	default:
		// Convert to string
		return &types.AttributeValueMemberS{Value: fmt.Sprintf("%v", v)}, nil
	}
}

// executeGSIQuery executes a query on a specific GSI for a given condition
func (d *DynamoDBStorage) executeGSIQuery(ctx context.Context, tableName string, gsiConfig GSIConfig, condition SearchCondition) (*GSIQueryResult, error) {
	// Convert value to String attribute value for GSI compatibility (all GSI keys are String type)
	attributeValue, err := d.convertValueToGSIAttribute(condition.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to convert value for GSI query: %w", err)
	}

	expressionAttributeNames := map[string]string{
		"#hashkey": gsiConfig.HashKey,
	}
	expressionAttributeValues := map[string]types.AttributeValue{
		":hashval": attributeValue,
	}

	var keyConditionExpression string

	// Handle different operators
	switch strings.ToLower(condition.Operator) {
	case "eq", "=":
		// For equality, we can query directly on the hash key
		keyConditionExpression = "#hashkey = :hashval"
	case "ne", "!=":
		// For not equal, we need to scan the GSI with a filter expression
		return d.executeGSIScanWithFilter(ctx, tableName, gsiConfig, condition)
	case "gt", ">", "lt", "<", "gte", ">=", "lte", "<=":
		// For range operations, we need to scan the GSI since we can't do range operations on hash key alone
		// We'll use the hash key with a placeholder and add a filter expression
		return d.executeGSIScanWithFilter(ctx, tableName, gsiConfig, condition)
	case "begins_with":
		// begins_with can work on hash key
		keyConditionExpression = "begins_with(#hashkey, :hashval)"
	default:
		return nil, fmt.Errorf("unsupported operator for GSI query: %s", condition.Operator)
	}

	// Build query input
	queryInput := &dynamodb.QueryInput{
		TableName:                 aws.String(tableName),
		IndexName:                 aws.String(gsiConfig.IndexName),
		KeyConditionExpression:    aws.String(keyConditionExpression),
		ExpressionAttributeNames:  expressionAttributeNames,
		ExpressionAttributeValues: expressionAttributeValues,
	}

	// No filter expression needed for equality and begins_with operations

	log.Printf("Executing GSI query on index %s with condition: %s %s %v",
		gsiConfig.IndexName, condition.Operand, condition.Operator, condition.Value)
	log.Printf("Query details - HashKey: %s, KeyCondition: %s",
		gsiConfig.HashKey, keyConditionExpression)

	result, err := d.client.Query(ctx, queryInput)
	if err != nil {
		log.Printf("GSI query failed on index %s: %v", gsiConfig.IndexName, err)
		log.Printf("Query input details:")
		log.Printf("  TableName: %s", aws.ToString(queryInput.TableName))
		log.Printf("  IndexName: %s", aws.ToString(queryInput.IndexName))
		log.Printf("  KeyConditionExpression: %s", aws.ToString(queryInput.KeyConditionExpression))
		log.Printf("  ExpressionAttributeNames: %v", queryInput.ExpressionAttributeNames)
		log.Printf("  ExpressionAttributeValues: %v", queryInput.ExpressionAttributeValues)

		// Check if the error is due to missing index
		if strings.Contains(err.Error(), "ValidationException") && strings.Contains(err.Error(), "key schema element") {
			log.Printf("ERROR: GSI index %s does not exist in DynamoDB table %s", gsiConfig.IndexName, tableName)
			log.Printf("This field should have been created with the table. Check if '%s' is in the top 20 supported fields.", condition.Operand)
		}

		return nil, fmt.Errorf("GSI query failed on index %s: %w", gsiConfig.IndexName, err)
	}

	// Extract ticket IDs from the results
	var ticketIDs []string
	for _, item := range result.Items {
		if pkAttr, exists := item["pk"]; exists {
			if pkVal, ok := pkAttr.(*types.AttributeValueMemberS); ok {
				ticketIDs = append(ticketIDs, pkVal.Value)
			}
		}
	}

	log.Printf("GSI query on %s returned %d results for condition %s %s %v",
		gsiConfig.IndexName, len(ticketIDs), condition.Operand, condition.Operator, condition.Value)

	// Create metrics for this query
	metrics := GSIQueryMetrics{
		IndexName:     gsiConfig.IndexName,
		ResultCount:   len(ticketIDs),
		ConditionUsed: condition,
		Success:       true,
	}

	return &GSIQueryResult{
		TicketIDs: ticketIDs,
		Items:     result.Items,
		Metrics:   metrics,
	}, nil
}

// executeGSIQueryWithProjection executes a GSI query with field projection
func (d *DynamoDBStorage) executeGSIQueryWithProjection(ctx context.Context, tableName string, gsiConfig GSIConfig, condition SearchCondition, projectedFields []string) (*GSIQueryResult, error) {
	// Convert value to String attribute value for GSI compatibility (all GSI keys are String type)
	attributeValue, err := d.convertValueToGSIAttribute(condition.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to convert value for GSI query with projection: %w", err)
	}

	expressionAttributeNames := map[string]string{
		"#hashkey": gsiConfig.HashKey,
	}
	expressionAttributeValues := map[string]types.AttributeValue{
		":hashval": attributeValue,
	}

	var keyConditionExpression string

	// Handle different operators
	switch strings.ToLower(condition.Operator) {
	case "eq", "=":
		keyConditionExpression = "#hashkey = :hashval"
	case "ne", "!=":
		return d.executeGSIScanWithFilterAndProjection(ctx, tableName, gsiConfig, condition, projectedFields)
	case "gt", ">", "lt", "<", "gte", ">=", "lte", "<=":
		return d.executeGSIScanWithFilterAndProjection(ctx, tableName, gsiConfig, condition, projectedFields)
	case "begins_with":
		keyConditionExpression = "begins_with(#hashkey, :hashval)"
	default:
		return nil, fmt.Errorf("unsupported operator for GSI query: %s", condition.Operator)
	}

	// Build projection expression
	projectionExpression, projectionAttributeNames := d.buildProjectionExpression(projectedFields)

	// Merge expression attribute names
	for k, v := range projectionAttributeNames {
		expressionAttributeNames[k] = v
	}

	// Build query input
	queryInput := &dynamodb.QueryInput{
		TableName:                 aws.String(tableName),
		IndexName:                 aws.String(gsiConfig.IndexName),
		KeyConditionExpression:    aws.String(keyConditionExpression),
		ExpressionAttributeNames:  expressionAttributeNames,
		ExpressionAttributeValues: expressionAttributeValues,
	}

	// Add projection if specified
	if projectionExpression != "" {
		queryInput.ProjectionExpression = aws.String(projectionExpression)
	}

	log.Printf("Executing GSI query with projection on index %s with condition: %s %s %v",
		gsiConfig.IndexName, condition.Operand, condition.Operator, condition.Value)

	result, err := d.client.Query(ctx, queryInput)
	if err != nil {
		log.Printf("GSI query with projection failed on index %s: %v", gsiConfig.IndexName, err)
		return nil, fmt.Errorf("GSI query with projection failed on index %s: %w", gsiConfig.IndexName, err)
	}

	// Extract ticket IDs from the results
	var ticketIDs []string
	for _, item := range result.Items {
		if pkAttr, exists := item["pk"]; exists {
			if pkVal, ok := pkAttr.(*types.AttributeValueMemberS); ok {
				ticketIDs = append(ticketIDs, pkVal.Value)
			}
		}
	}

	log.Printf("GSI query with projection on %s returned %d results for condition %s %s %v",
		gsiConfig.IndexName, len(ticketIDs), condition.Operand, condition.Operator, condition.Value)

	// Create metrics for this query
	metrics := GSIQueryMetrics{
		IndexName:     gsiConfig.IndexName,
		ResultCount:   len(ticketIDs),
		ConditionUsed: condition,
		Success:       true,
	}

	return &GSIQueryResult{
		TicketIDs: ticketIDs,
		Items:     result.Items,
		Metrics:   metrics,
	}, nil
}

// Note: createMissingGSI function removed - only top 20 fields are supported for searching

// executeGSIScanWithFilter executes a scan on GSI with filter expression for range operations
func (d *DynamoDBStorage) executeGSIScanWithFilter(ctx context.Context, tableName string, gsiConfig GSIConfig, condition SearchCondition) (*GSIQueryResult, error) {
	// Convert value to String attribute value for GSI compatibility (all GSI keys are String type)
	attributeValue, err := d.convertValueToGSIAttribute(condition.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to convert value for GSI scan: %w", err)
	}

	expressionAttributeNames := map[string]string{
		"#filterkey": gsiConfig.HashKey,
	}
	expressionAttributeValues := map[string]types.AttributeValue{
		":filterval": attributeValue,
	}

	// Build filter expression based on operator
	var filterExpression string
	switch strings.ToLower(condition.Operator) {
	case "ne", "!=":
		filterExpression = "#filterkey <> :filterval"
	case "gt", ">":
		filterExpression = "#filterkey > :filterval"
	case "lt", "<":
		filterExpression = "#filterkey < :filterval"
	case "gte", ">=":
		filterExpression = "#filterkey >= :filterval"
	case "lte", "<=":
		filterExpression = "#filterkey <= :filterval"
	default:
		return nil, fmt.Errorf("unsupported operator for GSI scan: %s", condition.Operator)
	}

	// Execute the GSI scan with filter
	scanInput := &dynamodb.ScanInput{
		TableName:                 aws.String(tableName),
		IndexName:                 aws.String(gsiConfig.IndexName),
		FilterExpression:          aws.String(filterExpression),
		ExpressionAttributeNames:  expressionAttributeNames,
		ExpressionAttributeValues: expressionAttributeValues,
	}

	result, err := d.client.Scan(ctx, scanInput)
	if err != nil {
		return nil, fmt.Errorf("GSI scan failed on index %s: %w", gsiConfig.IndexName, err)
	}

	// Extract ticket IDs from the results
	var ticketIDs []string
	for _, item := range result.Items {
		if pkAttr, exists := item["pk"]; exists {
			if pkVal, ok := pkAttr.(*types.AttributeValueMemberS); ok {
				ticketIDs = append(ticketIDs, pkVal.Value)
			}
		}
	}

	log.Printf("GSI scan on %s returned %d results for condition %s %s %v",
		gsiConfig.IndexName, len(ticketIDs), condition.Operand, condition.Operator, condition.Value)

	// Create metrics for this scan
	metrics := GSIQueryMetrics{
		IndexName:     gsiConfig.IndexName,
		ResultCount:   len(ticketIDs),
		ConditionUsed: condition,
		Success:       true,
	}

	return &GSIQueryResult{
		TicketIDs: ticketIDs,
		Items:     result.Items,
		Metrics:   metrics,
	}, nil
}

// executeGSIScanWithFilterAndProjection executes a GSI scan with filter and projection
func (d *DynamoDBStorage) executeGSIScanWithFilterAndProjection(ctx context.Context, tableName string, gsiConfig GSIConfig, condition SearchCondition, projectedFields []string) (*GSIQueryResult, error) {
	// Convert value to String attribute value for GSI compatibility (all GSI keys are String type)
	attributeValue, err := d.convertValueToGSIAttribute(condition.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to convert value for GSI scan with projection: %w", err)
	}

	expressionAttributeNames := map[string]string{
		"#filterkey": gsiConfig.HashKey,
	}
	expressionAttributeValues := map[string]types.AttributeValue{
		":filterval": attributeValue,
	}

	// Build filter expression based on operator
	var filterExpression string
	switch strings.ToLower(condition.Operator) {
	case "ne", "!=":
		filterExpression = "#filterkey <> :filterval"
	case "gt", ">":
		filterExpression = "#filterkey > :filterval"
	case "lt", "<":
		filterExpression = "#filterkey < :filterval"
	case "gte", ">=":
		filterExpression = "#filterkey >= :filterval"
	case "lte", "<=":
		filterExpression = "#filterkey <= :filterval"
	default:
		return nil, fmt.Errorf("unsupported operator for GSI scan: %s", condition.Operator)
	}

	// Build projection expression
	projectionExpression, projectionAttributeNames := d.buildProjectionExpression(projectedFields)

	// Merge expression attribute names
	for k, v := range projectionAttributeNames {
		expressionAttributeNames[k] = v
	}

	// Execute the GSI scan with filter and projection
	scanInput := &dynamodb.ScanInput{
		TableName:                 aws.String(tableName),
		IndexName:                 aws.String(gsiConfig.IndexName),
		FilterExpression:          aws.String(filterExpression),
		ExpressionAttributeNames:  expressionAttributeNames,
		ExpressionAttributeValues: expressionAttributeValues,
	}

	// Add projection if specified
	if projectionExpression != "" {
		scanInput.ProjectionExpression = aws.String(projectionExpression)
	}

	result, err := d.client.Scan(ctx, scanInput)
	if err != nil {
		return nil, fmt.Errorf("GSI scan with projection failed on index %s: %w", gsiConfig.IndexName, err)
	}

	// Extract ticket IDs from the results
	var ticketIDs []string
	for _, item := range result.Items {
		if pkAttr, exists := item["pk"]; exists {
			if pkVal, ok := pkAttr.(*types.AttributeValueMemberS); ok {
				ticketIDs = append(ticketIDs, pkVal.Value)
			}
		}
	}

	log.Printf("GSI scan with projection on %s returned %d results for condition %s %s %v",
		gsiConfig.IndexName, len(ticketIDs), condition.Operand, condition.Operator, condition.Value)

	// Create metrics for this scan
	metrics := GSIQueryMetrics{
		IndexName:     gsiConfig.IndexName,
		ResultCount:   len(ticketIDs),
		ConditionUsed: condition,
		Success:       true,
	}

	return &GSIQueryResult{
		TicketIDs: ticketIDs,
		Items:     result.Items,
		Metrics:   metrics,
	}, nil
}

// intersectTicketIDs finds common ticket IDs across multiple GSI query results
func (d *DynamoDBStorage) intersectTicketIDs(results []GSIQueryResult) []string {
	if len(results) == 0 {
		return []string{}
	}

	if len(results) == 1 {
		return results[0].TicketIDs
	}

	// Count occurrences of each ticket ID
	idCounts := make(map[string]int)
	for _, result := range results {
		for _, ticketID := range result.TicketIDs {
			idCounts[ticketID]++
		}
	}

	// Find IDs that appear in all result sets
	var intersection []string
	requiredCount := len(results)
	for ticketID, count := range idCounts {
		if count == requiredCount {
			intersection = append(intersection, ticketID)
		}
	}

	log.Printf("Intersected %d result sets, found %d common ticket IDs", len(results), len(intersection))
	return intersection
}

// getTicketsByIDs retrieves full ticket data for a list of ticket IDs using batch get
func (d *DynamoDBStorage) getTicketsByIDs(ctx context.Context, tableName string, ticketIDs []string) ([]*ticketpb.TicketData, error) {
	if len(ticketIDs) == 0 {
		return []*ticketpb.TicketData{}, nil
	}

	var tickets []*ticketpb.TicketData

	// DynamoDB BatchGetItem has a limit of 100 items per request
	batchSize := 100
	for i := 0; i < len(ticketIDs); i += batchSize {
		end := i + batchSize
		if end > len(ticketIDs) {
			end = len(ticketIDs)
		}

		batch := ticketIDs[i:end]
		batchTickets, err := d.getBatchTickets(ctx, tableName, batch)
		if err != nil {
			return nil, fmt.Errorf("failed to get batch tickets: %w", err)
		}

		tickets = append(tickets, batchTickets...)
	}

	return tickets, nil
}

// getBatchTickets retrieves a batch of tickets using BatchGetItem
func (d *DynamoDBStorage) getBatchTickets(ctx context.Context, tableName string, ticketIDs []string) ([]*ticketpb.TicketData, error) {
	if len(ticketIDs) == 0 {
		return []*ticketpb.TicketData{}, nil
	}

	// Build request keys
	var keys []map[string]types.AttributeValue
	for _, ticketID := range ticketIDs {
		keys = append(keys, map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: ticketID},
		})
	}

	// Execute batch get
	batchInput := &dynamodb.BatchGetItemInput{
		RequestItems: map[string]types.KeysAndAttributes{
			tableName: {
				Keys: keys,
			},
		},
	}

	result, err := d.client.BatchGetItem(ctx, batchInput)
	if err != nil {
		return nil, fmt.Errorf("batch get item failed: %w", err)
	}

	// Convert results to tickets
	var tickets []*ticketpb.TicketData
	if items, exists := result.Responses[tableName]; exists {
		for _, item := range items {
			ticketData := dynamoDBItemToProtobuf(item)
			tickets = append(tickets, ticketData)
		}
	}

	return tickets, nil
}

// getTicketsByIDsWithProjection retrieves tickets by IDs with field projection
func (d *DynamoDBStorage) getTicketsByIDsWithProjection(ctx context.Context, tableName string, ticketIDs []string, projectedFields []string) ([]*ticketpb.TicketData, error) {
	if len(ticketIDs) == 0 {
		return []*ticketpb.TicketData{}, nil
	}

	var tickets []*ticketpb.TicketData

	// DynamoDB BatchGetItem has a limit of 100 items per request
	batchSize := 100
	for i := 0; i < len(ticketIDs); i += batchSize {
		end := i + batchSize
		if end > len(ticketIDs) {
			end = len(ticketIDs)
		}

		batch := ticketIDs[i:end]
		batchTickets, err := d.getBatchTicketsWithProjection(ctx, tableName, batch, projectedFields)
		if err != nil {
			return nil, fmt.Errorf("failed to get batch tickets with projection: %w", err)
		}

		tickets = append(tickets, batchTickets...)
	}

	return tickets, nil
}

// getBatchTicketsWithProjection retrieves a batch of tickets with field projection
func (d *DynamoDBStorage) getBatchTicketsWithProjection(ctx context.Context, tableName string, ticketIDs []string, projectedFields []string) ([]*ticketpb.TicketData, error) {
	if len(ticketIDs) == 0 {
		return []*ticketpb.TicketData{}, nil
	}

	// Build request keys
	var keys []map[string]types.AttributeValue
	for _, ticketID := range ticketIDs {
		keys = append(keys, map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: ticketID},
		})
	}

	// Build projection expression
	projectionExpression, expressionAttributeNames := d.buildProjectionExpression(projectedFields)

	// Prepare batch get input
	keysAndAttributes := types.KeysAndAttributes{
		Keys: keys,
	}

	// Add projection if specified
	if projectionExpression != "" {
		keysAndAttributes.ProjectionExpression = aws.String(projectionExpression)
		keysAndAttributes.ExpressionAttributeNames = expressionAttributeNames
	}

	// Execute batch get
	batchInput := &dynamodb.BatchGetItemInput{
		RequestItems: map[string]types.KeysAndAttributes{
			tableName: keysAndAttributes,
		},
	}

	result, err := d.client.BatchGetItem(ctx, batchInput)
	if err != nil {
		return nil, fmt.Errorf("batch get item with projection failed: %w", err)
	}

	// Convert results to tickets with projection awareness
	var tickets []*ticketpb.TicketData
	if items, exists := result.Responses[tableName]; exists {
		for _, item := range items {
			ticketData := d.dynamoDBItemToProtobufWithProjection(item, projectedFields)
			tickets = append(tickets, ticketData)
		}
	}

	return tickets, nil
}

// executeGSIQueriesConcurrently executes multiple GSI queries in parallel for better performance
func (d *DynamoDBStorage) executeGSIQueriesConcurrently(ctx context.Context, tableName, tenant string, conditions []SearchCondition) ([]GSIQueryResult, error) {
	type gsiQueryJob struct {
		condition SearchCondition
		gsiConfig GSIConfig
		index     int
	}

	type gsiQueryResponse struct {
		result *GSIQueryResult
		err    error
		index  int
	}

	// Prepare jobs for each GSI query
	var jobs []gsiQueryJob
	for i, condition := range conditions {
		gsiConfig, exists := d.getGSIForField(tenant, condition.Operand)
		if !exists {
			return nil, fmt.Errorf("no GSI configuration found for field: %s in tenant: %s", condition.Operand, tenant)
		}
		jobs = append(jobs, gsiQueryJob{
			condition: condition,
			gsiConfig: *gsiConfig,
			index:     i,
		})
	}

	// Create channels for job distribution and result collection
	jobChan := make(chan gsiQueryJob, len(jobs))
	resultChan := make(chan gsiQueryResponse, len(jobs))

	// Start worker goroutines (limit to reasonable number)
	numWorkers := len(jobs)
	if numWorkers > 5 { // Limit concurrent queries to avoid overwhelming DynamoDB
		numWorkers = 5
	}

	log.Printf("Starting %d worker goroutines for %d GSI queries", numWorkers, len(jobs))

	// Start workers
	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			for job := range jobChan {
				log.Printf("Worker %d executing GSI query for field '%s' using index '%s'",
					workerID, job.condition.Operand, job.gsiConfig.IndexName)

				startTime := time.Now()
				result, err := d.executeGSIQuery(ctx, tableName, job.gsiConfig, job.condition)
				queryTime := time.Since(startTime)

				// Update metrics in result
				if result != nil {
					result.Metrics.QueryTime = queryTime
					result.Metrics.Success = (err == nil)
					if err != nil {
						result.Metrics.ErrorMessage = err.Error()
					}
				}

				resultChan <- gsiQueryResponse{
					result: result,
					err:    err,
					index:  job.index,
				}

				log.Printf("Worker %d completed GSI query for field '%s' in %v (success: %t)",
					workerID, job.condition.Operand, queryTime, err == nil)
			}
		}(w)
	}

	// Send jobs to workers
	for _, job := range jobs {
		jobChan <- job
	}
	close(jobChan)

	// Collect results
	results := make([]GSIQueryResult, len(jobs))
	var firstError error

	for i := 0; i < len(jobs); i++ {
		response := <-resultChan
		if response.err != nil {
			if firstError == nil {
				firstError = response.err
			}
			log.Printf("GSI query failed for job index %d: %v", response.index, response.err)
			continue
		}
		if response.result != nil {
			results[response.index] = *response.result
		}
	}

	if firstError != nil {
		return nil, fmt.Errorf("one or more GSI queries failed: %w", firstError)
	}

	log.Printf("Successfully completed %d concurrent GSI queries", len(results))
	return results, nil
}

// executeGSIQueriesConcurrentlyWithProjection executes multiple GSI queries with projection in parallel
func (d *DynamoDBStorage) executeGSIQueriesConcurrentlyWithProjection(ctx context.Context, tableName, tenant string, conditions []SearchCondition, projectedFields []string) ([]GSIQueryResult, error) {
	type gsiQueryJob struct {
		condition SearchCondition
		gsiConfig GSIConfig
		index     int
	}

	type gsiQueryResponse struct {
		result *GSIQueryResult
		err    error
		index  int
	}

	// Prepare jobs for each GSI query
	var jobs []gsiQueryJob
	for i, condition := range conditions {
		gsiConfig, exists := d.getGSIForField(tenant, condition.Operand)
		if !exists {
			return nil, fmt.Errorf("no GSI configuration found for field: %s in tenant: %s", condition.Operand, tenant)
		}
		jobs = append(jobs, gsiQueryJob{
			condition: condition,
			gsiConfig: *gsiConfig,
			index:     i,
		})
	}

	// Create channels for job distribution and result collection
	jobChan := make(chan gsiQueryJob, len(jobs))
	resultChan := make(chan gsiQueryResponse, len(jobs))

	// Start worker goroutines (limit to reasonable number)
	numWorkers := len(jobs)
	if numWorkers > 5 { // Limit concurrent queries to avoid overwhelming DynamoDB
		numWorkers = 5
	}

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			for job := range jobChan {
				queryStart := time.Now()
				result, err := d.executeGSIQueryWithProjection(ctx, tableName, job.gsiConfig, job.condition, projectedFields)
				queryTime := time.Since(queryStart)

				resultChan <- gsiQueryResponse{
					result: result,
					err:    err,
					index:  job.index,
				}

				if err != nil {
					log.Printf("Worker %d failed GSI query with projection for field '%s' in %v: %v",
						workerID, job.condition.Operand, queryTime, err)
				} else {
					log.Printf("Worker %d completed GSI query with projection for field '%s' in %v (success: %t)",
						workerID, job.condition.Operand, queryTime, err == nil)
				}
			}
		}(w)
	}

	// Send jobs to workers
	for _, job := range jobs {
		jobChan <- job
	}
	close(jobChan)

	// Collect results
	results := make([]GSIQueryResult, len(jobs))
	var firstError error

	for i := 0; i < len(jobs); i++ {
		response := <-resultChan
		if response.err != nil {
			if firstError == nil {
				firstError = response.err
			}
			log.Printf("GSI query with projection failed for job index %d: %v", response.index, response.err)
			continue
		}
		if response.result != nil {
			results[response.index] = *response.result
		}
	}

	if firstError != nil {
		return nil, fmt.Errorf("one or more GSI queries with projection failed: %w", firstError)
	}

	log.Printf("Successfully completed %d concurrent GSI queries with projection", len(results))
	return results, nil
}

// logGSIQueryMetrics logs performance metrics for GSI queries
func (d *DynamoDBStorage) logGSIQueryMetrics(results []GSIQueryResult) {
	log.Printf("=== GSI Query Performance Metrics ===")
	totalTime := time.Duration(0)
	totalResults := 0

	for i, result := range results {
		log.Printf("Query %d: index=%s, field=%s, time=%v, results=%d, success=%t",
			i+1, result.Metrics.IndexName, result.Metrics.ConditionUsed.Operand,
			result.Metrics.QueryTime, result.Metrics.ResultCount, result.Metrics.Success)

		if result.Metrics.Success {
			totalTime += result.Metrics.QueryTime
			totalResults += result.Metrics.ResultCount
		}

		if !result.Metrics.Success {
			log.Printf("Query %d error: %s", i+1, result.Metrics.ErrorMessage)
		}
	}

	if len(results) > 0 {
		avgTime := totalTime / time.Duration(len(results))
		log.Printf("Average query time: %v, Total results: %d", avgTime, totalResults)
	}
	log.Printf("=== End GSI Query Metrics ===")
}

// fetchExistingIndexes fetches the actual GSI indexes from DynamoDB for a tenant table
func (d *DynamoDBStorage) fetchExistingIndexes(ctx context.Context, tenantID string) error {
	if !d.enableGSI {
		log.Printf("GSI disabled, skipping index fetch for tenant: %s", tenantID)
		return nil
	}

	tableName := d.getTenantTableName(tenantID)

	// Describe the table to get GSI information
	describeOutput, err := d.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(tableName),
	})
	if err != nil {
		log.Printf("Failed to describe table %s: %v", tableName, err)
		return err
	}

	d.gsiMutex.Lock()
	defer d.gsiMutex.Unlock()

	// Initialize tenant GSI config if not exists
	if d.gsiConfig[tenantID] == nil {
		d.gsiConfig[tenantID] = initializeDefaultGSIConfig()
	}

	// Process existing GSIs from DynamoDB
	for _, gsi := range describeOutput.Table.GlobalSecondaryIndexes {
		indexName := aws.ToString(gsi.IndexName)

		// Find the field name that maps to this index
		var fieldName string
		for field, config := range d.gsiConfig[tenantID] {
			if config.IndexName == indexName {
				fieldName = field
				break
			}
		}

		if fieldName != "" {
			// Update existing config with actual DynamoDB data
			config := d.gsiConfig[tenantID][fieldName]
			config.Status = string(gsi.IndexStatus)
			config.ExistsInDynamoDB = true

			// Update projection information
			if gsi.Projection != nil {
				config.ProjectionType = string(gsi.Projection.ProjectionType)
				if gsi.Projection.NonKeyAttributes != nil {
					config.ProjectedAttributes = gsi.Projection.NonKeyAttributes
				}
			}

			d.gsiConfig[tenantID][fieldName] = config
			log.Printf("Found existing GSI for tenant %s, field %s: index=%s, status=%s",
				tenantID, fieldName, indexName, config.Status)
		} else {
			log.Printf("Found unknown GSI in table %s: %s", tableName, indexName)
		}
	}

	log.Printf("Fetched %d existing GSI indexes for tenant %s", len(describeOutput.Table.GlobalSecondaryIndexes), tenantID)
	return nil
}

// Note: createDefaultIndexes function removed - all indexes are now created upfront with the table

// isIndexAlreadyExistsError checks if the error indicates the index already exists
func (d *DynamoDBStorage) isIndexAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "already exists") ||
		strings.Contains(errStr, "ResourceInUseException") ||
		strings.Contains(errStr, "ValidationException")
}

// RefreshIndexCache refreshes the local GSI cache for a specific tenant
func (d *DynamoDBStorage) RefreshIndexCache(ctx context.Context, tenantID string) error {
	log.Printf("Refreshing index cache for tenant: %s", tenantID)
	return d.fetchExistingIndexes(ctx, tenantID)
}

// GetIndexForField returns the GSI configuration for a given field name and tenant
func (d *DynamoDBStorage) GetIndexForField(tenantID, fieldName string) (*GSIConfig, bool) {
	return d.getGSIForField(tenantID, fieldName)
}

// CreateGSI creates a new Global Secondary Index for a tenant table
func (d *DynamoDBStorage) CreateGSI(ctx context.Context, tenantID, fieldName, indexName, partitionKey, rangeKey string, projectionType string, projectedAttributes []string) error {
	if !d.enableGSI {
		return fmt.Errorf("GSI is disabled, cannot create index")
	}

	tableName := d.getTenantTableName(tenantID)

	// Validate projection type
	validProjectionTypes := map[string]bool{
		"ALL":       true,
		"KEYS_ONLY": true,
		"INCLUDE":   true,
	}
	if !validProjectionTypes[projectionType] {
		return fmt.Errorf("invalid projection type: %s. Must be ALL, KEYS_ONLY, or INCLUDE", projectionType)
	}

	// For INCLUDE projection, projected attributes are required
	if projectionType == "INCLUDE" && len(projectedAttributes) == 0 {
		return fmt.Errorf("projected attributes are required for INCLUDE projection type")
	}

	log.Printf("Creating GSI for tenant %s: index=%s, field=%s, partitionKey=%s, rangeKey=%s, projectionType=%s",
		tenantID, indexName, fieldName, partitionKey, rangeKey, projectionType)

	// Check if index already exists
	d.gsiMutex.RLock()
	if tenantGSI, exists := d.gsiConfig[tenantID]; exists {
		if config, fieldExists := tenantGSI[fieldName]; fieldExists && config.ExistsInDynamoDB {
			d.gsiMutex.RUnlock()
			return fmt.Errorf("GSI for field %s already exists in tenant %s", fieldName, tenantID)
		}
	}
	d.gsiMutex.RUnlock()

	// Prepare the GSI creation request
	projection := &types.Projection{
		ProjectionType: types.ProjectionType(projectionType),
	}
	if projectionType == "INCLUDE" {
		projection.NonKeyAttributes = projectedAttributes
	}

	gsiUpdate := types.GlobalSecondaryIndexUpdate{
		Create: &types.CreateGlobalSecondaryIndexAction{
			IndexName: aws.String(indexName),
			KeySchema: []types.KeySchemaElement{
				{
					AttributeName: aws.String(partitionKey),
					KeyType:       types.KeyTypeHash,
				},
			},
			Projection: projection,
			ProvisionedThroughput: &types.ProvisionedThroughput{
				ReadCapacityUnits:  aws.Int64(5),
				WriteCapacityUnits: aws.Int64(5),
			},
		},
	}

	// Add range key if provided
	if rangeKey != "" {
		gsiUpdate.Create.KeySchema = append(gsiUpdate.Create.KeySchema, types.KeySchemaElement{
			AttributeName: aws.String(rangeKey),
			KeyType:       types.KeyTypeRange,
		})
	}

	// Prepare attribute definitions for the new index
	attributeDefinitions := []types.AttributeDefinition{
		{
			AttributeName: aws.String(partitionKey),
			AttributeType: types.ScalarAttributeTypeS, // Assuming string type, adjust as needed
		},
	}
	if rangeKey != "" && rangeKey != partitionKey {
		attributeDefinitions = append(attributeDefinitions, types.AttributeDefinition{
			AttributeName: aws.String(rangeKey),
			AttributeType: types.ScalarAttributeTypeS,
		})
	}

	// Create the index
	updateInput := &dynamodb.UpdateTableInput{
		TableName:                   aws.String(tableName),
		AttributeDefinitions:        attributeDefinitions,
		GlobalSecondaryIndexUpdates: []types.GlobalSecondaryIndexUpdate{gsiUpdate},
	}

	_, err := d.client.UpdateTable(ctx, updateInput)
	if err != nil {
		return fmt.Errorf("failed to create GSI %s for tenant %s: %w", indexName, tenantID, err)
	}

	// Wait for the index to become active
	log.Printf("Waiting for GSI %s to become active for tenant %s...", indexName, tenantID)

	return nil
}

// updateLocalGSIConfig updates the local GSI configuration cache
func (d *DynamoDBStorage) updateLocalGSIConfig(tenantID, fieldName, indexName, partitionKey, rangeKey, projectionType string, projectedAttributes []string, status string) {
	d.gsiMutex.Lock()
	defer d.gsiMutex.Unlock()

	// Initialize tenant GSI config if not exists
	if d.gsiConfig[tenantID] == nil {
		d.gsiConfig[tenantID] = make(map[string]GSIConfig)
	}

	// Create new GSI config
	config := GSIConfig{
		IndexName:           indexName,
		HashKey:             partitionKey,
		RangeKey:            rangeKey,
		Priority:            10,  // Default priority for dynamically created indexes
		EstimatedCost:       2.0, // Default cost estimate
		SupportedOps:        []string{"eq", "=", "ne", "!=", "lt", "<", "lte", "<=", "gt", ">", "gte", ">="},
		Status:              status,
		ProjectionType:      projectionType,
		ProjectedAttributes: projectedAttributes,
		CreatedAt:           time.Now(),
		ExistsInDynamoDB:    status == "ACTIVE",
	}

	d.gsiConfig[tenantID][fieldName] = config
	log.Printf("Updated local GSI config for tenant %s, field %s: index=%s, status=%s",
		tenantID, fieldName, indexName, status)
}

// Note: initializeTenantGSI function removed - all indexes are now created upfront with the table

// Close gracefully closes the DynamoDB connection and clears tenant table cache
func (d *DynamoDBStorage) Close() error {
	// Clear the tenant tables map
	d.tenantTables.Range(func(key, value interface{}) bool {
		d.tenantTables.Delete(key)
		return true
	})

	// AWS SDK v2 doesn't require explicit connection closing
	// The HTTP client will be cleaned up by the garbage collector
	log.Println("DynamoDB tenant-aware storage closed")
	return nil
}
