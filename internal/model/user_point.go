package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type UserPoint struct {
	ID          uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID      uuid.UUID      `gorm:"type:uuid;uniqueIndex"`
	User        User           `gorm:"foreignKey:UserID;OnDelete:CASCADE"`
	TotalPoints int            `gorm:"type:int" json:"total_points"`
	CreatedAt   time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
