package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type OrderType string
type PaymentMethod string
type OrderStatus string

const (
	TakeAway OrderType = "TAKE_AWAY"
	Delivery OrderType = "DELIVERY"

	PayCash PaymentMethod = "CASH"
	PayQRIS PaymentMethod = "QRIS"
	PayTF   PaymentMethod = "TRANSFER"

	OrderDraft          OrderStatus = "ORDER_DRAFT"
	OrderWaitingPayment OrderStatus = "WAITING_PAYMENT"
	OrderPaid           OrderStatus = "PAID"

	OrderWaitingStore  OrderStatus = "WAITING_STORE_CONFIRMATION"
	OrderStoreAccepted OrderStatus = "STORE_ACCEPTED"
	OrderStoreRejected OrderStatus = "STORE_REJECTED"

	OrderSearchingDriver OrderStatus = "SEARCHING_DRIVER"
	OrderDriverAssigned  OrderStatus = "DRIVER_ASSIGNED"
	OrderOnTheWay        OrderStatus = "ON_THE_WAY"

	OrderDelivered OrderStatus = "DELIVERED"
	OrderCompleted OrderStatus = "COMPLETED"
	OrderCanceled  OrderStatus = "CANCELED"
)

type Order struct {
	ID                uuid.UUID        `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Code              string           `gorm:"type:varchar(255)" json:"code"`
	UserID            uuid.UUID        `gorm:"type:uuid" json:"user_id"`
	User              User             `gorm:"foreignKey:UserID;OnDelete:CASCADE" json:"user"`
	ShopID            uuid.UUID        `gorm:"type:uuid" json:"shop_id"`
	Shop              Shop             `gorm:"foreignKey:ShopID;OnDelete:CASCADE" json:"shop"`
	CartID            uuid.UUID        `gorm:"type:uuid" json:"cart_id"`
	Cart              Cart             `gorm:"foreignKey:CartID;OnDelete:CASCADE" json:"cart"`
	Status            OrderStatus      `gorm:"type:varchar(255)" json:"status"`
	OrderType         OrderType        `gorm:"type:varchar(255)" json:"order_type"`
	PaymentMethod     PaymentMethod    `gorm:"type:varchar(255)" json:"payment_method"`
	SubTotal          float64          `gorm:"type:float" json:"sub_total"`
	DiscountEvent     float64          `gorm:"type:float" json:"discount_event"`
	DiscountVoucher   float64          `gorm:"type:float" json:"discount_voucher"`
	ShippingFee       float64          `gorm:"float" json:"shipping_fee"`
	Total             float64          `gorm:"total" json:"total"`
	CustomerAddressID *uuid.UUID       `gorm:"type:uuid" json:"customer_address_id"`
	CustomerAddress   *CustomerAddress `gorm:"foreignKey:CustomerAddressID;OnDelete:CASCADE" json:"customer_address"`
	Note              *string          `gorm:"type:text" json:"note"`
	CreatedAt         time.Time        `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         time.Time        `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt         gorm.DeletedAt   `gorm:"index" json:"deleted_at,omitempty"`
}

type OrderItem struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	OrderID   uuid.UUID `gorm:"type:uuid" json:"order_id"`
	Order     Order     `gorm:"foreignKey:OrderID;OnDelete:CASCADE" json:"order"`
	ProductID uuid.UUID `gorm:"type:uuid" json:"product_id"`
	Product   Product   `gorm:"foreignKey:ProductID;OnDelete:CASCADE" json:"product"`
	Quantity  int       `gorm:"type:int" json:"quantity"`
	SubTotal  float64   `gorm:"type:float" json:"sub_total"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

type OrderEvent struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	OrderID   uuid.UUID `gorm:"type:uuid" json:"order_id"`
	Order     Order     `gorm:"foreignKey:OrderID;OnDelete:CASCADE" json:"order"`
	EventID   uuid.UUID `gorm:"type:uuid" json:"event_id"`
	Event     Event     `gorm:"foreignKey:EventID;OnDelete:CASCADE" json:"event"`
	Type      string    `gorm:"type:varchar(100)" json:"type"`
	Discount  float64   `gorm:"type:float" json:"discount"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

type OrderReward struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	OrderID   uuid.UUID `gorm:"type:uuid" json:"order_id"`
	Order     Order     `gorm:"foreignKey:OrderID;OnDelete:CASCADE" json:"order"`
	ProductID uuid.UUID `gorm:"type:uuid" json:"product_id"`
	Product   Product   `gorm:"foreignKey:ProductID;OnDelete:CASCADE" json:"product"`
	Quantity  int       `gorm:"type:int" json:"quantity"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

type OrderVoucher struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	OrderID   uuid.UUID `gorm:"type:uuid" json:"order_id"`
	Order     Order     `gorm:"foreignKey:OrderID;OnDelete:CASCADE" json:"order"`
	VoucherID uuid.UUID `gorm:"type:uuid" json:"voucher_id"`
	Voucher   Voucher   `gorm:"foreignKey:VoucherID;OnDelete:CASCADE" json:"voucher"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
