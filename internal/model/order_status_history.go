package model

import (
	"time"

	"github.com/google/uuid"
)

// OrderStatusHistory is one recorded transition in an order's lifecycle.
//
// This is the audit trail behind "Bagaimana pesanan ini sampai ke status
// sekarang?", which the schema could not answer before: orders carries only the
// current status, and OrderEvent -- despite the name -- records an event
// discount applied to the basket, not a state change.
//
// Rows are append-only. Nothing updates or deletes one, and there is no
// DeletedAt: an audit trail that can be quietly rewritten is not an audit trail.
type OrderStatusHistory struct {
	ID      uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	OrderID uuid.UUID `gorm:"type:uuid;index" json:"order_id"`
	Order   Order     `gorm:"foreignKey:OrderID;OnDelete:CASCADE" json:"-"`

	// FromStatus is empty on the row that records the order being created.
	FromStatus OrderStatus `gorm:"type:varchar(255)" json:"from_status"`
	ToStatus   OrderStatus `gorm:"type:varchar(255);not null" json:"to_status"`

	// Reason is mandatory for the transitions that destroy value (CANCELED,
	// STORE_REJECTED) and optional elsewhere. The service enforces that, not the
	// column: a NOT NULL here would also reject the happy-path rows.
	Reason string `gorm:"type:text" json:"reason"`

	// ActorID is nil when the transition was not made by a person -- a payment
	// callback or a scheduled job. Storing nil is honest; attributing a machine
	// transition to whoever happened to be logged in is not.
	ActorID *uuid.UUID `gorm:"type:uuid;index" json:"actor_id"`
	Actor   *User      `gorm:"foreignKey:ActorID" json:"-"`

	CreatedAt time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}
