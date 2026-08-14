package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Review is a customer's rating and comment about a product.
//
// OrderID is a pointer because a review does not have to come from a purchase:
// the admin dashboard has to display reviews imported from the previous system,
// which recorded no order, and a non-null column would force a fake order id on
// every one of them.
type Review struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID    uuid.UUID  `gorm:"type:uuid;index;not null" json:"user_id"`
	User      User       `gorm:"foreignKey:UserID;OnDelete:CASCADE" json:"user"`
	ProductID uuid.UUID  `gorm:"type:uuid;index;not null" json:"product_id"`
	Product   Product    `gorm:"foreignKey:ProductID;OnDelete:CASCADE" json:"product"`
	OrderID   *uuid.UUID `gorm:"type:uuid;index" json:"order_id"`
	Order     *Order     `gorm:"foreignKey:OrderID;OnDelete:CASCADE" json:"order"`

	// Rating is 1..5. The bound is also a CHECK constraint in the migration, so
	// a writer that bypasses the service cannot store a 0 or a 7.
	Rating  int    `gorm:"type:int;not null" json:"rating"`
	Comment string `gorm:"type:text" json:"comment"`

	// Status is 0 hidden / 1 visible, matching every other module's integer
	// status. New reviews default to visible; moderation hides them.
	Status int `gorm:"type:int;not null;default:1;index" json:"status"`

	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}
