package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Cart struct {
	ID        uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID    uuid.UUID      `gorm:"type:uuid"`
	User      User           `gorm:"foreignKey:UserID;OnDelete:CASCADE"`
	ShopID    uuid.UUID      `gorm:"type:uuid"`
	Shop      Shop           `gorm:"foreignKey:ShopID;constraint:OnDelete:CASCADE;"`
	Status    int            `gorm:"type:int;default:0"`
	CreatedAt time.Time      `gorm:"autoCreateTime"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}
