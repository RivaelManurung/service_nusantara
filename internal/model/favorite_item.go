package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type FavoriteItem struct {
	ID         uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	FavoriteID uuid.UUID `gorm:"type:uuid"`
	ProductID  uuid.UUID `gorm:"type:uuid"`
	Selected   bool      `gorm:"type:boolean"`

	Favorite Favorite `gorm:"foreignKey:FavoriteID;OnDelete:CASCADE"`
	Product  Product  `gorm:"foreignKey:ProductID;OnDelete:CASCADE"`

	CreatedAt time.Time      `gorm:"autoCreateTime"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}
