package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type EventType string

const (
	// EventType values for TypeEvent
	EventTypeBundle EventType = "BUNDLE"
	EventTypeDiskon EventType = "DISKON"
)

type Event struct {
	ID        uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Name      string         `gorm:"type:varchar(255);not null"`
	TypeEvent EventType      `gorm:"type:varchar(100)"`
	StartDate time.Time      `gorm:"type:timestamp;index"`
	EndDate   time.Time      `gorm:"type:timestamp;index"`
	Cover     string         `gorm:"type:varchar(255)"`
	Status    int            `gorm:"type:int;default:0"`
	CreatedBy uuid.UUID      `gorm:"type:uuid"`
	User      User           `gorm:"foreignKey:CreatedBy"`
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
