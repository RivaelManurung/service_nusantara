// Package report answers the two reporting screens in the admin web client:
// "Laporan Transaksi" (a paginated list of orders for a period, plus headline
// counts) and "Laporan Keuangan" (revenue over time and best sellers).
//
// It follows the shape of internal/modules/typeproduct: one package holds the
// response types, the persistence port, the business rules and the HTTP
// handlers. Two things make it different from a CRUD module:
//
//  1. It is read-only. There is no Input, no upload, no ErrNotFound -- an empty
//     period is a legitimate answer, not a 404.
//  2. Every number is computed by the database. The rows this module reports on
//     are orders, of which there may be millions; loading them into Go to add
//     them up would be both slow and, for money, wrong (see moneySum).
package report

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/model"
)

// RevenueStatuses are the order states whose money is counted as revenue.
//
// The lifecycle in internal/model/order.go runs
//
//	ORDER_DRAFT -> WAITING_PAYMENT -> PAID -> WAITING_STORE_CONFIRMATION ->
//	STORE_ACCEPTED -> SEARCHING_DRIVER -> DRIVER_ASSIGNED -> ON_THE_WAY ->
//	DELIVERED -> COMPLETED
//
// with STORE_REJECTED and CANCELED as terminal failures.
//
// Revenue starts at PAID: that is the first state in which the customer's money
// has actually been captured, and every state after it inherits that fact -- an
// order that is ON_THE_WAY was paid for before it was dispatched, so excluding
// it would under-report every in-flight order and make today's figures depend
// on how fast couriers happen to be moving.
//
// Deliberately excluded:
//
//   - ORDER_DRAFT and WAITING_PAYMENT: nothing has been paid. A basket someone
//     abandoned at the payment screen is not income.
//   - STORE_REJECTED and CANCELED: the sale was reversed. Counting them is the
//     specific bug this report exists to avoid -- the previous dashboard summed
//     every row and reported a month's revenue including orders the shop had
//     refused.
//
// A refund state does not exist in the model yet. When one is added it belongs
// here as an exclusion, not as a separate adjustment.
var RevenueStatuses = []model.OrderStatus{
	model.OrderPaid,
	model.OrderWaitingStore,
	model.OrderStoreAccepted,
	model.OrderSearchingDriver,
	model.OrderDriverAssigned,
	model.OrderOnTheWay,
	model.OrderDelivered,
	model.OrderCompleted,
}

// revenueStatusStrings is RevenueStatuses as bind parameters for `IN (?)`.
func revenueStatusStrings() []string {
	out := make([]string, 0, len(RevenueStatuses))
	for _, status := range RevenueStatuses {
		out = append(out, string(status))
	}
	return out
}

// IsRevenueStatus reports whether a status contributes to revenue.
func IsRevenueStatus(status string) bool {
	for _, candidate := range RevenueStatuses {
		if string(candidate) == status {
			return true
		}
	}
	return false
}

// allStatuses is every value the status filter accepts. An unknown status is
// rejected rather than silently matching nothing, so a typo in the query string
// does not look like "no transactions in this period".
var allStatuses = []model.OrderStatus{
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

var allPaymentMethods = []model.PaymentMethod{
	model.PayCash,
	model.PayQRIS,
	model.PayTF,
}

// Granularity is the bucket width of the revenue series.
type Granularity string

const (
	GranularityDay   Granularity = "day"
	GranularityWeek  Granularity = "week"
	GranularityMonth Granularity = "month"
)

// dateTruncUnit maps a granularity onto the PostgreSQL date_trunc unit.
//
// The mapping exists so the unit reaching SQL is never the caller's string:
// date_trunc takes its unit as text and cannot be parameterised, so an
// unvalidated value would be concatenated into the statement.
var dateTruncUnit = map[Granularity]string{
	GranularityDay:   "day",
	GranularityWeek:  "week",
	GranularityMonth: "month",
}

// Range is an inclusive period of calendar days.
//
// The stored bounds are half-open (From <= created_at < End) because orders
// carry a timestamp, not a date: filtering `created_at <= to` would drop every
// order placed after midnight on the final day.
type Range struct {
	From time.Time
	To   time.Time
}

// End is the exclusive upper bound for SQL comparisons.
func (r Range) End() time.Time { return r.To.AddDate(0, 0, 1) }

// Days is the inclusive length of the range.
func (r Range) Days() int { return int(r.To.Sub(r.From).Hours()/24) + 1 }

func (r Range) FromString() string { return r.From.Format(dateLayout) }
func (r Range) ToString() string   { return r.To.Format(dateLayout) }

const dateLayout = "2006-01-02"

// MaxRangeDays caps how much of the orders table one request may scan. A year
// plus a day covers "this year against last year to date" while keeping a
// single request bounded; without it, an empty-to-now range is a free table
// scan any authenticated user could trigger repeatedly.
const MaxRangeDays = 366

// ParseRange reads the `from` and `to` query values.
//
// Both are required and both are rejected loudly: a report that silently
// answers for all of history when the dates are missing is the worst outcome,
// because the numbers look plausible.
func ParseRange(fromRaw, toRaw string) (Range, error) {
	var fields []httpx.FieldError

	from, err := parseDay(strings.TrimSpace(fromRaw))
	if err != nil {
		fields = append(fields, httpx.FieldError{Field: "from", Message: err.Error()})
	}

	to, err := parseDay(strings.TrimSpace(toRaw))
	if err != nil {
		fields = append(fields, httpx.FieldError{Field: "to", Message: err.Error()})
	}

	if len(fields) > 0 {
		return Range{}, httpx.Validation("request validation failed").WithDetails(fields)
	}

	period := Range{From: from, To: to}

	if to.Before(from) {
		return Range{}, httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "to", Message: "must not be earlier than from"},
		})
	}

	if period.Days() > MaxRangeDays {
		return Range{}, httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "to", Message: fmt.Sprintf("the period must not exceed %d days", MaxRangeDays)},
		})
	}

	return period, nil
}

func parseDay(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("is required")
	}
	// UTC rather than the server's local zone: the same request must produce the
	// same rows regardless of where the process happens to run.
	day, err := time.ParseInLocation(dateLayout, raw, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be a date in YYYY-MM-DD format")
	}
	return day, nil
}

// ParseGranularity validates the bucket width, defaulting to daily.
func ParseGranularity(raw string) (Granularity, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return GranularityDay, nil
	}

	granularity := Granularity(trimmed)
	if _, ok := dateTruncUnit[granularity]; !ok {
		return "", httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "granularity", Message: "must be one of: day, week, month"},
		})
	}
	return granularity, nil
}

// ParseStatus validates an optional status filter.
func ParseStatus(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	for _, candidate := range allStatuses {
		if string(candidate) == trimmed {
			return trimmed, nil
		}
	}
	return "", httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
		{Field: "status", Message: "is not a known order status"},
	})
}

// ParsePaymentMethod validates an optional payment method filter.
func ParsePaymentMethod(raw string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToUpper(raw))
	if trimmed == "" {
		return "", nil
	}
	for _, candidate := range allPaymentMethods {
		if string(candidate) == trimmed {
			return trimmed, nil
		}
	}
	return "", httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
		{Field: "payment_method", Message: "must be one of: CASH, QRIS, TRANSFER"},
	})
}

// Filters narrow every endpoint in this module to the same slice of orders.
type Filters struct {
	Range         Range
	Status        string
	ShopID        uuid.UUID
	PaymentMethod string
}

// TransactionQuery is one page of the transaction list.
type TransactionQuery struct {
	Filters
	Page    int
	PerPage int
}

// Transaction is one row of the transaction report. The field names are the
// contract with web_nusantara/src/features/report/types.ts.
type Transaction struct {
	ID              uuid.UUID `json:"id"`
	Code            string    `json:"code"`
	CreatedAt       time.Time `json:"created_at"`
	CustomerName    string    `json:"customer_name"`
	ShopName        string    `json:"shop_name"`
	ItemCount       int       `json:"item_count"`
	SubTotal        float64   `json:"sub_total"`
	DiscountEvent   float64   `json:"discount_event"`
	DiscountVoucher float64   `json:"discount_voucher"`
	ShippingFee     float64   `json:"shipping_fee"`
	Total           float64   `json:"total"`
	Status          string    `json:"status"`
	PaymentMethod   string    `json:"payment_method"`
	OrderType       string    `json:"order_type"`
	// IsRevenue tells the UI which rows the financial report counted, so the two
	// screens cannot disagree about what a "sale" is.
	IsRevenue bool `json:"is_revenue"`
}

// StatusSummary is the per-status block of the transaction summary.
type StatusSummary struct {
	Status          string  `json:"status"`
	OrderCount      int64   `json:"order_count"`
	SubTotal        float64 `json:"sub_total"`
	DiscountEvent   float64 `json:"discount_event"`
	DiscountVoucher float64 `json:"discount_voucher"`
	ShippingFee     float64 `json:"shipping_fee"`
	Total           float64 `json:"total"`
	IsRevenue       bool    `json:"is_revenue"`
}

// TransactionSummary is the headline block above the table.
type TransactionSummary struct {
	From     string          `json:"from"`
	To       string          `json:"to"`
	Statuses []StatusSummary `json:"statuses"`
	// OrderCount and Total cover every order in the period, revenue or not.
	OrderCount int64   `json:"order_count"`
	Total      float64 `json:"total"`
	// Revenue* cover only the statuses in RevenueStatuses.
	RevenueOrderCount int64   `json:"revenue_order_count"`
	RevenueTotal      float64 `json:"revenue_total"`
}

// FinancialQuery is a revenue series request.
type FinancialQuery struct {
	Filters
	Granularity Granularity
}

// RevenuePoint is one bucket of the revenue series.
type RevenuePoint struct {
	// Bucket is the first day of the bucket, YYYY-MM-DD.
	Bucket          string  `json:"bucket"`
	OrderCount      int64   `json:"order_count"`
	Gross           float64 `json:"gross"`
	DiscountEvent   float64 `json:"discount_event"`
	DiscountVoucher float64 `json:"discount_voucher"`
	Shipping        float64 `json:"shipping"`
	Net             float64 `json:"net"`
}

// FinancialTotals is the whole-period roll-up.
type FinancialTotals struct {
	OrderCount      int64   `json:"order_count"`
	Gross           float64 `json:"gross"`
	DiscountEvent   float64 `json:"discount_event"`
	DiscountVoucher float64 `json:"discount_voucher"`
	Shipping        float64 `json:"shipping"`
	Net             float64 `json:"net"`
}

// FinancialReport is the financial page's payload.
type FinancialReport struct {
	From        string          `json:"from"`
	To          string          `json:"to"`
	Granularity Granularity     `json:"granularity"`
	Points      []RevenuePoint  `json:"points"`
	Totals      FinancialTotals `json:"totals"`
	// RevenueStatuses is echoed so the screen can explain which orders it counted
	// without hardcoding the list a second time.
	RevenueStatuses []string `json:"revenue_statuses"`
}

// TopProductQuery asks for the best sellers of a period.
type TopProductQuery struct {
	Filters
	Limit int
}

// Top-seller list bounds. The screen shows ten; the cap stops a caller asking
// for the entire catalogue in one response.
const (
	DefaultTopProducts = 10
	MaxTopProducts     = 50
)

// TopProduct is one best-selling product.
//
// A product is not owned by a shop in this model (shop_products is a join
// table), so the row identifies the product rather than a shop/product pair --
// the shop filter still applies, it just narrows which order lines are counted.
type TopProduct struct {
	ProductID   uuid.UUID `json:"product_id"`
	ProductName string    `json:"product_name"`
	ProductCode string    `json:"product_code"`
	Quantity    int64     `json:"quantity"`
	Revenue     float64   `json:"revenue"`
	OrderCount  int64     `json:"order_count"`
}

// Repository is the persistence port. Every method returns numbers the database
// already aggregated; none of them returns raw orders.
type Repository interface {
	Transactions(ctx context.Context, query TransactionQuery) ([]Transaction, int64, error)
	StatusSummary(ctx context.Context, filters Filters) ([]StatusSummary, error)
	Revenue(ctx context.Context, query FinancialQuery) ([]RevenuePoint, FinancialTotals, error)
	TopProducts(ctx context.Context, query TopProductQuery) ([]TopProduct, error)
}
