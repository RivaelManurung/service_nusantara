package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DevicePlatform is the store the registration came from. It decides which
// half of the FCM payload matters (`android` or `apns`) and lets an operator
// see what a broadcast actually reached.
type DevicePlatform string

const (
	DevicePlatformAndroid DevicePlatform = "ANDROID"
	DevicePlatformIOS     DevicePlatform = "IOS"
	DevicePlatformWeb     DevicePlatform = "WEB"
)

// IsValidDevicePlatform guards a client supplied value, so an arbitrary string
// never reaches the column.
func IsValidDevicePlatform(platform string) bool {
	switch DevicePlatform(platform) {
	case DevicePlatformAndroid, DevicePlatformIOS, DevicePlatformWeb:
		return true
	}
	return false
}

// DeviceToken is one FCM registration: a single installation of the app on a
// single device, owned by the account that was signed in when it registered.
//
// Token carries the unique index rather than (UserID, Token). FCM issues one
// registration per installation, and that installation follows the phone, not
// the account: when a second person signs in on the same device, FCM keeps
// handing out the same token. A per-user unique index would then leave two
// rows with the same token, and the previous owner would keep receiving the
// new owner's notifications. Keying on the token means a re-registration moves
// the row to whoever holds the device now.
type DeviceToken struct {
	ID     uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID uuid.UUID `gorm:"type:uuid;index;not null"`
	User   User      `gorm:"foreignKey:UserID"`
	// Token is FCM's registration id. It is text rather than varchar(255):
	// Google documents no upper bound and has grown it before.
	Token    string         `gorm:"type:text;uniqueIndex;not null"`
	Platform DevicePlatform `gorm:"type:varchar(20);index;not null"`
	// AppVersion is kept for support: "only 2.1.0 stopped receiving promos" is
	// a question this column answers and nothing else can.
	AppVersion string `gorm:"type:varchar(50)"`
	// LastSeenAt is refreshed on every re-registration. FCM tokens go stale
	// silently, so a sweep of registrations untouched for months is the only
	// way to keep this table from growing without bound.
	LastSeenAt time.Time `gorm:"index;not null"`

	CreatedAt time.Time      `gorm:"autoCreateTime"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (DeviceToken) TableName() string { return "device_tokens" }
