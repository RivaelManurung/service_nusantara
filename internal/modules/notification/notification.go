// Package notification serves the customer's inbox: listing messages, counting
// the unread ones and marking them read.
//
// It follows the shape of internal/modules/typeproduct, with one rule on top:
// a notification belongs to exactly one account, so every method takes the
// owner's id and every query is scoped by it. The owner is taken from the
// verified token in the handler and never from the request, which is what makes
// it impossible to read or acknowledge somebody else's inbox.
package notification

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches for this owner. Callers compare
// against this rather than gorm.ErrRecordNotFound, so the service layer never
// imports GORM.
var ErrNotFound = errors.New("notification not found")

// Channels are the inbox tabs on the customer app.
const (
	ChannelTransaksi = "TRANSAKSI"
	ChannelPromo     = "PROMO"
)

// Notification is the response shape. The field names are the ones the mobile
// client already reads, so they are fixed by contract.
type Notification struct {
	ID          uuid.UUID  `json:"id"`
	Channel     string     `json:"channel"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Type        string     `json:"type"`
	ReferenceID *uuid.UUID `json:"reference_id"`
	TargetType  string     `json:"target_type"`
	TargetRoute string     `json:"target_route"`
	IsRead      bool       `json:"is_read"`
	ReadAt      *time.Time `json:"read_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// UnreadCount powers the badge on each inbox tab.
type UnreadCount struct {
	Transaksi int `json:"transaksi"`
	Promo     int `json:"promo"`
	Total     int `json:"total"`
}

// ListQuery is a page of one user's inbox. UserID is part of the query rather
// than a filter applied later, so a query object without an owner cannot be
// constructed by accident.
type ListQuery struct {
	UserID  uuid.UUID
	Channel string
	Page    int
	PerPage int
}

// Repository is the persistence port. Every method carries the owner's id.
type Repository interface {
	List(ctx context.Context, query ListQuery) ([]Notification, int64, error)
	// UnreadByChannel returns the unread totals keyed by channel name.
	UnreadByChannel(ctx context.Context, userID uuid.UUID) (map[string]int, error)
	// FindByIDForUser returns ErrNotFound when the row does not exist *or*
	// belongs to somebody else -- the two are deliberately indistinguishable to
	// the caller, so the endpoint cannot be used to probe for foreign ids.
	FindByIDForUser(ctx context.Context, id, userID uuid.UUID) (Notification, error)
	MarkRead(ctx context.Context, id, userID uuid.UUID) error
	MarkAllRead(ctx context.Context, userID uuid.UUID, channel string) (int64, error)
}

// IsValidChannel guards a user supplied filter.
func IsValidChannel(channel string) bool {
	return channel == ChannelTransaksi || channel == ChannelPromo
}
