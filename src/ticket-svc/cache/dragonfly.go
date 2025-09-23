package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/platform/ticket-svc/logger"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
	"github.com/redis/go-redis/v9"
)

// DragonflyCache implements caching using DragonFly DB
type DragonflyCache struct {
	client *redis.Client
	logger logger.Logger
	// Cache metrics
	totalRequests int64
	cacheHits     int64
}

// NewDragonflyCache creates a new DragonFly cache instance
func NewDragonflyCache(addr, password string, db int) (*DragonflyCache, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		PoolSize:     20,
		MinIdleConns: 5,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to DragonFly: %w", err)
	}

	log.Printf("DragonFly cache connected successfully to %s", addr)

	return &DragonflyCache{
		client: rdb,
		logger: logger.NewLogger("dragonfly-cache", "ticket-svc"),
	}, nil
}

// Close closes the DragonFly connection
func (c *DragonflyCache) Close() error {
	return c.client.Close()
}

// GetTicket retrieves a ticket from cache
func (c *DragonflyCache) GetTicket(ctx context.Context, ticketID string) (*ticketpb.TicketData, bool) {
	key := fmt.Sprintf("ticket:%s", ticketID)
	atomic.AddInt64(&c.totalRequests, 1)

	val, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		c.logger.LogCacheMiss(key, "GET_TICKET")
		return nil, false
	} else if err != nil {
		log.Printf("Cache get error for ticket %s: %v", ticketID, err)
		c.logger.LogCacheMiss(key, "GET_TICKET")
		return nil, false
	}

	// Unmarshal into JSON-compatible format first
	var jsonTicket map[string]interface{}
	if err := json.Unmarshal([]byte(val), &jsonTicket); err != nil {
		log.Printf("Cache unmarshal error for ticket %s: %v", ticketID, err)
		c.logger.LogCacheMiss(key, "GET_TICKET")
		return nil, false
	}

	// Convert JSON format back to protobuf
	ticket := convertJSONToProtobuf(jsonTicket)
	if ticket == nil {
		log.Printf("Failed to convert cached JSON to protobuf for ticket %s", ticketID)
		c.logger.LogCacheMiss(key, "GET_TICKET")
		return nil, false
	}

	atomic.AddInt64(&c.cacheHits, 1)
	c.logger.LogCacheHit(key, "GET_TICKET")
	return ticket, true
}

// SetTicket stores a ticket in cache
func (c *DragonflyCache) SetTicket(ctx context.Context, ticket *ticketpb.TicketData, ttl time.Duration) error {
	key := fmt.Sprintf("ticket:%s", ticket.Id)

	// Convert protobuf to JSON-compatible format
	jsonTicket := convertProtobufToJSON(ticket)
	data, err := json.Marshal(jsonTicket)
	if err != nil {
		return fmt.Errorf("failed to marshal ticket: %w", err)
	}

	err = c.client.Set(ctx, key, data, ttl).Err()
	if err != nil {
		return fmt.Errorf("failed to set cache: %w", err)
	}

	return nil
}

// DeleteTicket removes a ticket from cache
func (c *DragonflyCache) DeleteTicket(ctx context.Context, ticketID string) error {
	key := fmt.Sprintf("ticket:%s", ticketID)
	return c.client.Del(ctx, key).Err()
}

// GetSearchResults retrieves search results from cache
func (c *DragonflyCache) GetSearchResults(ctx context.Context, searchKey string) ([]*ticketpb.TicketData, bool) {
	key := fmt.Sprintf("search:%s", searchKey)
	atomic.AddInt64(&c.totalRequests, 1)

	val, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		c.logger.LogCacheMiss(key, "SEARCH_RESULTS")
		return nil, false
	} else if err != nil {
		log.Printf("Cache get error for search %s: %v", searchKey, err)
		c.logger.LogCacheMiss(key, "SEARCH_RESULTS")
		return nil, false
	}

	// Unmarshal into JSON-compatible format first
	var jsonTickets []map[string]interface{}
	if err := json.Unmarshal([]byte(val), &jsonTickets); err != nil {
		log.Printf("Cache unmarshal error for search %s: %v", searchKey, err)
		c.logger.LogCacheMiss(key, "SEARCH_RESULTS")
		return nil, false
	}

	// Convert JSON format back to protobuf
	var tickets []*ticketpb.TicketData
	for _, jsonTicket := range jsonTickets {
		ticket := convertJSONToProtobuf(jsonTicket)
		if ticket != nil {
			tickets = append(tickets, ticket)
		}
	}

	atomic.AddInt64(&c.cacheHits, 1)
	c.logger.LogCacheHit(key, "SEARCH_RESULTS")
	return tickets, true
}

// SetSearchResults stores search results in cache
func (c *DragonflyCache) SetSearchResults(ctx context.Context, searchKey string, tickets []*ticketpb.TicketData, ttl time.Duration) error {
	key := fmt.Sprintf("search:%s", searchKey)

	// Convert protobuf to JSON-compatible format
	var jsonTickets []map[string]interface{}
	for _, ticket := range tickets {
		jsonTicket := convertProtobufToJSON(ticket)
		jsonTickets = append(jsonTickets, jsonTicket)
	}

	data, err := json.Marshal(jsonTickets)
	if err != nil {
		return fmt.Errorf("failed to marshal search results: %w", err)
	}

	err = c.client.Set(ctx, key, data, ttl).Err()
	if err != nil {
		return fmt.Errorf("failed to set search cache: %w", err)
	}

	return nil
}

// GetTicketList retrieves ticket list from cache
func (c *DragonflyCache) GetTicketList(ctx context.Context) ([]*ticketpb.TicketData, bool) {
	key := "tickets:list"

	val, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, false
	} else if err != nil {
		log.Printf("Cache get error for ticket list: %v", err)
		return nil, false
	}

	// Unmarshal into JSON-compatible format first
	var jsonTickets []map[string]interface{}
	if err := json.Unmarshal([]byte(val), &jsonTickets); err != nil {
		log.Printf("Cache unmarshal error for ticket list: %v", err)
		return nil, false
	}

	// Convert JSON format back to protobuf
	var tickets []*ticketpb.TicketData
	for _, jsonTicket := range jsonTickets {
		ticket := convertJSONToProtobuf(jsonTicket)
		if ticket != nil {
			tickets = append(tickets, ticket)
		}
	}

	return tickets, true
}

// SetTicketList stores ticket list in cache
func (c *DragonflyCache) SetTicketList(ctx context.Context, tickets []*ticketpb.TicketData, ttl time.Duration) error {
	key := "tickets:list"

	// Convert protobuf to JSON-compatible format
	var jsonTickets []map[string]interface{}
	for _, ticket := range tickets {
		jsonTicket := convertProtobufToJSON(ticket)
		jsonTickets = append(jsonTickets, jsonTicket)
	}

	data, err := json.Marshal(jsonTickets)
	if err != nil {
		return fmt.Errorf("failed to marshal ticket list: %w", err)
	}

	err = c.client.Set(ctx, key, data, ttl).Err()
	if err != nil {
		return fmt.Errorf("failed to set ticket list cache: %w", err)
	}

	return nil
}

// InvalidateTicketList removes ticket list from cache
func (c *DragonflyCache) InvalidateTicketList(ctx context.Context) error {
	return c.client.Del(ctx, "tickets:list").Err()
}

// InvalidateSearchPatterns removes search results matching a pattern
func (c *DragonflyCache) InvalidateSearchPatterns(ctx context.Context, pattern string) error {
	// Use SCAN to find all search keys and delete them
	searchPattern := fmt.Sprintf("search:%s*", pattern)

	keys, err := c.client.Keys(ctx, searchPattern).Result()
	if err != nil {
		return err
	}

	if len(keys) > 0 {
		return c.client.Del(ctx, keys...).Err()
	}

	return nil
}

// GenerateSearchKey creates a consistent cache key for search parameters
func (c *DragonflyCache) GenerateSearchKey(conditions interface{}, projectedFields []string, sortFields interface{}) string {
	// Create a simple hash of the search parameters
	searchData := map[string]interface{}{
		"conditions":       conditions,
		"projected_fields": projectedFields,
		"sort_fields":      sortFields,
	}

	data, err := json.Marshal(searchData)
	if err != nil {
		return fmt.Sprintf("unknown_%d", time.Now().UnixNano())
	}

	return fmt.Sprintf("%x", data)[:16] // Use first 16 chars of hash
}

// GetStats returns cache statistics
func (c *DragonflyCache) GetStats(ctx context.Context) (map[string]interface{}, error) {
	info := c.client.Info(ctx, "memory", "stats")
	result, err := info.Result()
	if err != nil {
		return nil, err
	}

	// Parse some basic stats from the INFO output
	stats := map[string]interface{}{
		"info": result,
	}

	return stats, nil
}

// convertProtobufToJSON converts a TicketData protobuf to JSON-compatible format
func convertProtobufToJSON(ticket *ticketpb.TicketData) map[string]interface{} {
	result := map[string]interface{}{
		"id":         ticket.Id,
		"created_at": ticket.CreatedAt,
		"updated_at": ticket.UpdatedAt,
	}

	// Convert protobuf fields to simple JSON values
	for fieldName, fieldValue := range ticket.Fields {
		result[fieldName] = convertFieldValueToInterface(fieldValue)
	}

	return result
}

// convertJSONToProtobuf converts a JSON map back to TicketData protobuf
func convertJSONToProtobuf(jsonTicket map[string]interface{}) *ticketpb.TicketData {
	ticket := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Extract core fields
	if id, ok := jsonTicket["id"].(string); ok {
		ticket.Id = id
	}
	if createdAt, ok := jsonTicket["created_at"].(string); ok {
		ticket.CreatedAt = createdAt
	}
	if updatedAt, ok := jsonTicket["updated_at"].(string); ok {
		ticket.UpdatedAt = updatedAt
	}

	// Convert all other fields to protobuf FieldValue
	for key, value := range jsonTicket {
		// Skip core fields
		if key == "id" || key == "created_at" || key == "updated_at" {
			continue
		}

		fieldValue := convertInterfaceToFieldValue(value)
		if fieldValue != nil {
			ticket.Fields[key] = fieldValue
		}
	}

	return ticket
}

// convertFieldValueToInterface converts a protobuf FieldValue back to Go interface{}
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

// convertInterfaceToFieldValue converts a Go interface{} value to protobuf FieldValue
func convertInterfaceToFieldValue(value interface{}) *ticketpb.FieldValue {
	switch v := value.(type) {
	case string:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: v},
		}
	case int:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
		}
	case int32:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
		}
	case int64:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_IntValue{IntValue: v},
		}
	case float32:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: float64(v)},
		}
	case float64:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: v},
		}
	case bool:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_BoolValue{BoolValue: v},
		}
	case []string:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringArray{
				StringArray: &ticketpb.StringArray{Values: v},
			},
		}
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
		}
	default:
		// Convert unknown types to string
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: fmt.Sprintf("%v", v)},
		}
	}
}

// GetCacheStats returns cache statistics and logs them
func (c *DragonflyCache) GetCacheStats() (totalRequests, cacheHits int64, hitRatio float64) {
	total := atomic.LoadInt64(&c.totalRequests)
	hits := atomic.LoadInt64(&c.cacheHits)

	var ratio float64
	if total > 0 {
		ratio = float64(hits) / float64(total)
	}

	c.logger.LogCacheStats("OVERALL", ratio, total, hits)

	return total, hits, ratio
}

// ResetCacheStats resets cache statistics
func (c *DragonflyCache) ResetCacheStats() {
	atomic.StoreInt64(&c.totalRequests, 0)
	atomic.StoreInt64(&c.cacheHits, 0)
}
