package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ShopProducts struct {
	ID         uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ShopID     uuid.UUID      `gorm:"type:uuid;index;not null"`
	ProductID  uuid.UUID      `gorm:"type:uuid;not null"`
	Price      float64        `gorm:"type:numeric(12,2)"`
	Stock      int            `gorm:"type:int;default:0"`
	Status     int            `gorm:"type:int;default:0"`
	AssignedAt time.Time      `gorm:"default:now()"`
	CreatedAt  time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"deleted_at"`

	Shop    Shop    `gorm:"foreignKey:ShopID;OnDelete:CASCADE;OnUpdate:CASCADE"`
	Product Product `gorm:"foreignKey:ProductID;OnDelete:CASCADE;OnUpdate:CASCADE"`
}
