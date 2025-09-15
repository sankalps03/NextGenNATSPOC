package storage

import (
	"context"
	"fmt"
	"log"

	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// ExampleUsagePostgreSQLDocumentDB demonstrates how to use the PostgreSQL DocumentDB storage with BSON
func ExampleUsagePostgreSQLDocumentDB() {
	// Initialize storage
	ctx := context.Background()
	connectionString := "host=localhost port=5433 user=docdbadmin password=SecurePass123! dbname=documentdb sslmode=disable"
	storage, err := NewPostgreSQLDocumentDBStorage(ctx, "ticket_hybrid", connectionString)
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Example 1: Create a ticket with fixed schema fields and BSON dynamic fields
	fmt.Println("=== Example 1: Create Ticket ===")
	ticketData := &ticketpb.TicketData{
		Id: "", // Will be auto-generated
		Fields: map[string]*ticketpb.FieldValue{
			// Fixed schema fields (stored in PostgreSQL columns for fast queries)
			"requesterid":  {Value: &ticketpb.FieldValue_IntValue{IntValue: 1001}},
			"technicianid": {Value: &ticketpb.FieldValue_IntValue{IntValue: 2001}},
			"statusid":     {Value: &ticketpb.FieldValue_IntValue{IntValue: 1}}, // Open
			"priorityid":   {Value: &ticketpb.FieldValue_IntValue{IntValue: 2}}, // High
			"companyid":    {Value: &ticketpb.FieldValue_IntValue{IntValue: 100}},
			"groupid":      {Value: &ticketpb.FieldValue_IntValue{IntValue: 10}},
			"categoryid":   {Value: &ticketpb.FieldValue_IntValue{IntValue: 5}},
			"subject":      {Value: &ticketpb.FieldValue_StringValue{StringValue: "Email server down"}},
			"description":  {Value: &ticketpb.FieldValue_StringValue{StringValue: "The main email server is not responding"}},
			"spam":         {Value: &ticketpb.FieldValue_BoolValue{BoolValue: false}},
			"removed":      {Value: &ticketpb.FieldValue_BoolValue{BoolValue: false}},

			// User fields (stored in user_fields BSON column)
			"updatedbyid": {Value: &ticketpb.FieldValue_IntValue{IntValue: 2002}},
			"resolvedby":  {Value: &ticketpb.FieldValue_IntValue{IntValue: 2001}},

			// Timing fields (stored in timing_fields BSON column)
			"totalonholdduration": {Value: &ticketpb.FieldValue_IntValue{IntValue: 3600000}},       // 1 hour in ms
			"totalresolutiontime": {Value: &ticketpb.FieldValue_IntValue{IntValue: 7200000}},       // 2 hours in ms
			"firstresponsetime":   {Value: &ticketpb.FieldValue_IntValue{IntValue: 1703123400000}}, // Unix timestamp

			// Workflow fields (stored in workflow_fields BSON column)
			"approvalstatus": {Value: &ticketpb.FieldValue_IntValue{IntValue: 1}}, // Pending
			"supportlevel":   {Value: &ticketpb.FieldValue_IntValue{IntValue: 2}}, // Level 2

			// General dynamic fields (stored in dynamic_fields BSON column)
			"name":                {Value: &ticketpb.FieldValue_StringValue{StringValue: "Critical Server Issue"}},
			"originaldescription": {Value: &ticketpb.FieldValue_StringValue{StringValue: "Original description from user"}},
			"impactid":            {Value: &ticketpb.FieldValue_IntValue{IntValue: 3}},
			"slaviolated":         {Value: &ticketpb.FieldValue_BoolValue{BoolValue: false}},
			"viprequest":          {Value: &ticketpb.FieldValue_BoolValue{BoolValue: true}},

			// Custom fields (stored in custom_fields BSON column)
			"customfield_department": {Value: &ticketpb.FieldValue_StringValue{StringValue: "IT Operations"}},
			"customfield_severity":   {Value: &ticketpb.FieldValue_IntValue{IntValue: 1}},
		},
	}

	err, result := storage.CreateTicket(ticketData)
	if err != nil {
		log.Printf("Create failed: %v", err)
		return
	}
	fmt.Printf("Created ticket: %s (DB ID: %v)\n", ticketData.Id, result["id"])

	// Example 2: Retrieve the ticket
	fmt.Println("\n=== Example 2: Get Ticket ===")
	retrievedTicket, found := storage.GetTicket(ticketData.Id, nil)
	if found {
		fmt.Printf("Retrieved ticket: %s\n", retrievedTicket.Id)
		fmt.Printf("Subject: %s\n", retrievedTicket.Fields["subject"].GetStringValue())
		fmt.Printf("Status ID: %d\n", retrievedTicket.Fields["statusid"].GetIntValue())
		fmt.Printf("VIP Request: %v\n", retrievedTicket.Fields["viprequest"].GetBoolValue())
		fmt.Printf("Custom Department: %s\n", retrievedTicket.Fields["customfield_department"].GetStringValue())
	}

	// Example 3: Update the ticket
	fmt.Println("\n=== Example 3: Update Ticket ===")
	retrievedTicket.Fields["statusid"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_IntValue{IntValue: 2}} // In Progress
	retrievedTicket.Fields["technicianid"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_IntValue{IntValue: 2002}}
	retrievedTicket.Fields["name"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: "Updated: Server Issue"}}
	retrievedTicket.Fields["customfield_progress"] = &ticketpb.FieldValue{Value: &ticketpb.FieldValue_StringValue{StringValue: "Investigating root cause"}}

	success := storage.UpdateTicket(retrievedTicket)
	if success {
		fmt.Println("Ticket updated successfully")
	}

	// Example 4: Search tickets using fixed schema fields (fast queries)
	fmt.Println("\n=== Example 4: Search by Fixed Fields ===")
	searchRequest := SearchRequest{
		Conditions: []SearchCondition{
			{Operand: "statusid", Operator: "eq", Value: 2},   // In Progress
			{Operand: "priorityid", Operator: "eq", Value: 2}, // High priority
		},
		SortFields: []SortField{
			{Field: "created_at", Order: "desc"},
		},
	}

	searchResults, err := storage.SearchTickets(searchRequest)
	if err != nil {
		log.Printf("Search failed: %v", err)
	} else {
		fmt.Printf("Found %d tickets with status=2 and priority=2\n", len(searchResults))
		for _, ticket := range searchResults {
			fmt.Printf("- %s: %s\n", ticket.Id, ticket.Fields["subject"].GetStringValue())
		}
	}

	// Example 5: Search tickets using dynamic BSON fields
	fmt.Println("\n=== Example 5: Search by Dynamic BSON Fields ===")
	dynamicSearchRequest := SearchRequest{
		Conditions: []SearchCondition{
			{Operand: "viprequest", Operator: "eq", Value: true},     // VIP requests (dynamic_fields BSON)
			{Operand: "name", Operator: "contains", Value: "Server"}, // Name contains "Server" (dynamic_fields BSON)
		},
	}

	dynamicResults, err := storage.SearchTickets(dynamicSearchRequest)
	if err != nil {
		log.Printf("Dynamic BSON search failed: %v", err)
	} else {
		fmt.Printf("Found %d VIP tickets with 'Server' in name (from BSON fields)\n", len(dynamicResults))
		for _, ticket := range dynamicResults {
			fmt.Printf("- %s: %s (VIP: %v)\n",
				ticket.Id,
				ticket.Fields["name"].GetStringValue(),
				ticket.Fields["viprequest"].GetBoolValue())
		}
	}

	// Example 6: Field projection (only return specific fields)
	fmt.Println("\n=== Example 6: Field Projection ===")
	projectionRequest := SearchRequest{
		Conditions: []SearchCondition{
			{Operand: "companyid", Operator: "eq", Value: 100},
		},
		ProjectedFields: []string{"subject", "statusid", "priorityid", "name"}, // Only these fields
		SortFields: []SortField{
			{Field: "priorityid", Order: "desc"},
		},
	}

	projectionResults, err := storage.SearchTicketsWithProjection(projectionRequest)
	if err != nil {
		log.Printf("Projection search failed: %v", err)
	} else {
		fmt.Printf("Found %d tickets for company 100 (projected fields only)\n", len(projectionResults))
		for _, ticket := range projectionResults {
			fmt.Printf("- %s: %s (Status: %d, Priority: %d, Name: %s)\n",
				ticket.Id,
				ticket.Fields["subject"].GetStringValue(),
				ticket.Fields["statusid"].GetIntValue(),
				ticket.Fields["priorityid"].GetIntValue(),
				ticket.Fields["name"].GetStringValue())
		}
	}

	// Example 7: Complex hybrid search (fixed + dynamic fields)
	fmt.Println("\n=== Example 7: Complex Hybrid Search ===")
	complexSearchRequest := SearchRequest{
		Conditions: []SearchCondition{
			{Operand: "statusid", Operator: "ne", Value: 4},             // Not closed (fixed field)
			{Operand: "priorityid", Operator: "gte", Value: 2},          // High or critical priority (fixed field)
			{Operand: "slaviolated", Operator: "eq", Value: false},      // SLA not violated (dynamic field)
			{Operand: "subject", Operator: "contains", Value: "server"}, // Subject contains server (fixed field)
		},
		SortFields: []SortField{
			{Field: "priorityid", Order: "desc"},
			{Field: "created_at", Order: "desc"},
		},
	}

	complexResults, err := storage.SearchTickets(complexSearchRequest)
	if err != nil {
		log.Printf("Complex search failed: %v", err)
	} else {
		fmt.Printf("Found %d tickets matching complex criteria\n", len(complexResults))
		for _, ticket := range complexResults {
			fmt.Printf("- %s: %s (Priority: %d, SLA Violated: %v)\n",
				ticket.Id,
				ticket.Fields["subject"].GetStringValue(),
				ticket.Fields["priorityid"].GetIntValue(),
				ticket.Fields["slaviolated"].GetBoolValue())
		}
	}

	// Example 8: List all tickets
	fmt.Println("\n=== Example 8: List All Tickets ===")
	allTickets, err := storage.ListTickets(nil)
	if err != nil {
		log.Printf("List failed: %v", err)
	} else {
		fmt.Printf("Total tickets in system: %d\n", len(allTickets))
	}

	// Example 9: Delete the ticket
	fmt.Println("\n=== Example 9: Delete Ticket ===")
	deletedTicket, deleted := storage.DeleteTicket(ticketData.Id)
	if deleted {
		fmt.Printf("Deleted ticket: %s\n", deletedTicket.Id)
	}

	fmt.Println("\n=== DocumentDB Hybrid Storage Example Complete ===")
}

// ExamplePerformanceComparison demonstrates performance characteristics
func ExamplePerformanceComparison() {
	fmt.Println("=== Performance Comparison Guide ===")
	fmt.Println()

	fmt.Println("Fixed Schema Fields (stored in columns):")
	fmt.Println("- Fast WHERE clauses, JOINs, and ORDER BY")
	fmt.Println("- Use standard B-tree indexes")
	fmt.Println("- Best for: requesterid, statusid, priorityid, subject, etc.")
	fmt.Println("- Example: WHERE statusid = 2 AND priorityid > 1")
	fmt.Println()

	fmt.Println("Dynamic Fields (stored in JSONB):")
	fmt.Println("- Uses GIN indexes for efficient queries")
	fmt.Println("- Flexible schema, can add fields without migrations")
	fmt.Println("- Best for: custom fields, less common fields, metadata")
	fmt.Println("- Example: WHERE dynamic_fields @> '{\"boolean_fields\": {\"viprequest\": true}}'")
	fmt.Println()

	fmt.Println("Hybrid Queries:")
	fmt.Println("- Combine both for optimal performance")
	fmt.Println("- Filter on fixed fields first, then dynamic fields")
	fmt.Println("- Example: WHERE statusid = 1 AND dynamic_fields->>'custom_field' = 'value'")
	fmt.Println()

	fmt.Println("Best Practices:")
	fmt.Println("1. Put frequently queried fields in fixed schema")
	fmt.Println("2. Use dynamic fields for custom/flexible data")
	fmt.Println("3. Always use proper indexes")
	fmt.Println("4. Monitor query performance and adjust field placement")
}

// ExampleBSONQueries shows advanced BSON query examples
func ExampleBSONQueries() {
	fmt.Println("=== Advanced BSON Query Examples ===")
	fmt.Println()

	fmt.Println("1. Exact match in dynamic_fields BSON:")
	fmt.Println("   WHERE dynamic_fields->>'name' = 'Server Issue'")
	fmt.Println()

	fmt.Println("2. Contains check (case-insensitive) in dynamic_fields BSON:")
	fmt.Println("   WHERE dynamic_fields->>'originaldescription' ILIKE '%email%'")
	fmt.Println()

	fmt.Println("3. Numeric comparison in timing_fields BSON:")
	fmt.Println("   WHERE (timing_fields->>'totalonholdduration')::bigint > 1000")
	fmt.Println()

	fmt.Println("4. Boolean check in dynamic_fields BSON:")
	fmt.Println("   WHERE (dynamic_fields->>'viprequest')::boolean = true")
	fmt.Println()

	fmt.Println("5. User fields BSON queries:")
	fmt.Println("   WHERE (user_fields->>'updatedbyid')::bigint = 2001")
	fmt.Println()

	fmt.Println("6. Workflow fields BSON queries:")
	fmt.Println("   WHERE (workflow_fields->>'approvalstatus')::int = 1")
	fmt.Println()

	fmt.Println("7. Check if BSON field exists:")
	fmt.Println("   WHERE dynamic_fields ? 'customfield_department'")
	fmt.Println()

	fmt.Println("8. Multiple BSON column search:")
	fmt.Println("   WHERE user_fields @> '{\"updatedbyid\": 2001}' OR timing_fields @> '{\"totalonholdduration\": 1000}'")
	fmt.Println()

	fmt.Println("9. Custom fields BSON queries:")
	fmt.Println("   WHERE custom_fields->>'customfield_department' = 'IT Operations'")
	fmt.Println()

	fmt.Println("10. Full-text search across all BSON columns:")
	fmt.Println("   WHERE (dynamic_fields::text ILIKE '%server%' OR user_fields::text ILIKE '%server%')")
}
