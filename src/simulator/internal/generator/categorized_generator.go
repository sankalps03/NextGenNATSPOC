package generator

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// CategorizedTicketGenerator generates tickets based on the specific field categories
type CategorizedTicketGenerator struct {
	*Generator // Embed the original generator
	rand       *rand.Rand
}

// FieldCategories defines the organized field structure
type FieldCategories struct {
	BaseColumns       []string
	RequestUserInfo   []string
	TimeSLAManagement []string
	LifecycleWorkflow []string
}

// NewCategorizedGenerator creates a new categorized ticket generator
func NewCategorizedGenerator(baseGen *Generator) *CategorizedTicketGenerator {
	return &CategorizedTicketGenerator{
		Generator: baseGen,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// GetFieldCategories returns the organized field structure as specified
func (g *CategorizedTicketGenerator) GetFieldCategories() FieldCategories {
	return FieldCategories{
		BaseColumns: []string{
			"id", "ticket_id", "name", "createdbyid", "createdtime", "updatedbyid", "updatedtime",
			"statusid", "priorityid", "requesterid", "subject", "description", "originaldescription",
			"oobtype", "callfrom", "emailreadconfigemail",
		},
		RequestUserInfo: []string{
			"categoryid", "departmentid", "locationid", "groupid", "impactid", "urgencyid",
			"technicianid", "supportlevel", "vendorid", "companyid", "servicecatalogid",
			"sourceid", "requesttype", "suggestedcategoryid", "suggestedgroupid",
			"violateducid", "transitionmodelid", "messengerconfigid", "templateid",
			"emailreadconfigid",
		},
		TimeSLAManagement: []string{
			"dueby", "duetimemanuallyupdated", "firstresponsetime", "responsedue",
			"responseduelevel", "responsedueviolated", "resolutionescalationtime",
			"responseescalationtime", "totalresolutiontime", "totalworkingtime",
			"totalslapausetime", "totalonholdduration", "oladueby", "oldoladueby",
			"olaviolated", "olaescalationtime", "oladuelevel", "ucdueby", "olducdueby",
			"ucduelevel", "ucescalationtime", "ucviolated", "totalucworkingtime",
			"totalucresolutiontime", "totaluconholdduration", "totalucpausetime",
			"olddueby", "oldresponsedue", "violatedslaid", "lastviolationtime",
			"lastolaviolationtime", "lastucviolationtime", "askfeedbackdate",
			"firstfeedbackdate", "lastapproveddate",
		},
		LifecycleWorkflow: []string{
			"approvalstatus", "approvaltype", "resolutionduelevel", "removed",
			"removedbyid", "removedtime", "reopened", "resolvedby", "closedby",
			"lastopenedtime", "lastresolvedtime", "lastclosedtime", "statuschangedtime",
			"groupchangedtime", "migrated", "purchaserequest", "spam", "viprequest",
			"slaviolated", "olaviolated", "ucviolated",
		},
	}
}

// GenerateCategorizedTicket creates a ticket with fields organized by categories
func (g *CategorizedTicketGenerator) GenerateCategorizedTicket(ctx context.Context, categoryID int64) (map[string]interface{}, error) {
	categories := g.GetFieldCategories()
	ticket := make(map[string]interface{})

	// Generate base columns (always present)
	g.generateBaseColumns(ticket, categoryID)

	// Generate fields from each category based on category type
	switch categoryID {
	case 1: // IT Support
		g.generateITSupportTicket(ticket, categories)
	case 2: // HR Request
		g.generateHRRequestTicket(ticket, categories)
	case 3: // Facilities
		g.generateFacilitiesTicket(ticket, categories)
	case 4: // Finance
		g.generateFinanceTicket(ticket, categories)
	case 5: // General Request
		g.generateGeneralRequestTicket(ticket, categories)
	default:
		// Default category with random fields from all groups
		g.generateDefaultTicket(ticket, categories)
	}

	return ticket, nil
}

// generateBaseColumns creates the core ticket fields
func (g *CategorizedTicketGenerator) generateBaseColumns(ticket map[string]interface{}, categoryID int64) {
	// Start with all static fields from the main generator
	staticFields := g.Generator.generateAllStaticFields()

	// Copy all static fields to the ticket
	for key, value := range staticFields {
		ticket[key] = value
	}

	// Override specific fields for categorized generation
	now := time.Now().UnixMilli()
	ticketID := fmt.Sprintf("TKT-%d-%d", categoryID, now)

	ticket["id"] = ticketID
	ticket["ticket_id"] = ticketID
	ticket["name"] = g.generateTicketName(categoryID)
	ticket["createdtime"] = now
	ticket["updatedtime"] = now
	ticket["statusid"] = g.generateStatusID()
	ticket["priorityid"] = g.generatePriorityID()
	ticket["subject"] = g.generateSubject(categoryID)
	ticket["categoryid"] = categoryID // Set the specific category ID
}

// generateITSupportTicket creates an IT support ticket with relevant fields
func (g *CategorizedTicketGenerator) generateITSupportTicket(ticket map[string]interface{}, categories FieldCategories) {
	// Override specific fields for IT Support (static fields are already set)
	ticket["description"] = "IT support request for system access and troubleshooting"
	ticket["originaldescription"] = ticket["description"]
	ticket["categoryid"] = int64(1)                       // IT Support category
	ticket["departmentid"] = int64(g.rand.Intn(10) + 1)   // IT departments 1-10
	ticket["locationid"] = int64(g.rand.Intn(50) + 1)     // Locations 1-50
	ticket["groupid"] = int64(g.rand.Intn(5) + 10)        // IT groups 10-14
	ticket["impactid"] = int64(g.rand.Intn(4) + 1)        // Impact 1-4
	ticket["urgencyid"] = int64(g.rand.Intn(4) + 1)       // Urgency 1-4
	ticket["technicianid"] = int64(g.rand.Intn(20) + 100) // IT technicians 100-119
	ticket["supportlevel"] = int32(g.rand.Intn(3) + 1)    // Support level 1-3

	// Time & SLA Management (IT typically has strict SLAs)
	g.generateTimeSLAFields(ticket, true) // strict SLA

	// Lifecycle & Workflow
	ticket["approvalstatus"] = int32(0) // No approval needed for IT
	ticket["requesttype"] = "IT_SUPPORT"
	ticket["sourceid"] = int64(g.rand.Intn(3) + 1) // Email, Portal, Phone
}

// generateHRRequestTicket creates an HR request ticket
func (g *CategorizedTicketGenerator) generateHRRequestTicket(ticket map[string]interface{}, categories FieldCategories) {
	// Override specific fields for HR Request (static fields are already set)
	ticket["description"] = "HR request for employee onboarding and policy information"
	ticket["originaldescription"] = ticket["description"]
	ticket["categoryid"] = int64(2)                       // HR category
	ticket["departmentid"] = int64(g.rand.Intn(10) + 1)   // HR departments 1-10
	ticket["locationid"] = int64(g.rand.Intn(50) + 1)     // Locations 1-50
	ticket["groupid"] = int64(g.rand.Intn(3) + 20)        // HR groups 20-22
	ticket["impactid"] = int64(g.rand.Intn(3) + 1)        // Impact 1-3
	ticket["urgencyid"] = int64(g.rand.Intn(3) + 1)       // Urgency 1-3
	ticket["technicianid"] = int64(g.rand.Intn(10) + 200) // HR staff 200-209
	ticket["supportlevel"] = int32(1)                     // Support level 1

	// Time & SLA Management (HR has moderate SLAs)
	g.generateTimeSLAFields(ticket, false) // moderate SLA

	// Lifecycle & Workflow (HR often requires approval)
	ticket["approvalstatus"] = int32(g.rand.Intn(3))   // 0=none, 1=pending, 2=approved
	ticket["approvaltype"] = int32(g.rand.Intn(2) + 1) // 1-2
	ticket["requesttype"] = "HR_REQUEST"
	ticket["sourceid"] = int64(g.rand.Intn(3) + 1) // Sources 1-3
}

// generateFacilitiesTicket creates a facilities request ticket
func (g *CategorizedTicketGenerator) generateFacilitiesTicket(ticket map[string]interface{}, categories FieldCategories) {
	// Override specific fields for Facilities Request (static fields are already set)
	ticket["description"] = "Facilities request for office space and equipment"
	ticket["originaldescription"] = ticket["description"]
	ticket["categoryid"] = int64(3)                       // Facilities category
	ticket["departmentid"] = int64(g.rand.Intn(10) + 1)   // Facilities departments 1-10
	ticket["locationid"] = int64(g.rand.Intn(50) + 1)     // Locations 1-50
	ticket["groupid"] = int64(g.rand.Intn(3) + 30)        // Facilities groups 30-32
	ticket["impactid"] = int64(g.rand.Intn(3) + 1)        // Impact 1-3
	ticket["urgencyid"] = int64(g.rand.Intn(3) + 1)       // Urgency 1-3
	ticket["technicianid"] = int64(g.rand.Intn(15) + 300) // Facilities staff 300-314
	ticket["supportlevel"] = int32(1)                     // Support level 1
	ticket["vendorid"] = int64(g.rand.Intn(50) + 1000)    // External vendors 1000-1049

	// Time & SLA Management (Facilities has relaxed SLAs)
	g.generateTimeSLAFields(ticket, false) // relaxed SLA

	// Lifecycle & Workflow
	ticket["approvalstatus"] = int32(g.rand.Intn(3)) // 0-2
	ticket["requesttype"] = "FACILITIES_REQUEST"
	ticket["sourceid"] = int64(g.rand.Intn(3) + 1) // Sources 1-3
}

// generateFinanceTicket creates a finance request ticket
func (g *CategorizedTicketGenerator) generateFinanceTicket(ticket map[string]interface{}, categories FieldCategories) {
	// Override specific fields for Finance Request (static fields are already set)
	ticket["description"] = "Finance request for budget approval and expense processing"
	ticket["originaldescription"] = ticket["description"]
	ticket["categoryid"] = int64(4)                       // Finance category
	ticket["departmentid"] = int64(g.rand.Intn(10) + 1)   // Finance departments 1-10
	ticket["locationid"] = int64(g.rand.Intn(50) + 1)     // Locations 1-50
	ticket["groupid"] = int64(g.rand.Intn(3) + 40)        // Finance groups 40-42
	ticket["impactid"] = int64(g.rand.Intn(4) + 1)        // Impact 1-4
	ticket["urgencyid"] = int64(g.rand.Intn(4) + 1)       // Urgency 1-4
	ticket["technicianid"] = int64(g.rand.Intn(10) + 400) // Finance staff 400-409
	ticket["supportlevel"] = int32(2)                     // Support level 2

	// Time & SLA Management (Finance has strict SLAs for compliance)
	g.generateTimeSLAFields(ticket, true) // strict SLA

	// Lifecycle & Workflow (Finance always requires approval)
	ticket["approvalstatus"] = int32(g.rand.Intn(3)) // 0-2
	ticket["approvaltype"] = int32(2)                // Always requires approval
	ticket["requesttype"] = "FINANCE_REQUEST"
	ticket["sourceid"] = int64(g.rand.Intn(3) + 1) // Sources 1-3
	ticket["viprequest"] = g.rand.Float32() < 0.1  // 10% VIP requests
}

// generateGeneralRequestTicket creates a general request ticket
func (g *CategorizedTicketGenerator) generateGeneralRequestTicket(ticket map[string]interface{}, categories FieldCategories) {
	// Override specific fields for General Request (static fields are already set)
	ticket["description"] = "General request for miscellaneous services"
	ticket["originaldescription"] = ticket["description"]
	ticket["categoryid"] = int64(5)                       // General category
	ticket["departmentid"] = int64(g.rand.Intn(10) + 1)   // General departments 1-10
	ticket["locationid"] = int64(g.rand.Intn(50) + 1)     // Locations 1-50
	ticket["groupid"] = int64(g.rand.Intn(5) + 50)        // General groups 50-54
	ticket["impactid"] = int64(g.rand.Intn(3) + 1)        // Impact 1-3
	ticket["urgencyid"] = int64(g.rand.Intn(3) + 1)       // Urgency 1-3
	ticket["technicianid"] = int64(g.rand.Intn(20) + 500) // General staff 500-519
	ticket["supportlevel"] = int32(1)                     // Support level 1

	// Time & SLA Management (General has moderate SLAs)
	g.generateTimeSLAFields(ticket, false) // moderate SLA

	// Lifecycle & Workflow
	ticket["approvalstatus"] = int32(g.rand.Intn(2)) // 0=none, 1=pending
	ticket["requesttype"] = "GENERAL_REQUEST"
	ticket["sourceid"] = int64(g.rand.Intn(3) + 1) // Sources 1-3
}

// generateDefaultTicket creates a ticket with random fields from all categories
func (g *CategorizedTicketGenerator) generateDefaultTicket(ticket map[string]interface{}, categories FieldCategories) {
	// Add random fields from each category
	g.addRandomFieldsFromCategory(ticket, categories.RequestUserInfo, 0.7)
	g.addRandomFieldsFromCategory(ticket, categories.TimeSLAManagement, 0.5)
	g.addRandomFieldsFromCategory(ticket, categories.LifecycleWorkflow, 0.3)
}

// Helper methods for generating specific field types

// generateTicketName creates a category-specific ticket name
func (g *CategorizedTicketGenerator) generateTicketName(categoryID int64) string {
	prefixes := map[int64]string{
		1: "INC", // IT Incident
		2: "SR",  // Service Request (HR)
		3: "FR",  // Facilities Request
		4: "FR",  // Finance Request
		5: "GR",  // General Request
	}

	prefix, exists := prefixes[categoryID]
	if !exists {
		prefix = "TKT"
	}

	return fmt.Sprintf("%s-%d", prefix, g.rand.Intn(99999)+10000)
}

// generateSubject creates a category-specific subject
func (g *CategorizedTicketGenerator) generateSubject(categoryID int64) string {
	subjects := map[int64][]string{
		1: { // IT Support
			"Network connectivity issue",
			"Software installation request",
			"Password reset required",
			"Hardware malfunction",
			"System access request",
			"Email configuration problem",
			"VPN connection issue",
			"Database access needed",
		},
		2: { // HR
			"Employee onboarding",
			"Policy clarification needed",
			"Benefits enrollment",
			"Time off request",
			"Training request",
			"Performance review",
			"Salary inquiry",
			"Document request",
		},
		3: { // Facilities
			"Office space request",
			"Equipment maintenance",
			"Meeting room booking",
			"Parking space allocation",
			"Building access card",
			"Furniture request",
			"Cleaning service",
			"Security issue",
		},
		4: { // Finance
			"Budget approval request",
			"Expense reimbursement",
			"Invoice processing",
			"Purchase order",
			"Financial report request",
			"Cost center allocation",
			"Vendor payment",
			"Audit documentation",
		},
		5: { // General
			"General inquiry",
			"Information request",
			"Process clarification",
			"Documentation update",
			"System feedback",
			"Suggestion submission",
			"Complaint resolution",
			"Service feedback",
		},
	}

	categorySubjects, exists := subjects[categoryID]
	if !exists {
		categorySubjects = subjects[5] // Default to general
	}

	return categorySubjects[g.rand.Intn(len(categorySubjects))]
}

// generateStatusID creates a realistic status ID
func (g *CategorizedTicketGenerator) generateStatusID() int64 {
	// Status IDs: 1=Open, 2=In Progress, 3=Pending, 4=Resolved, 5=Closed
	weights := []float32{0.3, 0.25, 0.15, 0.2, 0.1} // Distribution
	return int64(g.weightedRandom(weights) + 1)
}

// generatePriorityID creates a realistic priority ID
func (g *CategorizedTicketGenerator) generatePriorityID() int64 {
	// Priority IDs: 1=Low, 2=Medium, 3=High, 4=Critical
	weights := []float32{0.4, 0.35, 0.2, 0.05} // Distribution
	return int64(g.weightedRandom(weights) + 1)
}

// generateTimeSLAFields creates time and SLA related fields
func (g *CategorizedTicketGenerator) generateTimeSLAFields(ticket map[string]interface{}, strictSLA bool) {
	now := time.Now().UnixMilli()

	// Base SLA times (in milliseconds)
	var responseTime, resolutionTime int64
	if strictSLA {
		responseTime = 4 * 60 * 60 * 1000    // 4 hours
		resolutionTime = 24 * 60 * 60 * 1000 // 24 hours
	} else {
		responseTime = 8 * 60 * 60 * 1000    // 8 hours
		resolutionTime = 72 * 60 * 60 * 1000 // 72 hours
	}

	// Add some randomness
	responseTime += int64(g.rand.Intn(int(responseTime/2))) - responseTime/4
	resolutionTime += int64(g.rand.Intn(int(resolutionTime/2))) - resolutionTime/4

	ticket["dueby"] = now + resolutionTime
	ticket["responsedue"] = now + responseTime
	ticket["duetimemanuallyupdated"] = g.rand.Float32() < 0.1 // 10% manually updated

	// SLA levels and violations
	ticket["responseduelevel"] = g.rand.Intn(3) + 1
	ticket["responsedueviolated"] = g.rand.Float32() < 0.05 // 5% violated

	// Working time calculations
	workingTime := int64(g.rand.Intn(int(resolutionTime / 3)))
	ticket["totalworkingtime"] = workingTime
	ticket["totalresolutiontime"] = workingTime + int64(g.rand.Intn(int(workingTime/2)))
	ticket["totalslapausetime"] = int64(g.rand.Intn(int(workingTime / 10)))
	ticket["totalonholdduration"] = int64(g.rand.Intn(int(workingTime / 5)))

	// OLA fields (Operational Level Agreement)
	if g.rand.Float32() < 0.3 { // 30% have OLA
		ticket["oladueby"] = now + resolutionTime/2
		ticket["olaviolated"] = g.rand.Float32() < 0.03 // 3% violated
		ticket["olaescalationtime"] = now + resolutionTime/4
		ticket["oladuelevel"] = g.rand.Intn(3) + 1
	}

	// UC fields (Underpinning Contract)
	if g.rand.Float32() < 0.2 { // 20% have UC
		ticket["ucdueby"] = now + resolutionTime*2
		ticket["ucduelevel"] = g.rand.Intn(3) + 1
		ticket["ucescalationtime"] = now + resolutionTime
		ticket["ucviolated"] = g.rand.Float32() < 0.02 // 2% violated
		ticket["totalucworkingtime"] = workingTime
		ticket["totalucresolutiontime"] = ticket["totalresolutiontime"]
	}
}

// addRandomFieldsFromCategory adds random fields from a category with given probability
func (g *CategorizedTicketGenerator) addRandomFieldsFromCategory(ticket map[string]interface{}, fields []string, probability float32) {
	for _, field := range fields {
		if g.rand.Float32() < probability {
			ticket[field] = g.generateFieldValue(field)
		}
	}
}

// generateFieldValue creates a realistic value for a given field
func (g *CategorizedTicketGenerator) generateFieldValue(fieldName string) interface{} {
	switch fieldName {
	// Boolean fields
	case "duetimemanuallyupdated", "responsedueviolated", "removed", "reopened", "olaviolated", "ucviolated", "migrated", "spam", "viprequest":
		return g.rand.Float32() < 0.1 // 10% true for most boolean fields

	// ID fields
	case "departmentid", "locationid", "groupid", "impactid", "urgencyid", "technicianid", "vendorid":
		return g.rand.Intn(100) + 1
	case "removedbyid", "resolvedby", "closedby":
		return g.rand.Intn(1000) + 1000
	case "templateid", "servicecatalogid", "transitionmodelid":
		return g.rand.Intn(50) + 1

	// Time fields
	case "firstresponsetime", "resolutionescalationtime", "responsetimeescalationtime":
		return time.Now().UnixMilli() + int64(g.rand.Intn(86400000)) // Within 24 hours
	case "removedtime", "lastopenedtime", "lastresolvedtime", "lastclosedtime", "statuschangedtime", "groupchangedtime":
		return time.Now().UnixMilli() - int64(g.rand.Intn(86400000)) // Within last 24 hours
	case "lastviolationtime", "lastolaviolationtime", "lastucviolationtime":
		if g.rand.Float32() < 0.1 { // 10% have violations
			return time.Now().UnixMilli() - int64(g.rand.Intn(604800000)) // Within last week
		}
		return int64(0)

	// Level fields
	case "responseduelevel", "supportlevel", "oladuelevel", "ucduelevel":
		return g.rand.Intn(3) + 1

	// Status fields
	case "approvalstatus", "approvaltype":
		return g.rand.Intn(3)

	// String fields
	case "description", "originaldescription":
		descriptions := []string{
			"System experiencing intermittent connectivity issues",
			"User unable to access required application",
			"Request for additional software installation",
			"Hardware replacement needed for workstation",
			"Network printer not responding to print jobs",
			"Email synchronization problems with mobile device",
			"Database query performance degradation observed",
			"Security access review and update required",
		}
		return descriptions[g.rand.Intn(len(descriptions))]
	case "requesttype":
		types := []string{"INCIDENT", "SERVICE_REQUEST", "CHANGE_REQUEST", "PROBLEM", "TASK"}
		return types[g.rand.Intn(len(types))]

	// Numeric fields with specific ranges
	case "totalresolutiontime", "totalworkingtime", "totalucworkingtime", "totalucresolutiontime":
		return int64(g.rand.Intn(86400000)) // Up to 24 hours in milliseconds
	case "totalslapausetime", "totalonholdduration":
		return int64(g.rand.Intn(3600000)) // Up to 1 hour in milliseconds
	case "sourceid":
		return g.rand.Intn(5) + 1 // 1-5 for different sources

	default:
		// Default to random integer for unknown fields
		return g.rand.Intn(1000)
	}
}

// weightedRandom returns an index based on weighted probabilities
func (g *CategorizedTicketGenerator) weightedRandom(weights []float32) int {
	total := float32(0)
	for _, weight := range weights {
		total += weight
	}

	r := g.rand.Float32() * total
	cumulative := float32(0)

	for i, weight := range weights {
		cumulative += weight
		if r <= cumulative {
			return i
		}
	}

	return len(weights) - 1
}
