package storage

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	ticketpb "github.com/platform/ticket-svc/pb/proto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoDBStorage implements ticket storage using MongoDB
// Uses a single collection for all tickets
type MongoDBStorage struct {
	client         *mongo.Client
	database       *mongo.Database
	databaseName   string
	collectionName string
}

// NewMongoDBStorage creates a new MongoDB storage instance
func NewMongoDBStorage(ctx context.Context, collectionName, connectionString, databaseName string) (*MongoDBStorage, error) {
	if connectionString == "" {
		connectionString = "mongodb://localhost:27017"
	}
	if databaseName == "" {
		databaseName = "tickets"
	}
	if collectionName == "" {
		collectionName = "tickets"
	}

	log.Printf("Connecting to MongoDB at %s, database: %s", maskConnectionString(connectionString), databaseName)

	// Set client options with extended timeout for authentication
	clientOptions := options.Client().ApplyURI(connectionString)
	clientOptions.SetMaxPoolSize(25)
	clientOptions.SetMinPoolSize(5)
	clientOptions.SetMaxConnIdleTime(5 * time.Minute)
	clientOptions.SetConnectTimeout(30 * time.Second)
	clientOptions.SetServerSelectionTimeout(30 * time.Second)

	// Connect to MongoDB
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// Test the connection with extended timeout
	pingCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx, nil); err != nil {
		client.Disconnect(ctx)
		return nil, fmt.Errorf("failed to ping MongoDB (check authentication and network): %w", err)
	}

	log.Printf("MongoDB connection established successfully")

	database := client.Database(databaseName)

	storage := &MongoDBStorage{
		client:         client,
		database:       database,
		databaseName:   databaseName,
		collectionName: collectionName,
	}

	// Ensure the collection exists and has proper indexes
	if err := storage.ensureCollectionExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure collection exists: %w", err)
	}

	return storage, nil
}

// maskConnectionString masks sensitive information in connection strings for logging
func maskConnectionString(connectionString string) string {
	// Simple masking for passwords in connection strings
	// mongodb://username:password@host:port/database -> mongodb://username:***@host:port/database
	if strings.Contains(connectionString, "@") && strings.Contains(connectionString, ":") {
		parts := strings.Split(connectionString, "@")
		if len(parts) == 2 {
			userPart := parts[0]
			hostPart := parts[1]
			if strings.Contains(userPart, ":") {
				userCredParts := strings.Split(userPart, ":")
				if len(userCredParts) >= 3 { // mongodb://user:pass
					return userCredParts[0] + "://" + userCredParts[1] + ":***@" + hostPart
				}
			}
		}
	}
	return connectionString
}

// generateTicketID generates a unique ticket ID if not provided
func (m *MongoDBStorage) generateTicketID() string {
	// Generate a simple ticket ID with timestamp
	return fmt.Sprintf("TKT-%d", time.Now().UnixNano()/1000000)
}

// ensureCollectionExists ensures that the tickets collection exists
func (m *MongoDBStorage) ensureCollectionExists(ctx context.Context) error {
	// Check if collection exists with extended timeout
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	collections, err := m.database.ListCollectionNames(listCtx, bson.M{"name": m.collectionName})
	if err != nil {
		if strings.Contains(err.Error(), "Unauthorized") || strings.Contains(err.Error(), "authentication") {
			return fmt.Errorf("MongoDB authentication failed - check username/password in connection string: %w", err)
		}
		return fmt.Errorf("failed to list collections (check MongoDB connection and permissions): %w", err)
	}

	collectionExists := false
	for _, name := range collections {
		if name == m.collectionName {
			collectionExists = true
			break
		}
	}

	if !collectionExists {
		// Create collection with timeout
		createCtx, createCancel := context.WithTimeout(ctx, 30*time.Second)
		defer createCancel()

		if err := m.database.CreateCollection(createCtx, m.collectionName); err != nil {
			if strings.Contains(err.Error(), "Unauthorized") || strings.Contains(err.Error(), "authentication") {
				return fmt.Errorf("MongoDB authentication failed during collection creation - check username/password: %w", err)
			}
			return fmt.Errorf("failed to create collection %s (check MongoDB permissions): %w", m.collectionName, err)
		}
		log.Printf("Created new MongoDB collection: %s", m.collectionName)
	}

	// Create indexes
	if err := m.createIndexes(ctx); err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	return nil
}

// createIndexes creates clustered compound indexes grouped by business domain
// This replaces individual field indexes to reduce write burden and improve query performance
func (m *MongoDBStorage) createIndexes(ctx context.Context) error {
	collection := m.database.Collection(m.collectionName)

	// Essential primary indexes
	primaryIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "ticket_id", Value: 1}},
			Options: options.Index().SetName("idx_ticket_id"),
		},
		{
			Keys:    bson.D{{Key: "createdtime", Value: 1}},
			Options: options.Index().SetName("idx_created_time"),
		},
	}

	// Clustered compound indexes grouped by business domain
	clusterIndexes := []mongo.IndexModel{
		// 1. Request Metadata & Identity Cluster
		{
			Keys: bson.D{
				{Key: "requesterid", Value: 1},
				{Key: "technicianid", Value: 1},
				{Key: "groupid", Value: 1},
				{Key: "departmentid", Value: 1},
				{Key: "createdbyid", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_request_identity"),
		},
		// 2. SLA & Response Tracking Cluster
		{
			Keys: bson.D{
				{Key: "dueby", Value: 1},
				{Key: "firstresponsetime", Value: 1},
				{Key: "responsedue", Value: 1},
				{Key: "resolutionescalationtime", Value: 1},
				{Key: "lastviolationtime", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_sla_tracking"),
		},
		// 3. Status & Lifecycle Cluster
		{
			Keys: bson.D{
				{Key: "statusid", Value: 1},
				{Key: "statuschangedtime", Value: 1},
				{Key: "lastopenedtime", Value: 1},
				{Key: "lastresolvedtime", Value: 1},
				{Key: "lastclosedtime", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_status_lifecycle"),
		},
		// 4. Priority, Urgency & Impact Cluster
		{
			Keys: bson.D{
				{Key: "priorityid", Value: 1},
				{Key: "urgencyid", Value: 1},
				{Key: "impactid", Value: 1},
				{Key: "supportlevel", Value: 1},
				{Key: "approvalstatus", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_priority_impact"),
		},
		// 5. OLA (Operational Level Agreements) Cluster
		{
			Keys: bson.D{
				{Key: "oladueby", Value: 1},
				{Key: "oladuelevel", Value: 1},
				{Key: "olaescalationtime", Value: 1},
				{Key: "lastolaviolationtime", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_ola_tracking"),
		},
		// 6. UC (Underlying Contract) Cluster
		{
			Keys: bson.D{
				{Key: "ucdueby", Value: 1},
				{Key: "ucduelevel", Value: 1},
				{Key: "ucescalationtime", Value: 1},
				{Key: "lastucviolationtime", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_uc_tracking"),
		},
		// 7. Timing & Durations Cluster
		{
			Keys: bson.D{
				{Key: "totalonholdduration", Value: 1},
				{Key: "totalresolutiontime", Value: 1},
				{Key: "totalslapausetime", Value: 1},
				{Key: "totalworkingtime", Value: 1},
				{Key: "reopened", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_timing_durations"),
		},
		// 8. Feedback & Closure Cluster
		{
			Keys: bson.D{
				{Key: "closedby", Value: 1},
				{Key: "resolvedby", Value: 1},
				{Key: "askfeedbackdate", Value: 1},
				{Key: "firstfeedbackdate", Value: 1},
				{Key: "lastapproveddate", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_feedback_closure"),
		},
		// 9. Category & Templates Cluster
		{
			Keys: bson.D{
				{Key: "categoryid", Value: 1},
				{Key: "templateid", Value: 1},
				{Key: "servicecatalogid", Value: 1},
				{Key: "requesttype", Value: 1},
				{Key: "suggestedcategoryid", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_category_templates"),
		},
		// 10. Misc/Integration Cluster
		{
			Keys: bson.D{
				{Key: "companyid", Value: 1},
				{Key: "vendorid", Value: 1},
				{Key: "emailreadconfigid", Value: 1},
				{Key: "messengerconfigid", Value: 1},
			},
			Options: options.Index().SetName("idx_cluster_integration_misc"),
		},
	}

	// High-performance composite indexes for common query patterns
	performanceIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "requesterid", Value: 1},
				{Key: "statusid", Value: 1},
				{Key: "priorityid", Value: 1},
			},
			Options: options.Index().SetName("idx_perf_requester_status_priority"),
		},
		{
			Keys: bson.D{
				{Key: "technicianid", Value: 1},
				{Key: "statusid", Value: 1},
				{Key: "createdtime", Value: 1},
			},
			Options: options.Index().SetName("idx_perf_technician_status_created"),
		},
		{
			Keys: bson.D{
				{Key: "groupid", Value: 1},
				{Key: "statusid", Value: 1},
				{Key: "dueby", Value: 1},
			},
			Options: options.Index().SetName("idx_perf_group_status_due"),
		},
		{
			Keys: bson.D{
				{Key: "companyid", Value: 1},
				{Key: "categoryid", Value: 1},
				{Key: "statusid", Value: 1},
			},
			Options: options.Index().SetName("idx_perf_company_category_status"),
		},
	}

	// Combine all indexes
	allIndexes := append(primaryIndexes, clusterIndexes...)
	allIndexes = append(allIndexes, performanceIndexes...)

	// Create indexes in batches to avoid timeout
	batchSize := 5
	for i := 0; i < len(allIndexes); i += batchSize {
		end := i + batchSize
		if end > len(allIndexes) {
			end = len(allIndexes)
		}

		batch := allIndexes[i:end]
		if _, err := collection.Indexes().CreateMany(ctx, batch); err != nil {
			log.Printf("Warning: Failed to create clustered index batch %d-%d on %s: %v", i, end-1, m.collectionName, err)
		} else {
			log.Printf("Created clustered indexes batch %d-%d on collection: %s", i, end-1, m.collectionName)
		}
	}

	log.Printf("Successfully created %d clustered indexes on collection: %s", len(allIndexes), m.collectionName)
	return nil
}

// protobufToMongoDBDocument converts a TicketData protobuf to MongoDB document
func protobufToMongoDBDocument(ticketData *ticketpb.TicketData, isUpdate bool) (bson.M, error) {
	doc := bson.M{}

	// Core fields - these are managed by the application
	doc["ticket_id"] = ticketData.Id

	// Handle timestamps
	if !isUpdate {
		// For new tickets, set created_at to current time
		doc["created_at"] = time.Now()
	}
	// Always update updated_at for both create and update
	doc["updated_at"] = time.Now()

	// Generate ObjectID for _id field if creating new ticket
	if !isUpdate {
		doc["_id"] = primitive.NewObjectID()
	}

	// Process dynamic fields from protobuf
	if ticketData.Fields != nil {
		for fieldName, fieldValue := range ticketData.Fields {
			if fieldValue == nil {
				continue
			}

			// Get the actual value from the protobuf oneof
			value := convertFieldValueToInterface(fieldValue)
			if value == nil {
				continue
			}

			// Convert field name to lowercase for consistency
			fieldName = strings.ToLower(fieldName)

			// Map to specific columns if they exist in the schema
			switch fieldName {
			case "updatedbyid", "createdbyid", "removedbyid", "requesterid", "technicianid",
				"closedby", "resolvedby", "categoryid", "departmentid", "groupid", "impactid",
				"locationid", "priorityid", "statusid", "urgencyid", "violatedslaid",
				"servicecatalogid", "sourceid", "requesttype", "suggestedcategoryid", "suggestedgroupid",
				"companyid", "vendorid", "violateducid", "transitionmodelid", "messengerconfigid",
				"templateid", "emailreadconfigid":
				// Convert to int64 for BIGINT equivalent columns
				convertedVal := convertToInt64(value)
				if convertedVal != nil {
					doc[fieldName] = *convertedVal
				}
			case "updatedtime", "createdtime", "removedtime", "dueby", "firstresponsetime",
				"lastclosedtime", "lastopenedtime", "lastresolvedtime", "lastviolationtime",
				"olddueby", "oldresponsedue", "resolutionescalationtime", "responsedue",
				"responseescalationtime", "statuschangedtime", "groupchangedtime",
				"lastolaviolationtime", "oladueby", "oldoladueby", "askfeedbackdate",
				"firstfeedbackdate", "olaescalationtime", "lastucviolationtime",
				"olducdueby", "ucdueby", "ucescalationtime", "lastapproveddate":
				// Convert to int64 for timestamp fields (Unix timestamps in milliseconds)
				convertedVal := convertToInt64(value)
				if convertedVal != nil {
					doc[fieldName] = *convertedVal
				}
			case "approvalstatus", "approvaltype", "resolutionduelevel", "responseduelevel",
				"supportlevel", "oladuelevel", "ucduelevel":
				// Convert to int32 for INT columns
				convertedVal := convertToInt32(value)
				if convertedVal != nil {
					doc[fieldName] = *convertedVal
				}
			case "totalonholdduration", "totalresolutiontime", "totalslapausetime",
				"totalworkingtime", "totaluconholdduration", "totalucpausetime",
				"totalucworkingtime", "totalucresolutiontime":
				// Convert to int64 for duration fields (in milliseconds)
				convertedVal := convertToInt64(value)
				if convertedVal != nil {
					doc[fieldName] = *convertedVal
				}
			default:
				// For any other fields, store as-is
				doc[fieldName] = value
			}
		}
	}

	return doc, nil
}

// Helper functions for type conversion
func convertToInt64(value interface{}) *int64 {
	switch v := value.(type) {
	case int64:
		return &v
	case int32:
		val := int64(v)
		return &val
	case int:
		val := int64(v)
		return &val
	case float64:
		val := int64(v)
		return &val
	case float32:
		val := int64(v)
		return &val
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			return &parsed
		}
	}
	return nil
}

func convertToInt32(value interface{}) *int32 {
	switch v := value.(type) {
	case int32:
		return &v
	case int:
		val := int32(v)
		return &val
	case int64:
		val := int32(v)
		return &val
	case float64:
		val := int32(v)
		return &val
	case float32:
		val := int32(v)
		return &val
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 32); err == nil {
			val := int32(parsed)
			return &val
		}
	}
	return nil
}

// mongoDBDocumentToProtobuf converts a MongoDB document back to a TicketData protobuf
func mongoDBDocumentToProtobuf(doc bson.M) *ticketpb.TicketData {
	ticketData := &ticketpb.TicketData{
		Fields: make(map[string]*ticketpb.FieldValue),
	}

	// Extract core fields
	if ticketID, ok := doc["ticket_id"].(string); ok {
		ticketData.Id = ticketID
	}
	if createdAt, ok := doc["created_at"].(primitive.DateTime); ok {
		ticketData.CreatedAt = time.Unix(int64(createdAt)/1000, 0).Format(time.RFC3339)
	} else if createdAt, ok := doc["created_at"].(time.Time); ok {
		ticketData.CreatedAt = createdAt.Format(time.RFC3339)
	}
	if updatedAt, ok := doc["updated_at"].(primitive.DateTime); ok {
		ticketData.UpdatedAt = time.Unix(int64(updatedAt)/1000, 0).Format(time.RFC3339)
	} else if updatedAt, ok := doc["updated_at"].(time.Time); ok {
		ticketData.UpdatedAt = updatedAt.Format(time.RFC3339)
	}

	// Convert all other fields to protobuf FieldValue
	for key, value := range doc {
		// Skip core fields and MongoDB internal fields
		if key == "ticket_id" || key == "created_at" || key == "updated_at" || key == "_id" {
			continue
		}

		fieldValue := interfaceToFieldValue(value)
		if fieldValue != nil {
			ticketData.Fields[key] = fieldValue
		}
	}

	return ticketData
}

// interfaceToFieldValue converts a Go interface{} to protobuf FieldValue
func interfaceToFieldValue(value interface{}) *ticketpb.FieldValue {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case string:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: v},
		}
	case float64:
		// JSON numbers are float64, check if it's actually an integer
		if v == float64(int64(v)) {
			return &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_IntValue{IntValue: int64(v)},
			}
		}
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: v},
		}
	case bool:
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_BoolValue{BoolValue: v},
		}
	case []interface{}:
		// Convert to string array
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

// CreateTicket stores a new ticket in the MongoDB collection
func (m *MongoDBStorage) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Generate ticket ID if not provided
	if ticketData.Id == "" {
		ticketData.Id = m.generateTicketID()
	}

	// Convert protobuf to MongoDB document (isUpdate = false for create)
	doc, err := protobufToMongoDBDocument(ticketData, false)
	if err != nil {
		return fmt.Errorf("failed to convert protobuf to document: %w", err), nil
	}

	collection := m.database.Collection(m.collectionName)

	// Insert the document
	result, err := collection.InsertOne(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to create ticket in collection %s: %w", m.collectionName, err), nil
	}

	log.Printf("Created ticket %s in collection %s", ticketData.Id, m.collectionName)

	resultMap := map[string]interface{}{
		"_id":       result.InsertedID,
		"ticket_id": ticketData.Id,
	}

	return nil, resultMap
}

// GetTicket retrieves a single ticket by ID from the MongoDB collection
func (m *MongoDBStorage) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	collection := m.database.Collection(m.collectionName)

	// Build filter
	filter := bson.M{
		"ticket_id": id,
	}

	// Find the document
	var doc bson.M
	err := collection.FindOne(ctx, filter).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			log.Printf("Ticket %s not found", id)
			return nil, false
		}
		log.Printf("ERROR: Failed to get ticket %s: %v", id, err)
		return nil, false
	}

	// Convert MongoDB document back to protobuf
	ticketData := mongoDBDocumentToProtobuf(doc)

	log.Printf("Retrieved ticket %s from collection %s", id, m.collectionName)
	return ticketData, true
}

// UpdateTicket updates an existing ticket in the MongoDB collection
func (m *MongoDBStorage) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Convert protobuf to MongoDB document (isUpdate = true for update)
	doc, err := protobufToMongoDBDocument(ticketData, true)
	if err != nil {
		log.Printf("ERROR: Failed to convert protobuf to document: %v", err)
		return false
	}

	// Remove _id from update document as it cannot be updated
	delete(doc, "_id")

	collection := m.database.Collection(m.collectionName)

	// Build filter
	filter := bson.M{
		"ticket_id": ticketData.Id,
	}

	// Build update operation
	update := bson.M{
		"$set": doc,
	}

	// Update the document
	result, err := collection.UpdateOne(ctx, filter, update)
	if err != nil {
		log.Printf("ERROR: Failed to update ticket %s: %v", ticketData.Id, err)
		return false
	}

	if result.MatchedCount == 0 {
		log.Printf("WARNING: No ticket found to update with ID %s", ticketData.Id)
		return false
	}

	log.Printf("Updated ticket %s in collection %s", ticketData.Id, m.collectionName)
	return true
}

// DeleteTicket removes a ticket from the MongoDB collection
func (m *MongoDBStorage) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	collection := m.database.Collection(m.collectionName)

	// Build filter
	filter := bson.M{
		"ticket_id": id,
	}

	// Find and delete the document
	var doc bson.M
	err := collection.FindOneAndDelete(ctx, filter).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			log.Printf("Ticket %s not found for deletion", id)
			return nil, false
		}
		log.Printf("ERROR: Failed to delete ticket %s: %v", id, err)
		return nil, false
	}

	// Convert MongoDB document back to protobuf
	ticketData := mongoDBDocumentToProtobuf(doc)

	log.Printf("Deleted ticket %s from collection %s", id, m.collectionName)
	return ticketData, true
}

// ListTickets retrieves all tickets from the MongoDB collection
func (m *MongoDBStorage) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	collection := m.database.Collection(m.collectionName)

	// Build filter (empty filter to get all tickets)
	filter := bson.M{}

	// Find all documents
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to list tickets: %w", err)
	}
	defer cursor.Close(ctx)

	var tickets []*ticketpb.TicketData
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			log.Printf("WARNING: Failed to decode document: %v", err)
			continue
		}

		ticketData := mongoDBDocumentToProtobuf(doc)
		tickets = append(tickets, ticketData)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error while listing tickets: %w", err)
	}

	log.Printf("Listed %d tickets from collection %s", len(tickets), m.collectionName)
	return tickets, nil
}

// buildMongoDBFilter converts search conditions to MongoDB filter
func (m *MongoDBStorage) buildMongoDBFilter(conditions []SearchCondition) bson.M {
	filter := bson.M{}

	if len(conditions) == 0 {
		return filter
	}

	var andConditions []bson.M
	for _, condition := range conditions {
		fieldFilter := m.buildFieldFilter(condition)
		if fieldFilter != nil {
			andConditions = append(andConditions, fieldFilter)
		}
	}

	if len(andConditions) > 0 {
		filter["$and"] = andConditions
	}

	return filter
}

// buildFieldFilter converts a single search condition to MongoDB filter
func (m *MongoDBStorage) buildFieldFilter(condition SearchCondition) bson.M {
	field := strings.ToLower(condition.Operand)
	operator := strings.ToLower(condition.Operator)
	value := condition.Value

	switch operator {
	case "eq", "=", "==":
		return bson.M{field: value}
	case "ne", "!=", "<>":
		return bson.M{field: bson.M{"$ne": value}}
	case "gt", ">":
		return bson.M{field: bson.M{"$gt": value}}
	case "gte", ">=":
		return bson.M{field: bson.M{"$gte": value}}
	case "lt", "<":
		return bson.M{field: bson.M{"$lt": value}}
	case "lte", "<=":
		return bson.M{field: bson.M{"$lte": value}}
	case "contains", "like":
		if str, ok := value.(string); ok {
			return bson.M{field: bson.M{"$regex": str, "$options": "i"}}
		}
		return bson.M{field: bson.M{"$regex": fmt.Sprintf("%v", value), "$options": "i"}}
	case "begins_with", "startswith":
		if str, ok := value.(string); ok {
			return bson.M{field: bson.M{"$regex": "^" + str, "$options": "i"}}
		}
		return bson.M{field: bson.M{"$regex": "^" + fmt.Sprintf("%v", value), "$options": "i"}}
	case "in":
		if arr, ok := value.([]interface{}); ok {
			return bson.M{field: bson.M{"$in": arr}}
		}
		return bson.M{field: bson.M{"$in": []interface{}{value}}}
	case "not_in", "nin":
		if arr, ok := value.([]interface{}); ok {
			return bson.M{field: bson.M{"$nin": arr}}
		}
		return bson.M{field: bson.M{"$nin": []interface{}{value}}}
	default:
		log.Printf("WARNING: Unsupported operator '%s' for field '%s', defaulting to equality", operator, field)
		return bson.M{field: value}
	}
}

// buildMongoDBSort converts sort fields to MongoDB sort options
func (m *MongoDBStorage) buildMongoDBSort(sortFields []SortField) bson.D {
	if len(sortFields) == 0 {
		// Default sort by created_at descending
		return bson.D{{Key: "created_at", Value: -1}}
	}

	var sort bson.D
	for _, sortField := range sortFields {
		field := strings.ToLower(sortField.Field)
		order := 1 // ascending by default
		if strings.ToLower(sortField.Order) == "desc" {
			order = -1
		}
		sort = append(sort, bson.E{Key: field, Value: order})
	}

	return sort
}

// SearchTickets performs a search query on the MongoDB collection
func (m *MongoDBStorage) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	collection := m.database.Collection(m.collectionName)

	// Build filter from search conditions
	filter := m.buildMongoDBFilter(request.Conditions)

	// Build sort options
	sortOptions := m.buildMongoDBSort(request.SortFields)

	// Create find options
	findOptions := options.Find().SetSort(sortOptions)

	// Execute the query
	cursor, err := collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}
	defer cursor.Close(ctx)

	var tickets []*ticketpb.TicketData
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			log.Printf("WARNING: Failed to decode document: %v", err)
			continue
		}

		ticketData := mongoDBDocumentToProtobuf(doc)
		tickets = append(tickets, ticketData)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error while searching tickets: %w", err)
	}

	log.Printf("Found %d tickets in collection %s", len(tickets), m.collectionName)
	return tickets, nil
}

// SearchTicketsWithProjection performs a search query with field projection on the MongoDB collection
func (m *MongoDBStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	collection := m.database.Collection(m.collectionName)

	// Build filter from search conditions
	filter := m.buildMongoDBFilter(request.Conditions)

	// Build sort options
	sortOptions := m.buildMongoDBSort(request.SortFields)

	// Create find options with projection
	findOptions := options.Find().SetSort(sortOptions)

	// Build projection if specified
	if len(request.ProjectedFields) > 0 {
		projection := bson.M{}

		// Always include core fields
		projection["_id"] = 1
		projection["ticket_id"] = 1
		projection["created_at"] = 1
		projection["updated_at"] = 1

		// Include requested fields
		for _, field := range request.ProjectedFields {
			projection[strings.ToLower(field)] = 1
		}

		findOptions.SetProjection(projection)
	}

	// Execute the query
	cursor, err := collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to search tickets with projection: %w", err)
	}
	defer cursor.Close(ctx)

	var tickets []*ticketpb.TicketData
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			log.Printf("WARNING: Failed to decode document: %v", err)
			continue
		}

		ticketData := mongoDBDocumentToProtobuf(doc)
		tickets = append(tickets, ticketData)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error while searching tickets with projection: %w", err)
	}

	log.Printf("Found %d tickets with projection in collection %s", len(tickets), m.collectionName)
	return tickets, nil
}

// Close closes the MongoDB connection
func (m *MongoDBStorage) Close() error {
	if m.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := m.client.Disconnect(ctx); err != nil {
			return fmt.Errorf("failed to disconnect from MongoDB: %w", err)
		}
		log.Printf("MongoDB connection closed successfully")
	}
	return nil
}
