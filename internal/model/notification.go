package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NotificationChannel matches the two inbox tabs on the customer app
// (Transaksi / Promo).
type NotificationChannel string

// NotificationType is the visual tone the client uses to pick an icon and
// colour.
type NotificationType string

const (
	NotificationChannelTransaksi NotificationChannel = "TRANSAKSI"
	NotificationChannelPromo     NotificationChannel = "PROMO"

	NotificationTypeInfo    NotificationType = "INFO"
	NotificationTypeSuccess NotificationType = "SUCCESS"
	NotificationTypeWarning NotificationType = "WARNING"
	NotificationTypeError   NotificationType = "ERROR"
	NotificationTypePromo   NotificationType = "PROMO"
)

// Deep-link targets understood by the mobile client.
const (
	NotificationTargetOrder   = "ORDER"
	NotificationTargetVoucher = "VOUCHER"
	NotificationTargetPoint   = "POINT"
	NotificationTargetNone    = ""
)

// IsValidNotificationChannel guards a user supplied filter, so an arbitrary
// string can never reach the WHERE clause as if it were a known tab.
func IsValidNotificationChannel(channel string) bool {
	switch NotificationChannel(channel) {
	case NotificationChannelTransaksi, NotificationChannelPromo:
		return true
	}
	return false
}

// Notification is one row of a customer's inbox. It is always owned by exactly
// one user: every read and every write is scoped by UserID, so one account can
// never see or acknowledge another's messages.
type Notification struct {
	ID          uuid.UUID           `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID      uuid.UUID           `gorm:"type:uuid;index:idx_notifications_user_channel,priority:1;not null"`
	User        User                `gorm:"foreignKey:UserID"`
	Channel     NotificationChannel `gorm:"type:varchar(20);index:idx_notifications_user_channel,priority:2;not null"`
	Title       string              `gorm:"type:varchar(255);not null"`
	Body        string              `gorm:"type:text"`
	Type        NotificationType    `gorm:"type:varchar(30);default:'INFO'"`
	ReferenceID *uuid.UUID          `gorm:"type:uuid;index"`
	TargetType  string              `gorm:"type:varchar(30)"`
	TargetRoute string              `gorm:"type:varchar(255)"`
	ReadAt      *time.Time          `gorm:"index"`

	CreatedAt time.Time      `gorm:"autoCreateTime;index"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (Notification) TableName() string { return "notifications" }

// IsRead reports whether the customer has already opened the message.
func (n Notification) IsRead() bool { return n.ReadAt != nil }
