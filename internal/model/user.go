package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User is an account. Only Name and Role are always present: a customer who
// signed up with a phone number has no email or username, and one who signed in
// with Google has no password.
//
// Username, Email, Phone and Password are pointers so "not provided" is stored
// as NULL. The previous schema used non-null strings with unique indexes, which
// meant the second account without a phone number collided with the first on
// the empty string.
type User struct {
	ID       uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Name     string    `gorm:"type:varchar(255);not null"`
	Username *string   `gorm:"type:varchar(255);uniqueIndex"`
	Email    *string   `gorm:"type:varchar(255);uniqueIndex"`
	Phone    *string   `gorm:"type:varchar(32);uniqueIndex"`
	Password *string   `gorm:"type:varchar(255)"`

	EmailVerifiedAt *time.Time `gorm:"index"`
	PhoneVerifiedAt *time.Time `gorm:"index"`

	Gender      string     `gorm:"type:varchar(100)"`
	DateOfBirth *time.Time `gorm:"type:date"`
	Photo       string     `gorm:"type:varchar(255)"`

	RoleID uuid.UUID `gorm:"type:uuid;index"`
	Role   Role      `gorm:"foreignKey:RoleID"`
	Status int       `gorm:"type:int;not null;default:1"`

	Identities []UserIdentity `gorm:"foreignKey:UserID"`

	CreatedAt time.Time      `gorm:"autoCreateTime"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}
