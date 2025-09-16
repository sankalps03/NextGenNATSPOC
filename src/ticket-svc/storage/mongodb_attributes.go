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

// MongoDBAttributesStorage implements ticket storage using MongoDB with static scalar fields + attributes
// Uses static scalar fields for core data and attributes subdocument for custom fields
type MongoDBAttributesStorage struct {
	client         *mongo.Client
	database       *mongo.Database
	databaseName   string
	collectionName string
}

// AttributeKV represents a key-value pair for dynamic attributes with typed values
type AttributeKV struct {
	K    string        `bson:"k"`               // key
	VStr *string       `bson:"v_str,omitempty"` // string value
	VNum *float64      `bson:"v_num,omitempty"` // numeric value (int/float)
	VArr []interface{} `bson:"v_arr,omitempty"` // array value
	VObj interface{}   `bson:"v_obj,omitempty"` // object value
}

// TicketDocument represents the MongoDB document structure
type TicketDocument struct {
	ID                  primitive.ObjectID `bson:"_id,omitempty"`
	TicketID            string             `bson:"ticket_id"`
	Summary             string             `bson:"summary,omitempty"`
	Description         string             `bson:"description,omitempty"`
	OriginalDescription string             `bson:"originaldescription,omitempty"`
	CreatedAt           time.Time          `bson:"createdat"`
	UpdatedAt           time.Time          `bson:"updatedat"`
	Attributes          []AttributeKV      `bson:"attributes"`
}

// NewMongoDBAttributesStorage creates a new MongoDB attributes storage instance
func NewMongoDBAttributesStorage(connectionString, databaseName, collectionName string) (*MongoDBAttributesStorage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect to MongoDB
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(connectionString))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// Test the connection
	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	database := client.Database(databaseName)
	storage := &MongoDBAttributesStorage{
		client:         client,
		database:       database,
		databaseName:   databaseName,
		collectionName: collectionName,
	}

	// Create indexes
	err = storage.createIndexes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create indexes: %w", err)
	}

	log.Printf("MongoDB Attributes Storage initialized: database=%s, collection=%s", databaseName, collectionName)
	return storage, nil
}

// createIndexes creates the required indexes for the collection
func (m *MongoDBAttributesStorage) createIndexes(ctx context.Context) error {
	collection := m.database.Collection(m.collectionName)

	indexes := []mongo.IndexModel{
		// Unique index on ticket_id
		{
			Keys:    bson.D{{Key: "ticket_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_ticket_id_unique"),
		},
		// Index on createdat for time-based queries
		{
			Keys:    bson.D{{Key: "createdat", Value: 1}},
			Options: options.Index().SetName("idx_createdat"),
		},
		// Compound index on createdat and updatedat
		{
			Keys:    bson.D{{Key: "createdat", Value: 1}, {Key: "updatedat", Value: 1}},
			Options: options.Index().SetName("idx_time_range"),
		},
		// Text index on summary and description for full-text search
		{
			Keys: bson.D{
				{Key: "summary", Value: "text"},
				{Key: "description", Value: "text"},
				{Key: "originaldescription", Value: "text"},
			},
			Options: options.Index().SetName("idx_text_search"),
		},
		// Compound indexes on attributes key-value pairs for dynamic field queries
		{
			Keys:    bson.D{{Key: "attributes.k", Value: 1}, {Key: "attributes.v_str", Value: 1}},
			Options: options.Index().SetName("idx_attributes_k_vstr"),
		},
		{
			Keys:    bson.D{{Key: "attributes.k", Value: 1}, {Key: "attributes.v_num", Value: 1}},
			Options: options.Index().SetName("idx_attributes_k_vnum"),
		},
		{
			Keys:    bson.D{{Key: "attributes.k", Value: 1}, {Key: "attributes.v_arr", Value: 1}},
			Options: options.Index().SetName("idx_attributes_k_varr"),
		},
		{
			Keys:    bson.D{{Key: "attributes.k", Value: 1}, {Key: "attributes.v_obj", Value: 1}},
			Options: options.Index().SetName("idx_attributes_k_vobj"),
		},
	}

	_, err := collection.Indexes().CreateMany(ctx, indexes)
	if err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	log.Printf("Created indexes for collection %s", m.collectionName)
	return nil
}

// protobufToDocument converts TicketData protobuf to MongoDB document
func protobufToDocument(ticketData *ticketpb.TicketData) (*TicketDocument, error) {
	// Parse timestamps
	createdAt, err := time.Parse(time.RFC3339, ticketData.CreatedAt)
	if err != nil {
		createdAt = time.Now()
	}

	updatedAt, err := time.Parse(time.RFC3339, ticketData.UpdatedAt)
	if err != nil {
		updatedAt = time.Now()
	}

	// Initialize document with static scalar fields
	doc := &TicketDocument{
		TicketID:   ticketData.Id,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
		Attributes: make([]AttributeKV, 0),
	}

	// Process fields from protobuf
	for fieldName, fieldValue := range ticketData.Fields {
		switch fieldName {
		case "summary":
			if fieldValue.GetStringValue() != "" {
				doc.Summary = fieldValue.GetStringValue()
			}
		case "description":
			if fieldValue.GetStringValue() != "" {
				doc.Description = fieldValue.GetStringValue()
			}
		case "originaldescription":
			if fieldValue.GetStringValue() != "" {
				doc.OriginalDescription = fieldValue.GetStringValue()
			}
		default:
			// All other fields go into attributes array as typed key-value pairs
			attr := createTypedAttributeKV(fieldName, fieldValue)
			if attr != nil {
				doc.Attributes = append(doc.Attributes, *attr)
			}
		}
	}

	return doc, nil
}

// extractFieldValue extracts the actual value from FieldValue protobuf
func extractFieldValue(fieldValue *ticketpb.FieldValue) interface{} {
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

// createTypedAttributeKV creates a typed AttributeKV from protobuf FieldValue
func createTypedAttributeKV(key string, fieldValue *ticketpb.FieldValue) *AttributeKV {
	if fieldValue == nil {
		return nil
	}

	attr := &AttributeKV{K: key}

	switch v := fieldValue.Value.(type) {
	case *ticketpb.FieldValue_StringValue:
		attr.VStr = &v.StringValue
	case *ticketpb.FieldValue_IntValue:
		floatVal := float64(v.IntValue)
		attr.VNum = &floatVal
	case *ticketpb.FieldValue_DoubleValue:
		attr.VNum = &v.DoubleValue
	case *ticketpb.FieldValue_BoolValue:
		// Store boolean as numeric (0 or 1) for consistent indexing
		var boolAsNum float64
		if v.BoolValue {
			boolAsNum = 1
		} else {
			boolAsNum = 0
		}
		attr.VNum = &boolAsNum
	case *ticketpb.FieldValue_BytesValue:
		// Store bytes as object
		attr.VObj = v.BytesValue
	case *ticketpb.FieldValue_StringArray:
		// Convert to interface{} array
		arr := make([]interface{}, len(v.StringArray.Values))
		for i, val := range v.StringArray.Values {
			arr[i] = val
		}
		attr.VArr = arr
	default:
		return nil
	}

	return attr
}

// convertTypedAttributeToFieldValue converts typed AttributeKV back to protobuf FieldValue
func convertTypedAttributeToFieldValue(attr AttributeKV) *ticketpb.FieldValue {
	// Determine which type field is set and convert accordingly
	if attr.VStr != nil {
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: *attr.VStr},
		}
	}

	if attr.VNum != nil {
		// Check if this was originally a boolean (0 or 1)
		if *attr.VNum == 0 || *attr.VNum == 1 {
			// Could be boolean, but we need context to determine
			// For now, treat as number unless we have metadata
			if *attr.VNum == float64(int64(*attr.VNum)) {
				// It's an integer
				return &ticketpb.FieldValue{
					Value: &ticketpb.FieldValue_IntValue{IntValue: int64(*attr.VNum)},
				}
			}
		}

		// Check if it's an integer value
		if *attr.VNum == float64(int64(*attr.VNum)) {
			return &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_IntValue{IntValue: int64(*attr.VNum)},
			}
		}

		// It's a double
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_DoubleValue{DoubleValue: *attr.VNum},
		}
	}

	if attr.VArr != nil {
		// Convert back to string array (assuming it was originally string array)
		stringArr := make([]string, len(attr.VArr))
		for i, val := range attr.VArr {
			if str, ok := val.(string); ok {
				stringArr[i] = str
			} else {
				stringArr[i] = fmt.Sprintf("%v", val)
			}
		}
		return &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringArray{
				StringArray: &ticketpb.StringArray{Values: stringArr},
			},
		}
	}

	if attr.VObj != nil {
		// Convert object to bytes
		if bytes, ok := attr.VObj.([]byte); ok {
			return &ticketpb.FieldValue{
				Value: &ticketpb.FieldValue_BytesValue{BytesValue: bytes},
			}
		}
	}

	return nil
}

// documentToProtobuf converts MongoDB document to TicketData protobuf
func documentToProtobuf(doc *TicketDocument) (*ticketpb.TicketData, error) {
	ticketData := &ticketpb.TicketData{
		Id:        doc.TicketID,
		CreatedAt: doc.CreatedAt.Format(time.RFC3339),
		UpdatedAt: doc.UpdatedAt.Format(time.RFC3339),
		Fields:    make(map[string]*ticketpb.FieldValue),
	}

	// Add static scalar fields to Fields map
	if doc.Summary != "" {
		ticketData.Fields["summary"] = &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: doc.Summary},
		}
	}
	if doc.Description != "" {
		ticketData.Fields["description"] = &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: doc.Description},
		}
	}
	if doc.OriginalDescription != "" {
		ticketData.Fields["originaldescription"] = &ticketpb.FieldValue{
			Value: &ticketpb.FieldValue_StringValue{StringValue: doc.OriginalDescription},
		}
	}

	// Add attributes to Fields map
	for _, attr := range doc.Attributes {
		fieldValue := convertTypedAttributeToFieldValue(attr)
		if fieldValue != nil {
			ticketData.Fields[attr.K] = fieldValue
		}
	}

	return ticketData, nil
}

// generateTicketID generates a unique ticket ID if not provided
func (m *MongoDBAttributesStorage) generateTicketID() string {
	return fmt.Sprintf("TKT-%d", time.Now().UnixNano()/1000000)
}

// CreateTicket stores a new ticket in the MongoDB collection
func (m *MongoDBAttributesStorage) CreateTicket(ticketData *ticketpb.TicketData) (error, map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Generate ticket ID if not provided
	if ticketData.Id == "" {
		ticketData.Id = m.generateTicketID()
	}

	// Convert protobuf to MongoDB document
	doc, err := protobufToDocument(ticketData)
	if err != nil {
		return fmt.Errorf("failed to convert protobuf to document: %w", err), nil
	}

	// Insert document
	collection := m.database.Collection(m.collectionName)
	result, err := collection.InsertOne(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to insert ticket: %w", err), nil
	}

	// Return key-value representation for compatibility
	kvDocs := map[string]interface{}{
		"ticket_id": ticketData.Id,
		"_id":       result.InsertedID,
	}

	//log.Printf("Created ticket %s with MongoDB ID %v", ticketData.Id, result.InsertedID)
	return nil, kvDocs
}

// GetTicket retrieves a ticket by ID from the MongoDB collection
func (m *MongoDBAttributesStorage) GetTicket(id string, store jetstream.KeyValue) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	collection := m.database.Collection(m.collectionName)
	filter := bson.M{"ticket_id": id}

	var doc TicketDocument
	err := collection.FindOne(ctx, filter).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, false
		}
		log.Printf("Error retrieving ticket %s: %v", id, err)
		return nil, false
	}

	// Convert document to protobuf
	ticketData, err := documentToProtobuf(&doc)
	if err != nil {
		log.Printf("Error converting document to protobuf for ticket %s: %v", id, err)
		return nil, false
	}

	return ticketData, true
}

// UpdateTicket updates an existing ticket in the MongoDB collection
func (m *MongoDBAttributesStorage) UpdateTicket(ticketData *ticketpb.TicketData) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Convert protobuf to MongoDB document
	doc, err := protobufToDocument(ticketData)
	if err != nil {
		log.Printf("Error converting protobuf to document for ticket %s: %v", ticketData.Id, err)
		return false
	}

	// Update the document
	collection := m.database.Collection(m.collectionName)
	filter := bson.M{"ticket_id": ticketData.Id}
	update := bson.M{
		"$set": bson.M{
			"summary":             doc.Summary,
			"description":         doc.Description,
			"originaldescription": doc.OriginalDescription,
			"updatedat":           doc.UpdatedAt,
			"attributes":          doc.Attributes,
		},
	}

	result, err := collection.UpdateOne(ctx, filter, update)
	if err != nil {
		log.Printf("Error updating ticket %s: %v", ticketData.Id, err)
		return false
	}

	if result.MatchedCount == 0 {
		log.Printf("No ticket found with ID %s for update", ticketData.Id)
		return false
	}

	log.Printf("Updated ticket %s", ticketData.Id)
	return true
}

// DeleteTicket removes a ticket from the MongoDB collection
func (m *MongoDBAttributesStorage) DeleteTicket(id string) (*ticketpb.TicketData, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	collection := m.database.Collection(m.collectionName)
	filter := bson.M{"ticket_id": id}

	// First, get the ticket to return it
	var doc TicketDocument
	err := collection.FindOne(ctx, filter).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, false
		}
		log.Printf("Error finding ticket %s for deletion: %v", id, err)
		return nil, false
	}

	// Delete the ticket
	result, err := collection.DeleteOne(ctx, filter)
	if err != nil {
		log.Printf("Error deleting ticket %s: %v", id, err)
		return nil, false
	}

	if result.DeletedCount == 0 {
		log.Printf("No ticket found with ID %s for deletion", id)
		return nil, false
	}

	// Convert document to protobuf for return
	ticketData, err := documentToProtobuf(&doc)
	if err != nil {
		log.Printf("Error converting document to protobuf for deleted ticket %s: %v", id, err)
		return nil, false
	}

	log.Printf("Deleted ticket %s", id)
	return ticketData, true
}

// ListTickets retrieves all tickets from the MongoDB collection
func (m *MongoDBAttributesStorage) ListTickets(store jetstream.KeyValue) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	collection := m.database.Collection(m.collectionName)
	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("failed to find tickets: %w", err)
	}
	defer cursor.Close(ctx)

	var tickets []*ticketpb.TicketData
	for cursor.Next(ctx) {
		var doc TicketDocument
		if err := cursor.Decode(&doc); err != nil {
			log.Printf("Error decoding ticket document: %v", err)
			continue
		}

		ticketData, err := documentToProtobuf(&doc)
		if err != nil {
			log.Printf("Error converting document to protobuf: %v", err)
			continue
		}

		tickets = append(tickets, ticketData)
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	log.Printf("Listed %d tickets", len(tickets))
	return tickets, nil
}

// SearchTickets searches for tickets based on conditions
func (m *MongoDBAttributesStorage) SearchTickets(request SearchRequest) ([]*ticketpb.TicketData, error) {
	return m.searchTicketsInternal(request, false)
}

// SearchTicketsWithProjection searches for tickets with field projection
func (m *MongoDBAttributesStorage) SearchTicketsWithProjection(request SearchRequest) ([]*ticketpb.TicketData, error) {
	return m.searchTicketsInternal(request, true)
}

// searchTicketsInternal performs the actual search with optional projection
func (m *MongoDBAttributesStorage) searchTicketsInternal(request SearchRequest, useProjection bool) ([]*ticketpb.TicketData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build MongoDB filter from search conditions
	filter := m.buildMongoFilter(request.Conditions)

	// Build projection if requested
	var projection bson.M
	if useProjection && len(request.ProjectedFields) > 0 {
		projection = m.buildMongoProjection(request.ProjectedFields)
	}

	// Build sort options
	var sortOptions bson.D
	if len(request.SortFields) > 0 {
		sortOptions = m.buildMongoSort(request.SortFields)
	}

	// Execute query
	collection := m.database.Collection(m.collectionName)
	findOptions := options.Find()
	if projection != nil {
		findOptions.SetProjection(projection)
	}
	if sortOptions != nil {
		findOptions.SetSort(sortOptions)
	}

	findOptions.SetBatchSize(1000)

	start := time.Now()

	cursor, err := collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}
	defer cursor.Close(ctx)

	fmt.Println("Query execution time:", time.Since(start))

	var raw = make([]bson.Raw, 0, cursor.RemainingBatchLength())

	index := 0

	for cursor.Next(ctx) {
		raw = append(raw, cursor.Current)
		index++
	}

	fmt.Println(fmt.Sprintf("Query fetch time: %v for %v rows", time.Since(start), len(raw)))

	var tickets = make([]*ticketpb.TicketData, len(raw))
	start2 := time.Now()
	for i, r := range raw {
		var doc TicketDocument

		if err := bson.Unmarshal(r, &doc); err != nil {
			log.Printf("Error decoding ticket document: %v", err)
			continue
		}

		ticketData, err := documentToProtobuf(&doc)
		if err != nil {
			log.Printf("Error converting document to protobuf: %v", err)
			continue
		}

		// Apply field projection at protobuf level if needed
		if useProjection && len(request.ProjectedFields) > 0 {
			ticketData = m.applyProtobufProjection(ticketData, request.ProjectedFields)
		}

		tickets[i] = ticketData
	}

	fmt.Println(fmt.Sprintf("Query fetch time total: %v , unmarshal time : %v,  for %v rows", time.Since(start), time.Since(start2), len(tickets)))

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	log.Printf("Found %d tickets matching search criteria", len(tickets))
	return tickets, nil
}

// buildMongoFilter converts search conditions to MongoDB filter
func (m *MongoDBAttributesStorage) buildMongoFilter(conditions []SearchCondition) bson.M {
	if len(conditions) == 0 {
		return bson.M{}
	}

	var filters []bson.M
	for _, condition := range conditions {
		filter := m.buildSingleCondition(condition)
		if filter != nil {
			filters = append(filters, filter)
		}
	}

	if len(filters) == 0 {
		return bson.M{}
	}

	if len(filters) == 1 {
		return filters[0]
	}

	return bson.M{"$and": filters}
}

// buildSingleCondition converts a single search condition to MongoDB filter
func (m *MongoDBAttributesStorage) buildSingleCondition(condition SearchCondition) bson.M {
	fieldName := condition.Operand
	operator := condition.Operator
	value := condition.Value

	// Check if this is a static scalar field
	isStaticField := false
	var mongoField string
	switch fieldName {
	case "id":
		mongoField = "ticket_id"
		isStaticField = true
	case "summary":
		mongoField = "summary"
		isStaticField = true
	case "description":
		mongoField = "description"
		isStaticField = true
	case "originaldescription":
		mongoField = "originaldescription"
		isStaticField = true
	case "createdat", "created_at":
		mongoField = "createdat"
		isStaticField = true
	case "updatedat", "updated_at":
		mongoField = "updatedat"
		isStaticField = true
	default:
		// This is a dynamic field in attributes array
		isStaticField = false
	}

	// Convert value based on field type
	convertedValue := m.convertSearchValue(value, fieldName)

	// Build condition based on whether it's static or dynamic field
	if isStaticField {
		// Static field - direct query
		return m.buildStaticFieldCondition(mongoField, operator, convertedValue)
	} else {
		// Dynamic field - use $elemMatch on attributes array
		return m.buildDynamicFieldCondition(fieldName, operator, convertedValue)
	}
}

// buildStaticFieldCondition builds condition for static scalar fields
func (m *MongoDBAttributesStorage) buildStaticFieldCondition(mongoField string, operator string, value interface{}) bson.M {
	switch operator {
	case "eq":
		return bson.M{mongoField: value}
	case "ne":
		return bson.M{mongoField: bson.M{"$ne": value}}
	case "gt":
		return bson.M{mongoField: bson.M{"$gt": value}}
	case "gte":
		return bson.M{mongoField: bson.M{"$gte": value}}
	case "lt":
		return bson.M{mongoField: bson.M{"$lt": value}}
	case "lte":
		return bson.M{mongoField: bson.M{"$lte": value}}
	case "contains":
		if str, ok := value.(string); ok {
			return bson.M{mongoField: bson.M{"$regex": str, "$options": "i"}}
		}
		return bson.M{mongoField: bson.M{"$regex": fmt.Sprintf("%v", value), "$options": "i"}}
	case "begins_with":
		if str, ok := value.(string); ok {
			return bson.M{mongoField: bson.M{"$regex": "^" + str, "$options": "i"}}
		}
		return bson.M{mongoField: bson.M{"$regex": "^" + fmt.Sprintf("%v", value), "$options": "i"}}
	case "in":
		if arr, ok := value.([]interface{}); ok {
			return bson.M{mongoField: bson.M{"$in": arr}}
		}
		return bson.M{mongoField: bson.M{"$in": []interface{}{value}}}
	case "exists":
		if exists, ok := value.(bool); ok {
			return bson.M{mongoField: bson.M{"$exists": exists}}
		}
		return bson.M{mongoField: bson.M{"$exists": true}}
	default:
		log.Printf("Unsupported operator: %s, defaulting to eq", operator)
		return bson.M{mongoField: value}
	}
}

// buildDynamicFieldCondition builds condition for dynamic fields using $elemMatch with typed values
func (m *MongoDBAttributesStorage) buildDynamicFieldCondition(fieldName string, operator string, value interface{}) bson.M {
	// Determine the appropriate value field based on the value type
	valueField, convertedValue := m.determineValueField(value)

	switch operator {
	case "eq":
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, valueField: convertedValue}}}
	case "ne":
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, valueField: bson.M{"$ne": convertedValue}}}}
	case "gt":
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, valueField: bson.M{"$gt": convertedValue}}}}
	case "gte":
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, valueField: bson.M{"$gte": convertedValue}}}}
	case "lt":
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, valueField: bson.M{"$lt": convertedValue}}}}
	case "lte":
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, valueField: bson.M{"$lte": convertedValue}}}}
	case "contains":
		// String operations only work on v_str
		if valueField == "v_str" {
			if str, ok := convertedValue.(string); ok {
				return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, "v_str": bson.M{"$regex": str, "$options": "i"}}}}
			}
		}
		// Fallback to string conversion
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, "v_str": bson.M{"$regex": fmt.Sprintf("%v", value), "$options": "i"}}}}
	case "begins_with":
		// String operations only work on v_str
		if valueField == "v_str" {
			if str, ok := convertedValue.(string); ok {
				return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, "v_str": bson.M{"$regex": "^" + str, "$options": "i"}}}}
			}
		}
		// Fallback to string conversion
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, "v_str": bson.M{"$regex": "^" + fmt.Sprintf("%v", value), "$options": "i"}}}}
	case "in":
		if arr, ok := convertedValue.([]interface{}); ok {
			return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, valueField: bson.M{"$in": arr}}}}
		}
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, valueField: bson.M{"$in": []interface{}{convertedValue}}}}}
	case "exists":
		if exists, ok := value.(bool); ok && exists {
			return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName}}}
		}
		// For exists: false, we need to check that no element has this key
		return bson.M{"attributes": bson.M{"$not": bson.M{"$elemMatch": bson.M{"k": fieldName}}}}
	default:
		log.Printf("Unsupported operator: %s, defaulting to eq", operator)
		return bson.M{"attributes": bson.M{"$elemMatch": bson.M{"k": fieldName, valueField: convertedValue}}}
	}
}

// determineValueField determines which typed value field to use based on the value type
func (m *MongoDBAttributesStorage) determineValueField(value interface{}) (string, interface{}) {
	if value == nil {
		return "v_str", nil
	}

	switch v := value.(type) {
	case string:
		return "v_str", v
	case int, int8, int16, int32, int64:
		// Convert all integer types to float64 for consistent storage
		switch iv := v.(type) {
		case int:
			return "v_num", float64(iv)
		case int8:
			return "v_num", float64(iv)
		case int16:
			return "v_num", float64(iv)
		case int32:
			return "v_num", float64(iv)
		case int64:
			return "v_num", float64(iv)
		}
	case float32, float64:
		switch fv := v.(type) {
		case float32:
			return "v_num", float64(fv)
		case float64:
			return "v_num", fv
		}
	case bool:
		// Store boolean as numeric (0 or 1)
		if v {
			return "v_num", float64(1)
		}
		return "v_num", float64(0)
	case []interface{}:
		return "v_arr", v
	case []string:
		// Convert string array to interface{} array
		arr := make([]interface{}, len(v))
		for i, str := range v {
			arr[i] = str
		}
		return "v_arr", arr
	default:
		// For complex objects, use v_obj
		return "v_obj", v
	}

	// Fallback to string
	return "v_str", fmt.Sprintf("%v", value)
}

// convertSearchValue converts search value to appropriate type
func (m *MongoDBAttributesStorage) convertSearchValue(value interface{}, fieldName string) interface{} {
	if value == nil {
		return nil
	}

	// Handle timestamp fields
	if fieldName == "createdat" || fieldName == "updatedat" || fieldName == "created_at" || fieldName == "updated_at" {
		switch v := value.(type) {
		case string:
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t
			}
			// Try parsing as Unix timestamp
			if timestamp, err := strconv.ParseInt(v, 10, 64); err == nil {
				return time.Unix(timestamp/1000, (timestamp%1000)*1000000)
			}
		case int64:
			return time.Unix(v/1000, (v%1000)*1000000)
		case float64:
			return time.Unix(int64(v)/1000, (int64(v)%1000)*1000000)
		}
	}

	// Handle numeric conversions
	switch v := value.(type) {
	case string:
		// Try to convert string to number if it looks like one
		if intVal, err := strconv.ParseInt(v, 10, 64); err == nil {
			return intVal
		}
		if floatVal, err := strconv.ParseFloat(v, 64); err == nil {
			return floatVal
		}
		return v
	case float64:
		// JSON numbers come as float64
		if v == float64(int64(v)) {
			return int64(v)
		}
		return v
	default:
		return v
	}
}

// buildMongoProjection creates MongoDB projection from field list
func (m *MongoDBAttributesStorage) buildMongoProjection(projectedFields []string) bson.M {
	projection := bson.M{}

	// Always include _id and ticket_id
	projection["_id"] = 1
	projection["ticket_id"] = 1

	// Track if we need any attributes fields
	hasAttributeFields := false

	for _, field := range projectedFields {
		switch field {
		case "id":
			projection["ticket_id"] = 1
		case "summary":
			projection["summary"] = 1
		case "description":
			projection["description"] = 1
		case "originaldescription":
			projection["originaldescription"] = 1
		case "createdat", "created_at":
			projection["createdat"] = 1
		case "updatedat", "updated_at":
			projection["updatedat"] = 1
		default:
			// This is a dynamic field in attributes - mark that we need attributes
			hasAttributeFields = true
		}
	}

	// Include the full attributes object if any dynamic fields are requested
	// This avoids path collision by not trying to project individual attribute fields
	// We'll filter the specific fields at the application level in applyProtobufProjection
	if hasAttributeFields {
		projection["attributes"] = 1
	}

	return projection
}

// buildMongoSort creates MongoDB sort options from sort fields
func (m *MongoDBAttributesStorage) buildMongoSort(sortFields []SortField) bson.D {
	var sort bson.D

	for _, sortField := range sortFields {
		var mongoField string
		switch sortField.Field {
		case "id":
			mongoField = "ticket_id"
		case "summary":
			mongoField = "summary"
		case "description":
			mongoField = "description"
		case "originaldescription":
			mongoField = "originaldescription"
		case "createdat", "created_at":
			mongoField = "createdat"
		case "updatedat", "updated_at":
			mongoField = "updatedat"
		default:
			// Dynamic field in attributes
			mongoField = "attributes." + sortField.Field
		}

		direction := 1
		if strings.ToLower(sortField.Order) == "desc" {
			direction = -1
		}

		sort = append(sort, bson.E{Key: mongoField, Value: direction})
	}

	return sort
}

// applyProtobufProjection applies field projection at the protobuf level
func (m *MongoDBAttributesStorage) applyProtobufProjection(ticketData *ticketpb.TicketData, projectedFields []string) *ticketpb.TicketData {
	if len(projectedFields) == 0 {
		return ticketData
	}

	// Create new ticket data with only projected fields
	projected := &ticketpb.TicketData{
		Id:        ticketData.Id,
		CreatedAt: ticketData.CreatedAt,
		UpdatedAt: ticketData.UpdatedAt,
		Fields:    make(map[string]*ticketpb.FieldValue),
	}

	// Always include id field
	projectedFieldsMap := make(map[string]bool)
	projectedFieldsMap["id"] = true
	for _, field := range projectedFields {
		projectedFieldsMap[field] = true
	}

	// Copy only projected fields
	for fieldName, fieldValue := range ticketData.Fields {
		if projectedFieldsMap[fieldName] {
			projected.Fields[fieldName] = fieldValue
		}
	}

	return projected
}

// Close closes the MongoDB connection
func (m *MongoDBAttributesStorage) Close() error {
	if m.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := m.client.Disconnect(ctx)
		if err != nil {
			return fmt.Errorf("failed to disconnect from MongoDB: %w", err)
		}

		log.Printf("MongoDB Attributes Storage connection closed")
	}
	return nil
}
