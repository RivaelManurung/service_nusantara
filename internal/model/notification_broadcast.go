package model

import (
	"time"

	"github.com/google/uuid"
)

// Audience modes a broadcast can be addressed to.
const (
	AudienceAll     = "ALL"
	AudienceUsers   = "USERS"
	AudienceSegment = "SEGMENT"
)

// NotificationBroadcast records one send, once.
//
// The notifications table cannot answer "what have we sent?": it holds one row
// per recipient, so a promo to four hundred customers is four hundred rows that
// differ only by user_id. Reconstructing the send from them means grouping by
// title and a timestamp window, which is guesswork -- two operators sending the
// same title a minute apart would merge into one, and a personalised body would
// split one send into many.
//
// So the send is its own record. It also carries what the recipient rows can
// never know: how many accounts the audience resolved to, whether push was
// requested, how many devices actually took it, and who pressed the button.
//
// Append-only. A send is a past event; editing the record would not un-send it.
type NotificationBroadcast struct {
	ID uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`

	Title       string              `gorm:"type:varchar(255);not null" json:"title"`
	Body        string              `gorm:"type:text" json:"body"`
	Channel     NotificationChannel `gorm:"type:varchar(20);not null" json:"channel"`
	Type        NotificationType    `gorm:"type:varchar(30)" json:"type"`
	TargetType  string              `gorm:"type:varchar(30)" json:"target_type"`
	TargetRoute string              `gorm:"type:varchar(255)" json:"target_route"`

	// AudienceMode is ALL, USERS or SEGMENT -- how the operator described who
	// should receive it, kept alongside the resolved count because "semua
	// pelanggan" and "412 orang" answer different questions later.
	AudienceMode string `gorm:"type:varchar(20);not null" json:"audience_mode"`

	// RecipientCount is how many accounts the audience resolved to, and
	// SavedCount how many inbox rows were actually written. They differ only
	// when a write partly failed, which is exactly when someone needs to know.
	RecipientCount int   `gorm:"type:int;not null" json:"recipient_count"`
	SavedCount     int64 `gorm:"type:bigint;not null" json:"saved_count"`

	// The push half. Requested is the operator's choice; the rest is what
	// happened. A promo saved for four hundred customers and delivered to
	// ninety phones is a normal outcome, and one number would hide it.
	PushRequested bool   `gorm:"not null;default:false" json:"push_requested"`
	PushEnabled   bool   `gorm:"not null;default:false" json:"push_enabled"`
	PushSent      int    `gorm:"type:int;not null;default:0" json:"push_sent"`
	PushFailed    int    `gorm:"type:int;not null;default:0" json:"push_failed"`
	PushError     string `gorm:"type:text" json:"push_error"`

	// ActorID is the member of staff who sent it. Not a pointer: nothing sends
	// a broadcast except a person through the admin API.
	ActorID uuid.UUID `gorm:"type:uuid;index;not null" json:"actor_id"`
	Actor   User      `gorm:"foreignKey:ActorID" json:"-"`

	CreatedAt time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

func (NotificationBroadcast) TableName() string { return "notification_broadcasts" }
