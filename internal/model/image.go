package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Image struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	ImagePath string    `gorm:"type:varchar(255);index" json:"image_path"`
	// PublicID is the storage provider's own handle. Without it a replaced or
	// deleted record leaks its asset: the URL alone cannot address the file
	// for deletion, and re-deriving an id by parsing the URL is exactly the
	// fragile string surgery this rewrite avoids.
	PublicID  string         `gorm:"type:varchar(255);index" json:"-"`
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
