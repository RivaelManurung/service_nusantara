// Package order is the back-office view of the order lifecycle: the list an
// operator works from, the detail behind one order, the trail of how it got to
// its current status, and the guarded transitions that move it on.
//
// It follows the shape of internal/modules/typeproduct -- response types,
// persistence port, business rules, HTTP handlers -- with two differences worth
// stating up front:
//
//  1. There is no Create and no Delete. Orders are created by customers at
//     checkout, not by admins, and an order is business history: it is
//     cancelled, never removed. Handing the back office a delete button would
//     make the financial report unreproducible.
//  2. Status is a state machine, not a field. Every transition is validated
//     against Transitions and recorded in model.OrderStatusHistory, so "who
//     cancelled this and why" always has an answer.
package order

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/model"
)

// ErrNotFound is returned when no order matches. Callers compare against this
// rather than gorm.ErrRecordNotFound, so the service never imports GORM.
var ErrNotFound = errors.New("order not found")

// Transitions is the lifecycle, as a graph.
//
// The linear happy path in internal/modules/report/report.go reads
//
//	ORDER_DRAFT -> WAITING_PAYMENT -> PAID -> WAITING_STORE_CONFIRMATION ->
//	STORE_ACCEPTED -> SEARCHING_DRIVER -> DRIVER_ASSIGNED -> ON_THE_WAY ->
//	DELIVERED -> COMPLETED
//
// but the real thing branches, and the branches are where the money is:
//
//   - CANCELED is reachable only up to SEARCHING_DRIVER. Once a driver has been
//     assigned the shop has committed stock and a courier is en route, so
//     "cancel" stops being a status flip and becomes a refund-and-recall
//     problem this module deliberately does not pretend to solve.
//   - STORE_REJECTED is reachable only from WAITING_STORE_CONFIRMATION. A shop
//     that already accepted an order cannot un-accept it; it cancels.
//   - COMPLETED is reachable from DELIVERED (delivery) and directly from
//     STORE_ACCEPTED (take-away, where nothing is ever delivered). See
//     allowedFor: the order type decides which of the two applies.
//
// A status present with an empty slice is terminal.
var Transitions = map[model.OrderStatus][]model.OrderStatus{
	model.OrderDraft:          {model.OrderWaitingPayment, model.OrderCanceled},
	model.OrderWaitingPayment: {model.OrderPaid, model.OrderCanceled},
	model.OrderPaid:           {model.OrderWaitingStore, model.OrderCanceled},
	model.OrderWaitingStore: {
		model.OrderStoreAccepted,
		model.OrderStoreRejected,
		model.OrderCanceled,
	},
	model.OrderStoreAccepted: {
		model.OrderSearchingDriver, // DELIVERY
		model.OrderCompleted,       // TAKE_AWAY
		model.OrderCanceled,
	},
	model.OrderSearchingDriver: {model.OrderDriverAssigned, model.OrderCanceled},
	model.OrderDriverAssigned:  {model.OrderOnTheWay},
	model.OrderOnTheWay:        {model.OrderDelivered},
	model.OrderDelivered:       {model.OrderCompleted},

	// Terminal. Listed explicitly so a reader does not have to infer their
	// absence from the map's silence.
	model.OrderCompleted:     {},
	model.OrderCanceled:      {},
	model.OrderStoreRejected: {},
}

// reasonRequired lists the transitions an operator must justify.
//
// Both destroy value and both generate the question "why?" later -- from a
// customer, from finance, or from whoever is investigating a pattern of
// cancellations. Demanding the answer at the moment it is known is far cheaper
// than reconstructing it afterwards.
var reasonRequired = map[model.OrderStatus]bool{
	model.OrderCanceled:      true,
	model.OrderStoreRejected: true,
}

// ReasonRequired reports whether moving to status must carry a reason.
func ReasonRequired(status model.OrderStatus) bool { return reasonRequired[status] }

// MaxReasonRunes bounds the free-text reason. Long enough for a real
// explanation, short enough that the column cannot be used as blob storage.
const MaxReasonRunes = 500

// AllStatuses is every status the filter accepts, in lifecycle order so the UI
// can render them as a funnel without owning a second copy of the ordering.
var AllStatuses = []model.OrderStatus{
	model.OrderDraft,
	model.OrderWaitingPayment,
	model.OrderPaid,
	model.OrderWaitingStore,
	model.OrderStoreAccepted,
	model.OrderStoreRejected,
	model.OrderSearchingDriver,
	model.OrderDriverAssigned,
	model.OrderOnTheWay,
	model.OrderDelivered,
	model.OrderCompleted,
	model.OrderCanceled,
}

// AllOrderTypes and AllPaymentMethods bound their respective filters.
var (
	AllOrderTypes     = []model.OrderType{model.TakeAway, model.Delivery}
	AllPaymentMethods = []model.PaymentMethod{model.PayCash, model.PayQRIS, model.PayTF}
)

// NextStatuses returns the statuses reachable from current for an order of this
// type, in lifecycle order.
//
// The result is what the UI renders as buttons, so the screen and the validator
// can never disagree about what is allowed -- the screen asks this function
// rather than hardcoding a second lifecycle in TypeScript.
func NextStatuses(current model.OrderStatus, orderType model.OrderType) []model.OrderStatus {
	candidates := Transitions[current]
	allowed := make([]model.OrderStatus, 0, len(candidates))

	for _, candidate := range candidates {
		if allowedFor(current, candidate, orderType) {
			allowed = append(allowed, candidate)
		}
	}
	return allowed
}

// CanTransition reports whether current -> next is legal for this order type.
func CanTransition(current, next model.OrderStatus, orderType model.OrderType) bool {
	for _, candidate := range Transitions[current] {
		if candidate == next {
			return allowedFor(current, next, orderType)
		}
	}
	return false
}

// allowedFor applies the order-type rules that the graph alone cannot express.
//
// Delivery and take-away share every state up to STORE_ACCEPTED and diverge
// after it. Encoding that as two separate graphs would duplicate the eight
// shared edges; encoding it as one graph plus this filter keeps the shared part
// stated once.
func allowedFor(current, next model.OrderStatus, orderType model.OrderType) bool {
	if current != model.OrderStoreAccepted {
		// The courier states only exist on the delivery branch, which is only
		// reachable through STORE_ACCEPTED, so nothing else needs filtering.
		return true
	}

	switch next {
	case model.OrderSearchingDriver:
		// Nobody delivers a take-away order.
		return orderType == model.Delivery
	case model.OrderCompleted:
		// A delivery is only complete once it has actually been delivered.
		return orderType == model.TakeAway
	default:
		return true
	}
}

// IsKnownStatus reports whether raw names a status this module recognises.
func IsKnownStatus(raw string) bool {
	for _, candidate := range AllStatuses {
		if string(candidate) == raw {
			return true
		}
	}
	return false
}

// Summary is one row of the order list. Field names are the contract with
// web_nusantara/src/features/order/types.ts.
type Summary struct {
	ID            uuid.UUID `json:"id"`
	Code          string    `json:"code"`
	Status        string    `json:"status"`
	OrderType     string    `json:"order_type"`
	PaymentMethod string    `json:"payment_method"`

	CustomerID   uuid.UUID `json:"customer_id"`
	CustomerName string    `json:"customer_name"`
	ShopID       uuid.UUID `json:"shop_id"`
	ShopName     string    `json:"shop_name"`

	ItemCount int     `json:"item_count"`
	Total     float64 `json:"total"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// StalledForMinutes is how long the order has sat in its current status,
	// derived from updated_at. It is what turns a list into a worklist: the
	// operator's question is not "what orders exist" but "what is stuck".
	StalledForMinutes int64 `json:"stalled_for_minutes"`
}

// Item is one line of an order.
type Item struct {
	ID          uuid.UUID `json:"id"`
	ProductID   uuid.UUID `json:"product_id"`
	ProductName string    `json:"product_name"`
	ProductCode string    `json:"product_code"`
	Image       string    `json:"image"`
	Quantity    int       `json:"quantity"`
	SubTotal    float64   `json:"sub_total"`
}

// Address is the delivery address the order was placed against.
//
// model.CustomerAddress carries no recipient or phone of its own, so neither
// appears here: the contact for a delivery is the ordering customer, already on
// Detail as CustomerName/CustomerPhone. Inventing the two fields and filling
// them from the user would look like address data that could differ from the
// account, and it never can.
type Address struct {
	Label string  `json:"label"`
	Full  string  `json:"full_address"`
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
}

// AppliedVoucher is a voucher that was redeemed against this order.
//
// model.Voucher has no name column -- Code is the identity a customer quotes
// and Description is the human sentence -- so those are the two fields carried.
type AppliedVoucher struct {
	VoucherID   uuid.UUID `json:"voucher_id"`
	Code        string    `json:"code"`
	Description string    `json:"description"`
}

// AppliedEvent is an event discount that was applied to this order.
type AppliedEvent struct {
	EventID  uuid.UUID `json:"event_id"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Discount float64   `json:"discount"`
}

// Detail is one order in full.
type Detail struct {
	Summary

	CustomerEmail string `json:"customer_email"`
	CustomerPhone string `json:"customer_phone"`
	ShopAddress   string `json:"shop_address"`

	SubTotal        float64 `json:"sub_total"`
	DiscountEvent   float64 `json:"discount_event"`
	DiscountVoucher float64 `json:"discount_voucher"`
	ShippingFee     float64 `json:"shipping_fee"`

	Note    string   `json:"note"`
	Address *Address `json:"address"`

	Items    []Item           `json:"items"`
	Vouchers []AppliedVoucher `json:"vouchers"`
	Events   []AppliedEvent   `json:"events"`

	// NextStatuses is what this order may become, already filtered by order
	// type. The screen renders one button per entry.
	NextStatuses []string `json:"next_statuses"`
	// ReasonRequiredFor is the subset of NextStatuses that demands a reason, so
	// the dialog knows to make the field mandatory without duplicating the rule.
	ReasonRequiredFor []string `json:"reason_required_for"`
}

// TimelineEntry is one recorded transition, newest first in the response.
type TimelineEntry struct {
	ID         uuid.UUID  `json:"id"`
	FromStatus string     `json:"from_status"`
	ToStatus   string     `json:"to_status"`
	Reason     string     `json:"reason"`
	ActorID    *uuid.UUID `json:"actor_id"`
	// ActorName is empty for transitions no person made -- a payment callback or
	// a scheduled job. The UI renders that as "Sistem".
	ActorName string    `json:"actor_name"`
	CreatedAt time.Time `json:"created_at"`
}

// Filters narrow the list. A zero value means "do not narrow by this".
type Filters struct {
	Status        string
	OrderType     string
	PaymentMethod string
	Search        string
	ShopID        uuid.UUID
	CustomerID    uuid.UUID

	// From and To bound created_at. Both optional; unlike the report module this
	// list is a worklist rather than an accounting document, so "everything
	// still open" is a legitimate default.
	From *time.Time
	To   *time.Time

	// ScopedShopIDs restricts the result to these shops. It is set by the
	// service from the caller's assignments, never from the request: a shop
	// filter the client could widen would not be a scope at all.
	//
	// nil means unrestricted (superadmin). Empty-but-non-nil means the caller is
	// assigned to no shop and must therefore see nothing.
	ScopedShopIDs []uuid.UUID
}

// ListQuery is one page of the order list.
type ListQuery struct {
	Filters
	Page    int
	PerPage int
}

// StatusChange is a validated transition, ready to persist.
type StatusChange struct {
	OrderID uuid.UUID
	From    model.OrderStatus
	To      model.OrderStatus
	Reason  string
	ActorID uuid.UUID
}

// Repository is the persistence port.
type Repository interface {
	List(ctx context.Context, query ListQuery) ([]Summary, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (Detail, error)
	Timeline(ctx context.Context, orderID uuid.UUID) ([]TimelineEntry, error)

	// ApplyStatus writes the new status and its history row in one transaction.
	// Splitting them would allow exactly the drift this module exists to
	// prevent: a status with no explanation, or an explanation for a status the
	// order never reached.
	ApplyStatus(ctx context.Context, change StatusChange) error

	// AssignedShopIDs lists the shops a member of staff works in, used to scope
	// every read. It queries shop_cashiers directly rather than importing the
	// shop module: a port that returns ids does not need the other module's
	// response types.
	AssignedShopIDs(ctx context.Context, staffID uuid.UUID) ([]uuid.UUID, error)
}
