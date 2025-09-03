package storage

import (
	ticketpb "github.com/platform/ticket-svc/pb/proto"
)

// TicketStorage defines the interface for ticket storage operations
type TicketStorage interface {
	CreateTicket(tenant string, ticketData *ticketpb.TicketData) error
	GetTicket(tenant, id string) (*ticketpb.TicketData, bool)
	UpdateTicket(tenant string, ticketData *ticketpb.TicketData) bool
	DeleteTicket(tenant, id string) (*ticketpb.TicketData, bool)
	ListTickets(tenant string) ([]*ticketpb.TicketData, error)
	Close() error
}
