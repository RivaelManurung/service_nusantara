package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Shop struct {
	ID    uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Name  string    `gorm:"type:varchar(255);not null"`
	Cover string    `gorm:"type:varchar(255);not null"`
	// PublicID is the storage provider's own handle. Without it a replaced or
	// deleted record leaks its asset: the URL alone cannot address the file
	// for deletion, and re-deriving an id by parsing the URL is exactly the
	// fragile string surgery this rewrite avoids.
	CoverPublicID string         `gorm:"type:varchar(255)" json:"-"`
	Description   string         `gorm:"type:text"`
	FullAddress   string         `gorm:"type:varchar(255)"`
	Lat           float64        `gorm:"type:decimal(10,8)"`
	Lng           float64        `gorm:"type:decimal(11,8)"`
	Status        int            `gorm:"type:int;default:0"`
	CreatedBy     uuid.UUID      `gorm:"type:uuid" json:"created_by"`
	CreatedAt     time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"deleted_at"`

	Creator User `gorm:"foreignKey:CreatedBy;OnDelete:CASCADE"`
}

type ShopImage struct {
	ID        uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ShopID    uuid.UUID      `gorm:"type:uuid;not null"`
	ImageID   uuid.UUID      `gorm:"type:uuid;not null"`
	Altext    string         `gorm:"type:varchar(255);not null"`
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`

	Shop  Shop  `gorm:"foreignKey:ShopID;OnDelete:CASCADE;OnUpdate:CASCADE"`
	Image Image `gorm:"foreignKey:ImageID;OnDelete:CASCADE;OnUpdate:CASCADE"`
}
