package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type UserVoucher struct {
	ID         uuid.UUID         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID     uuid.UUID         `gorm:"type:uuid;index" json:"user_id"`
	User       User              `gorm:"foreignKey:UserID;OnDelete:CASCADE"`
	VoucherID  uuid.UUID         `gorm:"type:uuid;index" json:"voucher_id"`
	Voucher    Voucher           `gorm:"foreignKey:VoucherID;OnDelete:CASCADE"`
	DetailID   uuid.UUID         `gorm:"type:uuid;index" json:"detail_id"` // Relasi ke snapshot
	Detail     UserVoucherDetail `gorm:"foreignKey:DetailID;OnDelete:CASCADE"`
	IsUsed     bool              `gorm:"default:false" json:"is_used"`
	RedeemedAt *time.Time        `gorm:"type:timestamp" json:"redeemed_at"`
	ClaimedAt  time.Time         `gorm:"autoCreateTime" json:"claimed_at"`
	CreatedAt  time.Time         `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time         `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt  gorm.DeletedAt    `gorm:"index" json:"deleted_at"`

	_ struct{} `gorm:"uniqueIndex:user_voucher_unique,unique;column:unique_idx"`
}

type UserVoucherDetail struct {
	ID                uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	VoucherCode       string         `gorm:"type:varchar(50);not null" json:"voucher_code"`
	DiscountType      string         `gorm:"type:varchar(100);not null" json:"discount_type"`
	DiscountAmount    int            `gorm:"not null" json:"discount_amount"`
	DiscountPercent   int            `gorm:"not null" json:"discount_percent"`
	MinPurchaseAmount int            `gorm:"not null" json:"min_purchase_amount"`
	ValidFrom         time.Time      `json:"valid_from"`
	ValidUntil        time.Time      `json:"valid_until"`
	Description       string         `gorm:"type:text" json:"description"`
	CreatedAt         time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt         gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
