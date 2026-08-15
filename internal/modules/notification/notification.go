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

// Repository is the persistence port. Every read and every acknowledgement
// carries the owner's id; only the broadcast half, which is written by staff
// rather than by the owner, does not.
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

	// Recipients resolves an audience to the accounts it names.
	//
	// It returns ids rather than writing the rows itself, because the same
	// list is needed twice: once to fill the inboxes and once to find the
	// devices to wake.
	Recipients(ctx context.Context, audience Audience) ([]uuid.UUID, error)
	// CreateMany writes one inbox row per recipient and reports how many
	// landed.
	CreateMany(ctx context.Context, messages []NewNotification) (int64, error)
	// SearchCustomers backs the recipient picker in the back office.
	SearchCustomers(ctx context.Context, query CustomerQuery) ([]Customer, int64, error)

	// RecordBroadcast files one send as a single row, so the back office can
	// list what has been sent. The notifications table cannot answer that: it
	// holds one row per recipient, and regrouping them by title and timestamp
	// is guesswork.
	RecordBroadcast(ctx context.Context, broadcast Broadcast) (Broadcast, error)
	// ListBroadcasts returns the send history, newest first.
	ListBroadcasts(ctx context.Context, page, perPage int) ([]Broadcast, int64, error)
}

// Broadcast is one recorded send, as the history screen shows it.
//
// The field names are the contract with
// web_nusantara/src/features/notification/types.ts.
type Broadcast struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Channel     string    `json:"channel"`
	Type        string    `json:"type"`
	TargetType  string    `json:"target_type"`
	TargetRoute string    `json:"target_route"`

	AudienceMode   string `json:"audience_mode"`
	RecipientCount int    `json:"recipient_count"`
	SavedCount     int64  `json:"saved_count"`

	PushRequested bool   `json:"push_requested"`
	PushEnabled   bool   `json:"push_enabled"`
	PushSent      int    `json:"push_sent"`
	PushFailed    int    `json:"push_failed"`
	PushError     string `json:"push_error"`

	ActorID   uuid.UUID `json:"actor_id"`
	ActorName string    `json:"actor_name"`
	CreatedAt time.Time `json:"created_at"`
}

// CustomerQuery is one page of the recipient picker.
type CustomerQuery struct {
	Search  string
	Page    int
	PerPage int
}

// Customer is one candidate recipient.
//
// It carries the minimum an operator needs to recognise an account -- a name
// and one way to tell two people of the same name apart. This is deliberately
// not the user module's profile: a screen for choosing who receives a promo
// has no business exposing verification flags or sign-in methods.
type Customer struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email,omitempty"`
	Phone string    `json:"phone,omitempty"`
}

// IsValidChannel guards a user supplied filter.
func IsValidChannel(channel string) bool {
	return channel == ChannelTransaksi || channel == ChannelPromo
}

// --- broadcast ---------------------------------------------------------

// How a broadcast names its recipients.
const (
	// AudienceAll is every active customer.
	AudienceAll = "ALL"
	// AudienceUsers is a list picked by hand in the back office.
	AudienceUsers = "USERS"
	// AudienceSegment is a filter evaluated at send time.
	AudienceSegment = "SEGMENT"
)

// IsValidAudienceMode guards a client supplied mode.
func IsValidAudienceMode(mode string) bool {
	switch mode {
	case AudienceAll, AudienceUsers, AudienceSegment:
		return true
	}
	return false
}

// Segment is the filter behind AudienceSegment.
//
// Every field is optional and they combine with AND. An empty segment is
// rejected rather than treated as "everybody": a filter that silently widens
// to the whole customer base is how a test promo reaches fifty thousand
// phones.
type Segment struct {
	// RoleName restricts to one role, matched case-insensitively.
	RoleName string
	// HasOrdered, when true, keeps only accounts with at least one order.
	HasOrdered bool
	// RegisteredFrom and RegisteredTo bound users.created_at.
	RegisteredFrom *time.Time
	RegisteredTo   *time.Time
}

// IsEmpty reports a segment that would select everybody.
func (s Segment) IsEmpty() bool {
	return s.RoleName == "" && !s.HasOrdered && s.RegisteredFrom == nil && s.RegisteredTo == nil
}

// Audience is who a broadcast is for.
type Audience struct {
	Mode string
	// UserIDs is used by AudienceUsers only.
	UserIDs []uuid.UUID
	// Segment is used by AudienceSegment only.
	Segment Segment
}

// SendRequest is one broadcast, as the back office describes it.
type SendRequest struct {
	Audience    Audience
	Channel     string
	Title       string
	Body        string
	Type        string
	TargetType  string
	TargetRoute string
	ReferenceID *uuid.UUID
	// Push is the operator's choice to also wake the devices. Saving to the
	// inbox without pushing is a real use -- "it is there when they next open
	// the app" -- so it is a decision rather than an implicit consequence.
	Push bool
	// ActorID is the member of staff sending it, taken from the token rather
	// than the body. It is what makes the send history attributable.
	ActorID uuid.UUID
}

// NewNotification is one inbox row waiting to be written.
type NewNotification struct {
	UserID      uuid.UUID
	Channel     string
	Title       string
	Body        string
	Type        string
	TargetType  string
	TargetRoute string
	ReferenceID *uuid.UUID
}

// SendResult is what the back office is told afterwards.
//
// The inbox count and the push counts are separate on purpose: a promo saved
// for four hundred customers but delivered to ninety phones is a normal
// outcome, and reporting one number would hide it.
type SendResult struct {
	// Recipients is how many accounts the audience resolved to.
	Recipients int `json:"recipients"`
	// Saved is how many inbox rows were written.
	Saved int64 `json:"saved"`
	// Devices is how many registrations those accounts have.
	Devices int `json:"devices"`
	// PushSent and PushFailed are per device, not per account.
	PushSent   int `json:"push_sent"`
	PushFailed int `json:"push_failed"`
	// PushEnabled is false when the deployment has no FCM credentials. The UI
	// then says "saved to the inbox, push is off" rather than claiming a
	// delivery that never happened.
	PushEnabled bool `json:"push_enabled"`
	// PushError explains a delivery that failed as a whole, in the operator's
	// words rather than the log's.
	PushError string `json:"push_error,omitempty"`
}

// Device is one address a broadcast can be delivered to.
type Device struct {
	UserID uuid.UUID
	Token  string
}

// DeviceRegistry is the port onto the device_tokens table.
//
// It is declared here, next to its consumer, and satisfied by an adapter in
// the wiring layer: internal/modules/devicetoken owns that table, and the two
// modules deliberately do not import each other.
type DeviceRegistry interface {
	TokensFor(ctx context.Context, userIDs []uuid.UUID) ([]Device, error)
	// DeleteTokens drops registrations the provider reported as gone.
	DeleteTokens(ctx context.Context, tokens []string) error
}
