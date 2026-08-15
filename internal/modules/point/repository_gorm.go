package point

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"service_nusantara/internal/model"
)

// GormRepository is the Postgres implementation of Repository.
type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

// AccountExists reports whether the user row is there at all.
func (r *GormRepository) AccountExists(ctx context.Context, userID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ? AND deleted_at IS NULL", userID).
		Count(&count).Error
	return count > 0, err
}

// Balance computes the ledger truth alongside the cached total.
//
// One statement rather than three round trips: the aggregates are over the same
// table and the same account, so making the database do it once is both faster
// and free of the race where the ledger changes between reads.
func (r *GormRepository) Balance(ctx context.Context, userID uuid.UUID) (Balance, error) {
	var ledger struct {
		Inflow        int64
		Outflow       int64
		EntryCount    int64
		ExpiredInflow int64
	}

	err := r.db.WithContext(ctx).
		Table("user_point_histories").
		Select(`COALESCE(SUM(CASE WHEN direction = 'in'  THEN points ELSE 0 END), 0) AS inflow,
		        COALESCE(SUM(CASE WHEN direction = 'out' THEN points ELSE 0 END), 0) AS outflow,
		        COUNT(*) AS entry_count,
		        COALESCE(SUM(CASE WHEN direction = 'in' AND expired_at IS NOT NULL
		                           AND expired_at < NOW() THEN points ELSE 0 END), 0) AS expired_inflow`).
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Scan(&ledger).Error
	if err != nil {
		return Balance{}, err
	}

	var cached struct {
		TotalPoints int64
		UpdatedAt   *time.Time
	}
	// A missing user_points row is a zero balance, not an error: an account that
	// has never earned a point never had one created.
	err = r.db.WithContext(ctx).
		Table("user_points").
		Select("COALESCE(total_points, 0) AS total_points, updated_at").
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Limit(1).
		Scan(&cached).Error
	if err != nil {
		return Balance{}, err
	}

	ledgerBalance := ledger.Inflow - ledger.Outflow

	return Balance{
		UserID:        userID,
		Cached:        cached.TotalPoints,
		Ledger:        ledgerBalance,
		Drift:         cached.TotalPoints - ledgerBalance,
		EntryCount:    ledger.EntryCount,
		ExpiredInflow: ledger.ExpiredInflow,
		UpdatedAt:     cached.UpdatedAt,
	}, nil
}

// History returns one page of the ledger, newest first.
func (r *GormRepository) History(ctx context.Context, query HistoryQuery) ([]Entry, int64, error) {
	base := r.db.WithContext(ctx).
		Table("user_point_histories").
		Where("user_id = ? AND deleted_at IS NULL", query.UserID)

	if query.Direction != "" {
		base = base.Where("direction = ?", query.Direction)
	}

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []Entry{}, 0, nil
	}

	var rows []Entry
	err := base.Session(&gorm.Session{}).
		Select(`id,
		        direction,
		        points,
		        COALESCE(point_type, '')  AS point_type,
		        COALESCE(source, '')      AS source,
		        COALESCE(source_id, '')   AS source_id,
		        COALESCE(description, '') AS description,
		        expired_at,
		        created_at,
		        (expired_at IS NOT NULL AND expired_at < NOW()) AS is_expired`).
		Order("created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []Entry{}
	}
	return rows, total, nil
}

// Adjust writes the movement and moves the cached total together.
//
// The UPDATE is expressed as `total_points = total_points + delta` rather than
// as a value computed in Go. Reading the total, adding to it and writing it back
// would lose one of two concurrent adjustments; letting the database do the
// arithmetic under the row lock cannot.
func (r *GormRepository) Adjust(ctx context.Context, adjustment Adjustment) error {
	delta := adjustment.Points
	if adjustment.Direction == DirectionOut {
		delta = -delta
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		entry := model.UserPointHistories{
			UserID:      adjustment.UserID,
			PointType:   "adjustment",
			Source:      SourceAdjustment,
			SourceId:    adjustment.ActorID.String(),
			Points:      int(adjustment.Points),
			Description: adjustment.Reason,
			Direction:   adjustment.Direction,
		}
		if err := tx.Omit("User").Create(&entry).Error; err != nil {
			return err
		}

		// Upsert: an account being corrected may never have earned a point, so
		// the row it needs may not exist yet.
		return tx.Exec(`
			INSERT INTO user_points (id, user_id, total_points, created_at, updated_at)
			VALUES (gen_random_uuid(), ?, ?, NOW(), NOW())
			ON CONFLICT (user_id) DO UPDATE
			SET total_points = user_points.total_points + ?, updated_at = NOW()`,
			adjustment.UserID, delta, delta).Error
	})
}

// ClaimedVouchers lists what an account holds, newest claim first.
func (r *GormRepository) ClaimedVouchers(ctx context.Context, userID uuid.UUID) ([]ClaimedVoucher, error) {
	rows := []ClaimedVoucher{}
	err := r.db.WithContext(ctx).
		Table("user_vouchers").
		Select(`user_vouchers.id,
		        user_vouchers.voucher_id,
		        COALESCE(vouchers.code, '')        AS code,
		        COALESCE(vouchers.description, '') AS description,
		        user_vouchers.is_used,
		        user_vouchers.claimed_at,
		        user_vouchers.redeemed_at,
		        user_voucher_details.valid_until`).
		Joins("LEFT JOIN vouchers ON vouchers.id = user_vouchers.voucher_id").
		Joins("LEFT JOIN user_voucher_details ON user_voucher_details.id = user_vouchers.detail_id").
		Where("user_vouchers.user_id = ? AND user_vouchers.deleted_at IS NULL", userID).
		Order("user_vouchers.claimed_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// Claimants lists who holds a given voucher.
//
// This is the other half of voucher oversight: ClaimedVouchers answers "what
// does this person hold", Claimants answers "who took this promotion", which is
// the view that makes a coordinated claim spree visible.
func (r *GormRepository) Claimants(
	ctx context.Context,
	voucherID uuid.UUID,
	page, perPage int,
) ([]Claimant, int64, error) {
	base := r.db.WithContext(ctx).
		Table("user_vouchers").
		Joins("LEFT JOIN users ON users.id = user_vouchers.user_id").
		Where("user_vouchers.voucher_id = ? AND user_vouchers.deleted_at IS NULL", voucherID)

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []Claimant{}, 0, nil
	}

	rows := []Claimant{}
	err := base.Session(&gorm.Session{}).
		Select(`user_vouchers.user_id,
		        COALESCE(users.name, '')  AS name,
		        COALESCE(users.email, '') AS email,
		        COALESCE(users.phone, '') AS phone,
		        user_vouchers.is_used,
		        user_vouchers.claimed_at,
		        user_vouchers.redeemed_at`).
		Order("user_vouchers.claimed_at DESC").
		Limit(perPage).
		Offset((page - 1) * perPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
