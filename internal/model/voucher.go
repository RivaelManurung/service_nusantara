package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Voucher struct {
	ID              uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Code            string         `gorm:"type:varchar(50);index" json:"code"`
	DiscountAmount  int            `gorm:"type:int" json:"discount_amount"`
	DiscountPercent int            `gorm:"type:int" json:"discount_percent"`
	MinimumSpend    int            `gorm:"type:int" json:"minimum_spend"`
	PointCost       int            `gorm:"type:int" json:"point_cost"`
	StartDate       time.Time      `gorm:"type:timestamp;index" json:"start_date"`
	EndDate         time.Time      `gorm:"type:timestamp;index" json:"end_date"`
	Quota           int            `gorm:"type:int" json:"quota"`
	ClaimedCount    int            `gorm:"type:int;default:0" json:"claimed_count"`
	Description     string         `gorm:"type:text" json:"description"`
	DiscountType    string         `gorm:"type:varchar(100)" json:"discount_type"`
	Status          int            `gorm:"type:int;default:0" json:"status"`
	CreatedBy       uuid.UUID      `gorm:"type:uuid"`
	User            User           `gorm:"foreignKey:CreatedBy"`
	CreatedAt       time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
