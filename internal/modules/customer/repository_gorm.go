package customer

import (
	"context"
	"errors"
	"strings"
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

// revenueStatuses is what counts towards an account's spend.
//
// Taken from the report module rather than restated, so the customer screen and
// the financial report can never disagree about what a sale is. That
// disagreement is the specific bug the report package was written to end: the
// previous dashboard summed every order row, including the ones the shop had
// refused.
func revenueStatuses() []string {
	out := make([]string, 0, len(report.RevenueStatuses))
	for _, status := range report.RevenueStatuses {
		out = append(out, string(status))
	}
	return out
}

// listRow is the flat shape the list query selects into.
type listRow struct {
	ID              uuid.UUID
	Name            string
	Username        *string
	Email           *string
	Phone           *string
	Photo           string
	Role            string
	Status          int
	EmailVerifiedAt *time.Time
	PhoneVerifiedAt *time.Time
	OrderCount      int64
	TotalSpend      float64
	CreatedAt       time.Time
}

func (r listRow) toSummary() Summary {
	return Summary{
		ID:            r.ID,
		Name:          r.Name,
		Username:      derefString(r.Username),
		Email:         derefString(r.Email),
		Phone:         derefString(r.Phone),
		Photo:         r.Photo,
		Role:          r.Role,
		Status:        r.Status,
		EmailVerified: r.EmailVerifiedAt != nil,
		PhoneVerified: r.PhoneVerifiedAt != nil,
		OrderCount:    r.OrderCount,
		TotalSpend:    r.TotalSpend,
		CreatedAt:     r.CreatedAt,
	}
}

// List returns one page of accounts, newest first.
func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Summary, int64, error) {
	base := r.scoped(ctx, query.Filters)

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []Summary{}, 0, nil
	}

	// The two order aggregates are correlated subqueries rather than a JOIN +
	// GROUP BY: joining orders would multiply the user row per order and force
	// every selected column into the GROUP BY, which is both slower and easy to
	// get subtly wrong when a column is added later.
	var rows []listRow
	err := base.Session(&gorm.Session{}).
		Select(`users.id,
		        users.name,
		        users.username,
		        users.email,
		        users.phone,
		        users.photo,
		        COALESCE(roles.name, '') AS role,
		        users.status,
		        users.email_verified_at,
		        users.phone_verified_at,
		        users.created_at,
		        (SELECT COUNT(*) FROM orders o
		          WHERE o.user_id = users.id AND o.deleted_at IS NULL) AS order_count,
		        (SELECT COALESCE(SUM(o.total), 0) FROM orders o
		          WHERE o.user_id = users.id AND o.deleted_at IS NULL
		            AND o.status IN ?) AS total_spend`, revenueStatuses()).
		Order("users.created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}

	out := make([]Summary, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toSummary())
	}
	return out, total, nil
}

// scoped builds the filtered query shared by List and its count.
func (r *GormRepository) scoped(ctx context.Context, filters Filters) *gorm.DB {
	q := r.db.WithContext(ctx).
		Model(&model.User{}).
		Joins("LEFT JOIN roles ON roles.id = users.role_id").
		Where("users.deleted_at IS NULL")

	if filters.Status != nil {
		q = q.Where("users.status = ?", *filters.Status)
	}
	if role := strings.TrimSpace(filters.Role); role != "" {
		q = q.Where("roles.name = ?", role)
	}
	if search := strings.TrimSpace(filters.Search); search != "" {
		pattern := "%" + escapeLike(search) + "%"
		q = q.Where(
			"users.name ILIKE ? OR users.email ILIKE ? OR users.phone ILIKE ? OR users.username ILIKE ?",
			pattern, pattern, pattern, pattern,
		)
	}

	return q
}

// FindByID loads one account with everything the detail screen shows.
func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Detail, error) {
	type detailRow struct {
		listRow
		Gender      string
		DateOfBirth *time.Time
	}

	var row detailRow
	err := r.db.WithContext(ctx).
		Model(&model.User{}).
		Joins("LEFT JOIN roles ON roles.id = users.role_id").
		Select(`users.id,
		        users.name,
		        users.username,
		        users.email,
		        users.phone,
		        users.photo,
		        COALESCE(roles.name, '') AS role,
		        users.status,
		        users.email_verified_at,
		        users.phone_verified_at,
		        users.created_at,
		        users.gender,
		        users.date_of_birth,
		        (SELECT COUNT(*) FROM orders o
		          WHERE o.user_id = users.id AND o.deleted_at IS NULL) AS order_count,
		        (SELECT COALESCE(SUM(o.total), 0) FROM orders o
		          WHERE o.user_id = users.id AND o.deleted_at IS NULL
		            AND o.status IN ?) AS total_spend`, revenueStatuses()).
		Where("users.id = ? AND users.deleted_at IS NULL", id).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}

	detail := Detail{
		Summary:     row.listRow.toSummary(),
		Gender:      row.Gender,
		DateOfBirth: row.DateOfBirth,
	}

	if err := r.enrich(ctx, id, &detail); err != nil {
		return Detail{}, err
	}
	return detail, nil
}

// enrich fills the counters and history the list does not carry.
//
// Separate queries rather than more subqueries on the detail select: these run
// once for one account, and four readable statements beat one that needs a
// diagram.
func (r *GormRepository) enrich(ctx context.Context, id uuid.UUID, detail *Detail) error {
	db := r.db.WithContext(ctx)

	// The cached balance. A missing row is a zero balance, not an error: a
	// customer who has never earned a point has no user_points row.
	var balance int64
	if err := db.Table("user_points").
		Select("COALESCE(total_points, 0)").
		Where("user_id = ? AND deleted_at IS NULL", id).
		Limit(1).
		Scan(&balance).Error; err != nil {
		return err
	}
	detail.PointBalance = balance

	var claimed, used int64
	if err := db.Table("user_vouchers").
		Where("user_id = ? AND deleted_at IS NULL", id).
		Count(&claimed).Error; err != nil {
		return err
	}
	if err := db.Table("user_vouchers").
		Where("user_id = ? AND deleted_at IS NULL AND is_used = true", id).
		Count(&used).Error; err != nil {
		return err
	}
	detail.VoucherClaimed = claimed
	detail.VoucherUsed = used

	var lastOrder *time.Time
	if err := db.Table("orders").
		Select("MAX(created_at)").
		Where("user_id = ? AND deleted_at IS NULL", id).
		Scan(&lastOrder).Error; err != nil {
		return err
	}
	detail.LastOrderAt = lastOrder

	moderation := []ModerationEntry{}
	if err := db.Table("account_actions").
		Select(`account_actions.id,
		        account_actions.action,
		        account_actions.reason,
		        account_actions.actor_id,
		        COALESCE(users.name, '') AS actor_name,
		        account_actions.created_at`).
		Joins("LEFT JOIN users ON users.id = account_actions.actor_id").
		Where("account_actions.target_user_id = ?", id).
		Order("account_actions.created_at DESC").
		Find(&moderation).Error; err != nil {
		return err
	}
	detail.Moderation = moderation

	return nil
}

// ApplyStatus writes the decision and its audit row atomically.
//
// The UPDATE is guarded by the status the caller read, so two operators acting
// on one account cannot both succeed and leave two contradictory audit rows.
func (r *GormRepository) ApplyStatus(ctx context.Context, change StatusChange) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).
			Where("id = ? AND status <> ? AND deleted_at IS NULL", change.TargetID, change.Status).
			Updates(map[string]any{
				"status":     change.Status,
				"updated_at": time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// Either the account is gone or it is already in this state. The
			// service has already checked existence, so the caller is told the
			// account moved rather than that it vanished.
			return ErrNotFound
		}

		action := model.AccountAction{
			TargetUserID: change.TargetID,
			ActorID:      change.ActorID,
			Action:       change.Action(),
			Reason:       change.Reason,
		}
		return tx.Omit("Target", "Actor").Create(&action).Error
	})
}

// RoleNames lists every role, so the filter is built from the database.
func (r *GormRepository) RoleNames(ctx context.Context) ([]string, error) {
	names := []string{}
	err := r.db.WithContext(ctx).
		Table("roles").
		Order("name ASC").
		Pluck("name", &names).Error
	if err != nil {
		return nil, err
	}
	return names, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// escapeLike neutralises the wildcards in a search term, so a query for "100%"
// does not match every account in the table.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
