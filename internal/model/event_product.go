package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type EventProduct struct {
	ID              uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	EventID         uuid.UUID      `gorm:"type:uuid;not null"`
	Event           Event          `gorm:"foreignKey:EventID;OnDelete:CASCADE;OnUpdate:CASCADE"`
	ProductID       uuid.UUID      `gorm:"type:uuid;not null"`
	Product         Product        `gorm:"foreignKey:ProductID;OnDelete:CASCADE;OnUpdate:CASCADE"`
	DiscountPercent int            `gorm:"type:int"`
	DiscountAmount  float64        `gorm:"type:float"`
	CreatedAt       time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
