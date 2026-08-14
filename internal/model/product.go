package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Product struct {
	ID            uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name          string         `gorm:"type:varchar(255);index" json:"name"`
	ImageID       uuid.UUID      `gorm:"type:uuid;index" json:"image_id"`
	Image         Image          `gorm:"foreignKey:ImageID" json:"image"`
	Code          string         `gorm:"type:varchar(255);index" json:"code"`
	Price         int            `gorm:"type:int" json:"price"`
	Unit          string         `gorm:"type:varchar(50)" json:"unit"`
	Description   string         `gorm:"type:text" json:"description"`
	Status        int            `gorm:"type:int;default:0" json:"status"`
	TypeProductID uuid.UUID      `gorm:"type:uuid;index" json:"type_product_id"`
	TypeProduct   TypeProduct    `gorm:"foreignKey:TypeProductID;OnDelete:CASCADE" json:"type_product"`
	ProductImages []ProductImage `gorm:"foreignKey:ProductID;OnDelete:CASCADE" json:"product_images"`
	CreatedBy     uuid.UUID      `gorm:"type:uuid" json:"created_by"`
	User          User           `gorm:"foreignKey:CreatedBy;OnDelete:CASCADE"`
	CreatedAt     time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}

type ProductImage struct {
	ID        uuid.UUID      `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	ProductID uuid.UUID      `gorm:"type:uuid;index" json:"product_id"`
	Product   Product        `gorm:"foreignKey:ProductID;OnDelete:CASCADE" json:"product"`
	ImageID   uuid.UUID      `gorm:"type:uuid;index" json:"image_id"`
	Image     Image          `gorm:"foreignKey:ImageID" json:"image"`
	AltText   string         `gorm:"type:varchar(255)" json:"alt_text"`
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
