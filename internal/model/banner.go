package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Banner struct {
	ID    uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Photo string    `gorm:"type:varchar(255)"`
	// PublicID is the storage provider's own handle. Without it a replaced or
	// deleted record leaks its asset: the URL alone cannot address the file
	// for deletion, and re-deriving an id by parsing the URL is exactly the
	// fragile string surgery this rewrite avoids.
	PhotoPublicID string    `gorm:"type:varchar(255)" json:"-"`
	Name          string    `gorm:"type:varchar(255);not null"`
	Description   string    `gorm:"type:text"`
	Status        int       `gorm:"type:int;not null"`
	UserID        uuid.UUID `gorm:"type:uuid"`
	User          User      `gorm:"foreignKey:UserID;OnDelete:CASCADE"`

	CreatedAt time.Time      `gorm:"autoCreateTime"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}
