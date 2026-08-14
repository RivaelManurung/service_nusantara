package report

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// GormRepository is the PostgreSQL implementation of Repository.
//
// Every statement here is hand-written SQL rather than a chain of GORM
// builders, because the point of this module is the aggregation: the shape of
// the GROUP BY and of the money casts is the thing worth reading.
type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

// moneySum renders the aggregate used for every currency column.
//
// The columns are `float` (double precision). Adding thousands of doubles
// accumulates representation error -- a period of small rupiah amounts can end
// up cents away from the sum of its own rows, which makes a financial report
// that does not reconcile with the transaction list beneath it. Casting each
// value to `numeric` makes the addition exact; the result is rounded to two
// decimals and cast back to float8 so the driver hands Go a plain float64
// rather than a numeric it would decode as text.
func moneySum(column string) string {
	return fmt.Sprintf("ROUND(COALESCE(SUM(%s::numeric), 0), 2)::float8", column)
}

// scope builds the WHERE clause shared by every query in this module.
//
// `deleted_at IS NULL` is not optional: these are raw SQL statements, so GORM's
// soft-delete scope does not apply and a removed order would otherwise still be
// counted as income.
func scope(filters Filters, alias string, revenueOnly bool) (string, []any) {
	conditions := []string{
		alias + ".deleted_at IS NULL",
		alias + ".created_at >= ?",
		alias + ".created_at < ?",
	}
	args := []any{filters.Range.From, filters.Range.End()}

	if revenueOnly {
		conditions = append(conditions, alias+".status IN ?")
		args = append(args, revenueStatusStrings())
	} else if filters.Status != "" {
		conditions = append(conditions, alias+".status = ?")
		args = append(args, filters.Status)
	}

	if filters.ShopID != uuid.Nil {
		conditions = append(conditions, alias+".shop_id = ?")
		args = append(args, filters.ShopID)
	}

	if filters.PaymentMethod != "" {
		conditions = append(conditions, alias+".payment_method = ?")
		args = append(args, filters.PaymentMethod)
	}

	return strings.Join(conditions, " AND "), args
}

func (r *GormRepository) Transactions(ctx context.Context, query TransactionQuery) ([]Transaction, int64, error) {
	where, args := scope(query.Filters, "o", false)

	var total int64
	countSQL := "SELECT COUNT(*) FROM orders o WHERE " + where
	if err := r.db.WithContext(ctx).Raw(countSQL, args...).Scan(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count transactions: %w", err)
	}
	if total == 0 {
		return []Transaction{}, 0, nil
	}

	// The customer and shop joins do not filter the order itself: an order
	// placed by an account that was later removed is still a real transaction,
	// so it keeps its row and loses only the name.
	//
	// item_count is a correlated subquery rather than a join, so it is evaluated
	// for the page's rows instead of aggregating the whole order_items table.
	listSQL := `
		SELECT o.id,
		       o.code,
		       o.created_at,
		       COALESCE(u.name, '') AS customer_name,
		       COALESCE(s.name, '') AS shop_name,
		       COALESCE((SELECT SUM(oi.quantity)
		                   FROM order_items oi
		                  WHERE oi.order_id = o.id), 0) AS item_count,
		       o.sub_total,
		       o.discount_event,
		       o.discount_voucher,
		       o.shipping_fee,
		       o.total,
		       o.status,
		       o.payment_method,
		       o.order_type
		  FROM orders o
		  LEFT JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
		  LEFT JOIN shops s ON s.id = o.shop_id AND s.deleted_at IS NULL
		 WHERE ` + where + `
		 ORDER BY o.created_at DESC, o.id DESC
		 LIMIT ? OFFSET ?`

	pageArgs := append(append([]any{}, args...), query.PerPage, (query.Page-1)*query.PerPage)

	rows := make([]Transaction, 0, query.PerPage)
	if err := r.db.WithContext(ctx).Raw(listSQL, pageArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list transactions: %w", err)
	}

	for i := range rows {
		rows[i].IsRevenue = IsRevenueStatus(rows[i].Status)
	}

	return rows, total, nil
}

func (r *GormRepository) StatusSummary(ctx context.Context, filters Filters) ([]StatusSummary, error) {
	where, args := scope(filters, "o", false)

	summarySQL := `
		SELECT o.status,
		       COUNT(*) AS order_count,
		       ` + moneySum("o.sub_total") + ` AS sub_total,
		       ` + moneySum("o.discount_event") + ` AS discount_event,
		       ` + moneySum("o.discount_voucher") + ` AS discount_voucher,
		       ` + moneySum("o.shipping_fee") + ` AS shipping_fee,
		       ` + moneySum("o.total") + ` AS total
		  FROM orders o
		 WHERE ` + where + `
		 GROUP BY o.status`

	rows := make([]StatusSummary, 0, len(allStatuses))
	if err := r.db.WithContext(ctx).Raw(summarySQL, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("summarise transactions: %w", err)
	}

	for i := range rows {
		rows[i].IsRevenue = IsRevenueStatus(rows[i].Status)
	}

	return rows, nil
}

func (r *GormRepository) Revenue(ctx context.Context, query FinancialQuery) ([]RevenuePoint, FinancialTotals, error) {
	// revenueOnly, so a cancelled or rejected order can never reach the chart.
	where, args := scope(query.Filters, "o", true)

	// The unit comes from the whitelist, never from the request: date_trunc
	// takes its unit as literal text and cannot be parameterised.
	unit, ok := dateTruncUnit[query.Granularity]
	if !ok {
		return nil, FinancialTotals{}, fmt.Errorf("unsupported granularity %q", query.Granularity)
	}

	// `AT TIME ZONE 'UTC'` pins the bucket boundaries to the same zone the range
	// was parsed in; without it the series would silently shift with the
	// database session's timezone.
	bucket := fmt.Sprintf("date_trunc('%s', o.created_at AT TIME ZONE 'UTC')", unit)

	seriesSQL := `
		SELECT to_char(` + bucket + `, 'YYYY-MM-DD') AS bucket,
		       COUNT(*) AS order_count,
		       ` + moneySum("o.sub_total") + ` AS gross,
		       ` + moneySum("o.discount_event") + ` AS discount_event,
		       ` + moneySum("o.discount_voucher") + ` AS discount_voucher,
		       ` + moneySum("o.shipping_fee") + ` AS shipping,
		       ` + moneySum("o.total") + ` AS net
		  FROM orders o
		 WHERE ` + where + `
		 GROUP BY 1
		 ORDER BY 1`

	points := make([]RevenuePoint, 0, 32)
	if err := r.db.WithContext(ctx).Raw(seriesSQL, args...).Scan(&points).Error; err != nil {
		return nil, FinancialTotals{}, fmt.Errorf("build revenue series: %w", err)
	}

	// A second aggregate rather than adding the buckets up in Go: the totals are
	// money, and re-summing rounded doubles is exactly the drift moneySum exists
	// to prevent.
	totalsSQL := `
		SELECT COUNT(*) AS order_count,
		       ` + moneySum("o.sub_total") + ` AS gross,
		       ` + moneySum("o.discount_event") + ` AS discount_event,
		       ` + moneySum("o.discount_voucher") + ` AS discount_voucher,
		       ` + moneySum("o.shipping_fee") + ` AS shipping,
		       ` + moneySum("o.total") + ` AS net
		  FROM orders o
		 WHERE ` + where

	var totals FinancialTotals
	if err := r.db.WithContext(ctx).Raw(totalsSQL, args...).Scan(&totals).Error; err != nil {
		return nil, FinancialTotals{}, fmt.Errorf("total revenue: %w", err)
	}

	return points, totals, nil
}

func (r *GormRepository) TopProducts(ctx context.Context, query TopProductQuery) ([]TopProduct, error) {
	where, args := scope(query.Filters, "o", true)

	// Grouping on oi.product_id rather than p.id keeps a line whose product was
	// later soft-deleted: the quantity was still sold, and dropping it would
	// make the top-seller totals disagree with the revenue chart.
	topSQL := `
		SELECT oi.product_id,
		       COALESCE(p.name, '') AS product_name,
		       COALESCE(p.code, '') AS product_code,
		       COALESCE(SUM(oi.quantity), 0) AS quantity,
		       ` + moneySum("oi.sub_total") + ` AS revenue,
		       COUNT(DISTINCT oi.order_id) AS order_count
		  FROM order_items oi
		  JOIN orders o ON o.id = oi.order_id
		  LEFT JOIN products p ON p.id = oi.product_id AND p.deleted_at IS NULL
		 WHERE ` + where + `
		 GROUP BY oi.product_id, p.name, p.code
		 ORDER BY quantity DESC, revenue DESC, product_name ASC
		 LIMIT ?`

	rows := make([]TopProduct, 0, query.Limit)
	if err := r.db.WithContext(ctx).Raw(topSQL, append(append([]any{}, args...), query.Limit)...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list top products: %w", err)
	}

	return rows, nil
}
