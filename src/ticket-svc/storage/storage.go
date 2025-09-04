package storage

import (
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// SearchCondition represents a search condition with operand, operator, and value
type SearchCondition struct {
	Operand  string      `json:"operand"`  // field name (e.g., "title", "priority", "created_at")
	Operator string      `json:"operator"` // operator (e.g., "eq", "ne", "gt", "lt", "gte", "lte", "contains", "begins_with")
	Value    interface{} `json:"value"`    // value to compare against
}

// SearchRequest represents a search request with conditions and optional field projection
type SearchRequest struct {
	Conditions      []SearchCondition `json:"conditions"`                 // search conditions to apply
	ProjectedFields []string          `json:"projected_fields,omitempty"` // fields to include in results (empty = all fields)
}

// TicketStorage defines the interface for ticket storage operations
type TicketStorage interface {
	CreateTicket(tenant string, ticketData *ticketpb.TicketData) error
	GetTicket(tenant, id string) (*ticketpb.TicketData, bool)
	UpdateTicket(tenant string, ticketData *ticketpb.TicketData) bool
	DeleteTicket(tenant, id string) (*ticketpb.TicketData, bool)
	ListTickets(tenant string) ([]*ticketpb.TicketData, error)
	SearchTickets(tenant string, conditions []SearchCondition) ([]*ticketpb.TicketData, error)
	SearchTicketsWithProjection(tenant string, request SearchRequest) ([]*ticketpb.TicketData, error)
	Close() error
}
