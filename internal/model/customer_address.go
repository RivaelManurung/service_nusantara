package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CustomerAddress struct {
	ID          uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	UserID      uuid.UUID `gorm:"type:uuid;not null"`
	User        User      `gorm:"foreignKey:UserID;OnDelete:CASCADE;OnUpdate:CASCADE"`
	Label       string    `gorm:"type:varchar(100);not null"`
	AddressText string    `gorm:"type:text;not null"`
	Lat         float64   `gorm:"not null"`
	Lng         float64   `gorm:"not null"`
	IsDefault   bool      `gorm:"default:false"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}
