package storage

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
	"google.golang.org/protobuf/proto"
)

// DynamoDBStorage implements tenant-aware dynamic ticket storage using AWS DynamoDB with protobuf
// Each tenant gets its own DynamoDB table for complete data isolation
type DynamoDBStorage struct {
	client        *dynamodb.Client
	baseTableName string
	tenantTables  sync.Map     // tenant -> tableName mapping for performance
	tableMutex    sync.RWMutex // synchronizes table creation operations
}

// NewDynamoDBStorage creates a new tenant-aware DynamoDB storage instance with connection pooling
// Each tenant will get its own DynamoDB table for complete data isolation
func NewDynamoDBStorage(ctx context.Context, baseTableName, region, dbURL, dbAddress string) (*DynamoDBStorage, error) {
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

	// Add custom endpoint resolver if URL/address is provided
	if endpointURL != "" {
		configOptions = append(configOptions, config.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				if service == dynamodb.ServiceID {
					return aws.Endpoint{
						URL:           endpointURL,
						SigningRegion: region,
					}, nil
				}
				return aws.Endpoint{}, fmt.Errorf("unknown endpoint requested")
			}),
		))
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

	storage := &DynamoDBStorage{
		client:        client,
		baseTableName: baseTableName,
		tenantTables:  sync.Map{},
	}

	if endpointURL != "" {
		log.Printf("Successfully initialized tenant-aware DynamoDB storage with base table: %s in region: %s at endpoint: %s", baseTableName, region, endpointURL)
	} else {
		log.Printf("Successfully initialized tenant-aware DynamoDB storage with base table: %s in region: %s", baseTableName, region)
	}
	return storage, nil
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
	return nil
}

// createTableIfNotExists creates a new DynamoDB table with the standard schema
func (d *DynamoDBStorage) createTableIfNotExists(ctx context.Context, tableName string) error {
	// Create table with timeout
	createCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	input := &dynamodb.CreateTableInput{
		TableName: aws.String(tableName),
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("pk"),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},
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

	// Wait for table to become active
	waiter := dynamodb.NewTableExistsWaiter(d.client)
	waitCtx, waitCancel := context.WithTimeout(ctx, 120*time.Second)
	defer waitCancel()

	err = waiter.Wait(waitCtx, &dynamodb.DescribeTableInput{
		TableName: aws.String(tableName),
	}, 120*time.Second)

	if err != nil {
		return fmt.Errorf("table %s creation timed out: %w", tableName, err)
	}

	return nil
}

// convertToFieldValue converts a Go value to protobuf FieldValue
func (d *DynamoDBStorage) convertToFieldValue(value interface{}) (*ticketpb.FieldValue, error) {
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

// convertFromFieldValue converts protobuf FieldValue back to Go value
func (d *DynamoDBStorage) convertFromFieldValue(fieldValue *ticketpb.FieldValue) (interface{}, error) {
	switch v := fieldValue.Value.(type) {
	case *ticketpb.FieldValue_StringValue:
		return v.StringValue, nil
	case *ticketpb.FieldValue_IntValue:
		return v.IntValue, nil
	case *ticketpb.FieldValue_DoubleValue:
		return v.DoubleValue, nil
	case *ticketpb.FieldValue_BoolValue:
		return v.BoolValue, nil
	case *ticketpb.FieldValue_BytesValue:
		return v.BytesValue, nil
	case *ticketpb.FieldValue_StringArray:
		return v.StringArray.Values, nil
	default:
		return nil, fmt.Errorf("unknown field value type")
	}
}

// CreateTicket stores a new ticket in the tenant-specific DynamoDB table using protobuf serialization
func (d *DynamoDBStorage) CreateTicket(tenant string, ticketData *ticketpb.TicketData) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Ensure tenant table exists
	if err := d.ensureTenantTable(ctx, tenant); err != nil {
		return fmt.Errorf("failed to ensure tenant table: %w", err)
	}

	// Serialize protobuf to bytes
	data, err := proto.Marshal(ticketData)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	// Store in tenant-specific DynamoDB table with ticket ID as primary key
	_, err = d.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item: map[string]types.AttributeValue{
			"pk":   &types.AttributeValueMemberS{Value: ticketData.Id},
			"data": &types.AttributeValueMemberB{Value: data},
		},
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})

	if err != nil {
		return fmt.Errorf("failed to create ticket in tenant table %s: %w", tableName, err)
	}

	log.Printf("Created ticket %s for tenant %s in table %s", ticketData.Id, tenant, tableName)
	return nil
}

// GetTicket retrieves a ticket by tenant and ID from the tenant-specific DynamoDB table
func (d *DynamoDBStorage) GetTicket(tenant, id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Ensure tenant table exists
	if err := d.ensureTenantTable(ctx, tenant); err != nil {
		log.Printf("ERROR: Failed to ensure tenant table for %s: %v", tenant, err)
		return nil, false
	}

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	result, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{
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

	// Extract protobuf data
	dataAttr, ok := result.Item["data"]
	if !ok {
		log.Printf("ERROR: No data field found in DynamoDB item")
		return nil, false
	}

	dataBytes, ok := dataAttr.(*types.AttributeValueMemberB)
	if !ok {
		log.Printf("ERROR: Data field is not binary type")
		return nil, false
	}

	// Deserialize protobuf
	var pbTicket ticketpb.TicketData
	if err := proto.Unmarshal(dataBytes.Value, &pbTicket); err != nil {
		log.Printf("ERROR: Failed to unmarshal protobuf: %v", err)
		return nil, false
	}

	return &pbTicket, true
}

// UpdateTicket updates an existing ticket in the tenant-specific DynamoDB table
func (d *DynamoDBStorage) UpdateTicket(tenant string, ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Ensure tenant table exists
	if err := d.ensureTenantTable(ctx, tenant); err != nil {
		log.Printf("ERROR: Failed to ensure tenant table for %s: %v", tenant, err)
		return false
	}

	// Serialize protobuf to bytes
	data, err := proto.Marshal(ticketData)
	if err != nil {
		log.Printf("ERROR: Failed to marshal protobuf: %v", err)
		return false
	}

	// Get tenant-specific table name
	tableName := d.getTenantTableName(tenant)

	// Update with condition that item exists
	_, err = d.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: ticketData.Id},
		},
		UpdateExpression: aws.String("SET #data = :data"),
		ExpressionAttributeNames: map[string]string{
			"#data": "data",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":data": &types.AttributeValueMemberB{Value: data},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})

	if err != nil {
		log.Printf("ERROR: Failed to update ticket in tenant table %s: %v", tableName, err)
		return false
	}

	log.Printf("Updated ticket %s for tenant %s in table %s", ticketData.Id, tenant, tableName)
	return true
}

// DeleteTicket removes a ticket from the tenant-specific DynamoDB table and returns it
func (d *DynamoDBStorage) DeleteTicket(tenant, id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	// Convert DynamoDB items to tickets
	for _, item := range result.Items {
		// Extract protobuf data
		dataAttr, ok := item["data"]
		if !ok {
			log.Printf("ERROR: No data field found in DynamoDB item")
			continue
		}

		dataBytes, ok := dataAttr.(*types.AttributeValueMemberB)
		if !ok {
			log.Printf("ERROR: Data field is not binary type")
			continue
		}

		// Deserialize protobuf
		var pbTicket ticketpb.TicketData
		if err := proto.Unmarshal(dataBytes.Value, &pbTicket); err != nil {
			log.Printf("ERROR: Failed to unmarshal protobuf: %v", err)
			continue
		}

		tickets = append(tickets, &pbTicket)
	}

	log.Printf("Listed %d tickets for tenant %s from table %s", len(tickets), tenant, tableName)
	return tickets, nil
}

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
