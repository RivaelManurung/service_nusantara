package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type TypeProduct struct {
	ID     uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Image  string    `gorm:"type:varchar(255)"`
	Name   string    `gorm:"type:varchar(255);not null"`
	Status int       `gorm:"type:int;not null"`
	UserID uuid.UUID `gorm:"type:uuid"`
	User   User      `gorm:"foreignKey:UserID"`

	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
