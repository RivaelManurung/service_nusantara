package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ShopCashier struct {
	ID         uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	ShopID     uuid.UUID `gorm:"type:uuid;index;not null"`
	CashierID  uuid.UUID `gorm:"type:uuid;index;not null"`
	AssignedAt time.Time `gorm:"default:now()"`

	Shop Shop `gorm:"foreignKey:ShopID;OnDelete:CASCADE;OnUpdate:CASCADE"`
	User User `gorm:"foreignKey:CashierID;OnDelete:CASCADE;OnUpdate:CASCADE"`

	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
