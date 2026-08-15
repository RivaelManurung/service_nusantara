package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"service_nusantara/internal/model"
	"service_nusantara/internal/modules/report"
)

// GormRepository is the Postgres implementation of Repository.
type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

const dateLayout = "2006-01-02"

// revenueStatuses is what counts as a sale.
//
// Taken from the report module rather than restated, so the dashboard and the
// financial report can never disagree. That disagreement is the exact bug the
// report package was written to end -- the previous dashboard summed every
// order row, including the ones the shop had refused.
func revenueStatuses() []string {
	out := make([]string, 0, len(report.RevenueStatuses))
	for _, status := range report.RevenueStatuses {
		out = append(out, string(status))
	}
	return out
}

// terminalStatuses can never be "stuck": they are finished.
var terminalStatuses = []string{
	string(model.OrderCompleted),
	string(model.OrderCanceled),
	string(model.OrderStoreRejected),
}

// awaitingShop are the states an operator can personally clear.
var awaitingShop = []string{
	string(model.OrderPaid),
	string(model.OrderWaitingStore),
}

// accountRow is the identity block every anomaly rule selects.
type accountRow struct {
	UserID uuid.UUID
	Name   string
	Email  string
	Phone  string
}

// Summary returns today's figures plus yesterday's for comparison.
func (r *GormRepository) Summary(ctx context.Context, day, stalledBefore time.Time) (Summary, error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := start.AddDate(0, 0, 1)
	previousStart := start.AddDate(0, 0, -1)

	summary := Summary{Date: start.Format(dateLayout)}

	today, err := r.dayFigures(ctx, start, end)
	if err != nil {
		return Summary{}, err
	}
	summary.OrdersToday = today.orders
	summary.RevenueToday = today.revenue

	yesterday, err := r.dayFigures(ctx, previousStart, start)
	if err != nil {
		return Summary{}, err
	}
	// Only offered when there is something to compare: growth measured from a
	// day with no orders is noise dressed as insight.
	if yesterday.orders > 0 {
		orders := yesterday.orders
		revenue := yesterday.revenue
		summary.OrdersYesterday = &orders
		summary.RevenueYesterday = &revenue
	}

	if err := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("deleted_at IS NULL AND created_at >= ? AND created_at < ?", start, end).
		Count(&summary.NewCustomers).Error; err != nil {
		return Summary{}, fmt.Errorf("count new customers: %w", err)
	}

	// Stalled is measured across all open orders, not just today's: an order
	// forgotten on Friday is exactly what this number exists to surface on
	// Monday.
	if err := r.db.WithContext(ctx).
		Model(&model.Order{}).
		Where("deleted_at IS NULL AND status NOT IN ? AND updated_at < ?",
			terminalStatuses, stalledBefore).
		Count(&summary.StalledOrders).Error; err != nil {
		return Summary{}, fmt.Errorf("count stalled orders: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Model(&model.Order{}).
		Where("deleted_at IS NULL AND status IN ?", awaitingShop).
		Count(&summary.AwaitingAction).Error; err != nil {
		return Summary{}, fmt.Errorf("count orders awaiting action: %w", err)
	}

	return summary, nil
}

type dayFigures struct {
	orders  int64
	revenue float64
}

// dayFigures counts orders and sums revenue for one half-open interval.
func (r *GormRepository) dayFigures(ctx context.Context, start, end time.Time) (dayFigures, error) {
	var row struct {
		Orders  int64
		Revenue float64
	}

	err := r.db.WithContext(ctx).
		Model(&model.Order{}).
		Select(`COUNT(*) AS orders,
		        COALESCE(SUM(CASE WHEN status IN ? THEN total ELSE 0 END), 0) AS revenue`,
			revenueStatuses()).
		Where("deleted_at IS NULL AND created_at >= ? AND created_at < ?", start, end).
		Scan(&row).Error
	if err != nil {
		return dayFigures{}, fmt.Errorf("day figures: %w", err)
	}

	return dayFigures{orders: row.Orders, revenue: row.Revenue}, nil
}

// Trend returns the last `days` days of sales, oldest first.
//
// generate_series fills the gaps: a day with no orders must appear as a zero
// rather than be missing, or the chart silently compresses quiet days out of
// existence and every line looks healthy.
func (r *GormRepository) Trend(ctx context.Context, days int) ([]TrendPoint, error) {
	rows := []TrendPoint{}

	err := r.db.WithContext(ctx).Raw(`
		SELECT to_char(d.day, 'YYYY-MM-DD') AS date,
		       COALESCE(o.orders, 0)  AS orders,
		       COALESCE(o.revenue, 0) AS revenue
		  FROM generate_series(
		         (CURRENT_DATE - ((? - 1) || ' days')::interval)::date,
		         CURRENT_DATE,
		         INTERVAL '1 day'
		       ) AS d(day)
		  LEFT JOIN (
		        SELECT date_trunc('day', created_at)::date AS day,
		               COUNT(*) AS orders,
		               COALESCE(SUM(CASE WHEN status IN ? THEN total ELSE 0 END), 0) AS revenue
		          FROM orders
		         WHERE deleted_at IS NULL
		         GROUP BY 1
		       ) AS o ON o.day = d.day
		 ORDER BY d.day ASC`,
		days, revenueStatuses()).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("trend: %w", err)
	}

	return rows, nil
}

// Anomalies runs the rules and returns the findings.
//
// Separate queries rather than one union: each rule reads different tables, and
// a single statement joining all of them would be both slower and unreadable --
// which matters, because whoever tunes a threshold has to see what it does.
func (r *GormRepository) Anomalies(ctx context.Context, limit int) ([]Anomaly, error) {
	findings := []Anomaly{}

	for _, rule := range []func(context.Context, int) ([]Anomaly, error){
		r.voucherHoarders,
		r.serialCancellers,
		r.driftedPoints,
	} {
		found, err := rule(ctx, limit)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}

	if len(findings) > limit {
		findings = findings[:limit]
	}
	return findings, nil
}

// voucherHoarders: claims several vouchers, redeems none, never orders.
func (r *GormRepository) voucherHoarders(ctx context.Context, limit int) ([]Anomaly, error) {
	var rows []struct {
		accountRow
		Claimed int64
	}

	err := r.db.WithContext(ctx).Raw(`
		SELECT u.id AS user_id,
		       COALESCE(u.name, '')  AS name,
		       COALESCE(u.email, '') AS email,
		       COALESCE(u.phone, '') AS phone,
		       COUNT(uv.id) AS claimed
		  FROM users u
		  JOIN user_vouchers uv
		    ON uv.user_id = u.id AND uv.deleted_at IS NULL AND uv.is_used = false
		 WHERE u.deleted_at IS NULL
		   AND NOT EXISTS (
		         SELECT 1 FROM orders o
		          WHERE o.user_id = u.id AND o.deleted_at IS NULL
		       )
		 GROUP BY u.id, u.name, u.email, u.phone
		HAVING COUNT(uv.id) >= ?
		 ORDER BY claimed DESC
		 LIMIT ?`, VoucherHoardMin, limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("voucher hoarders: %w", err)
	}

	out := make([]Anomaly, 0, len(rows))
	for _, item := range rows {
		out = append(out, Anomaly{
			UserID:   item.UserID,
			Name:     item.Name,
			Email:    item.Email,
			Phone:    item.Phone,
			Rule:     RuleVoucherHoarding,
			Severity: SeverityWarn,
			Metric:   item.Claimed,
			Detail: fmt.Sprintf(
				"Mengklaim %d voucher, belum memakai satu pun, dan belum pernah memesan.",
				item.Claimed),
		})
	}
	return out, nil
}

// serialCancellers: a majority of their orders end cancelled or rejected.
func (r *GormRepository) serialCancellers(ctx context.Context, limit int) ([]Anomaly, error) {
	var rows []struct {
		accountRow
		Total     int64
		Cancelled int64
	}

	err := r.db.WithContext(ctx).Raw(`
		SELECT u.id AS user_id,
		       COALESCE(u.name, '')  AS name,
		       COALESCE(u.email, '') AS email,
		       COALESCE(u.phone, '') AS phone,
		       COUNT(o.id) AS total,
		       COUNT(*) FILTER (WHERE o.status IN (?, ?)) AS cancelled
		  FROM users u
		  JOIN orders o ON o.user_id = u.id AND o.deleted_at IS NULL
		 WHERE u.deleted_at IS NULL
		 GROUP BY u.id, u.name, u.email, u.phone
		HAVING COUNT(o.id) >= ?
		   AND COUNT(*) FILTER (WHERE o.status IN (?, ?)) * 100 / COUNT(o.id) >= ?
		 ORDER BY cancelled DESC
		 LIMIT ?`,
		string(model.OrderCanceled), string(model.OrderStoreRejected),
		CancelRateMinOrders,
		string(model.OrderCanceled), string(model.OrderStoreRejected),
		CancelRatePercent, limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("serial cancellers: %w", err)
	}

	out := make([]Anomaly, 0, len(rows))
	for _, item := range rows {
		out = append(out, Anomaly{
			UserID:   item.UserID,
			Name:     item.Name,
			Email:    item.Email,
			Phone:    item.Phone,
			Rule:     RuleCancelRate,
			Severity: SeverityWarn,
			Metric:   item.Cancelled,
			Detail: fmt.Sprintf(
				"%d dari %d pesanan berakhir dibatalkan atau ditolak.",
				item.Cancelled, item.Total),
		})
	}
	return out, nil
}

// driftedPoints: the cached balance disagrees with its own ledger.
//
// Not fraud -- a bug. It belongs in the same queue because it is found the same
// way (nobody goes looking) and costs the same thing: a customer with the wrong
// balance eventually complains.
func (r *GormRepository) driftedPoints(ctx context.Context, limit int) ([]Anomaly, error) {
	var rows []struct {
		accountRow
		Cached int64
		Ledger int64
	}

	err := r.db.WithContext(ctx).Raw(`
		SELECT u.id AS user_id,
		       COALESCE(u.name, '')  AS name,
		       COALESCE(u.email, '') AS email,
		       COALESCE(u.phone, '') AS phone,
		       COALESCE(p.total_points, 0) AS cached,
		       COALESCE(l.balance, 0)      AS ledger
		  FROM users u
		  LEFT JOIN user_points p
		         ON p.user_id = u.id AND p.deleted_at IS NULL
		  LEFT JOIN (
		        SELECT user_id,
		               SUM(CASE WHEN direction = 'in' THEN points ELSE -points END) AS balance
		          FROM user_point_histories
		         WHERE deleted_at IS NULL
		         GROUP BY user_id
		       ) AS l ON l.user_id = u.id
		 WHERE u.deleted_at IS NULL
		   AND (p.id IS NOT NULL OR l.user_id IS NOT NULL)
		   AND COALESCE(p.total_points, 0) <> COALESCE(l.balance, 0)
		 ORDER BY ABS(COALESCE(p.total_points, 0) - COALESCE(l.balance, 0)) DESC
		 LIMIT ?`, limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("drifted points: %w", err)
	}

	out := make([]Anomaly, 0, len(rows))
	for _, item := range rows {
		drift := item.Cached - item.Ledger
		out = append(out, Anomaly{
			UserID:   item.UserID,
			Name:     item.Name,
			Email:    item.Email,
			Phone:    item.Phone,
			Rule:     RulePointDrift,
			Severity: SeverityWarn,
			Metric:   abs64(drift),
			Detail: fmt.Sprintf(
				"Saldo tersimpan %d, riwayat berjumlah %d — selisih %d.",
				item.Cached, item.Ledger, drift),
		})
	}
	return out, nil
}

func abs64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
