package voucher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"service_nusantara/internal/model"
)

// GormRepository is the PostgreSQL implementation of Repository.
type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Voucher, int64, error) {
	tx := r.db.WithContext(ctx).Model(&model.Voucher{})

	if search := strings.TrimSpace(query.Search); search != "" {
		// ILIKE keeps the search case-insensitive without a functional index;
		// the escape guards a literal % or _ typed by the user.
		pattern := "%" + escapeLike(search) + "%"
		tx = tx.Where("code ILIKE ? OR description ILIKE ?", pattern, pattern)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count vouchers: %w", err)
	}

	var rows []model.Voucher
	err := tx.Order("created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list vouchers: %w", err)
	}

	items := make([]Voucher, 0, len(rows))
	for _, row := range rows {
		items = append(items, toVoucher(row))
	}
	return items, total, nil
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Voucher, error) {
	var row model.Voucher
	if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Voucher{}, ErrNotFound
		}
		return Voucher{}, fmt.Errorf("find voucher: %w", err)
	}
	return toVoucher(row), nil
}

func (r *GormRepository) ExistsByCode(ctx context.Context, code string, excludeID uuid.UUID) (bool, error) {
	tx := r.db.WithContext(ctx).Model(&model.Voucher{}).Where("LOWER(code) = LOWER(?)", code)
	if excludeID != uuid.Nil {
		// On update, the row being edited is not a conflict with itself.
		tx = tx.Where("id <> ?", excludeID)
	}

	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check voucher code: %w", err)
	}
	return count > 0, nil
}

func (r *GormRepository) Create(ctx context.Context, row Voucher, createdBy uuid.UUID) (Voucher, error) {
	record := model.Voucher{
		ID:              row.ID,
		Code:            row.Code,
		DiscountType:    row.DiscountType,
		DiscountAmount:  row.DiscountAmount,
		DiscountPercent: row.DiscountPercent,
		MinimumSpend:    row.MinimumSpend,
		PointCost:       row.PointCost,
		StartDate:       row.StartDate,
		EndDate:         row.EndDate,
		Quota:           row.Quota,
		Description:     row.Description,
		Status:          row.Status,
		CreatedBy:       createdBy,
	}
	if err := r.db.WithContext(ctx).Create(&record).Error; err != nil {
		return Voucher{}, fmt.Errorf("create voucher: %w", err)
	}
	return toVoucher(record), nil
}

func (r *GormRepository) Update(ctx context.Context, id uuid.UUID, row Voucher) (Voucher, error) {
	// Written as an explicit map because the zero half of the discount pair has
	// to be stored: a voucher switched from percent to amount must not keep
	// reading as a percentage discount.
	updates := map[string]any{
		"code":             row.Code,
		"discount_type":    row.DiscountType,
		"discount_amount":  row.DiscountAmount,
		"discount_percent": row.DiscountPercent,
		"minimum_spend":    row.MinimumSpend,
		"point_cost":       row.PointCost,
		"start_date":       row.StartDate,
		"end_date":         row.EndDate,
		"quota":            row.Quota,
		"description":      row.Description,
		"updated_at":       time.Now().UTC(),
	}

	result := r.db.WithContext(ctx).Model(&model.Voucher{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return Voucher{}, fmt.Errorf("update voucher: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return Voucher{}, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

func (r *GormRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status int) error {
	result := r.db.WithContext(ctx).Model(&model.Voucher{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()})

	if result.Error != nil {
		return fmt.Errorf("update voucher status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&model.Voucher{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete voucher: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) Claimed(ctx context.Context, id uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.WithContext(ctx).
		Raw(`SELECT EXISTS (
		       SELECT 1 FROM user_vouchers
		       WHERE voucher_id = ? AND deleted_at IS NULL
		     )`, id).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("check voucher claims: %w", err)
	}
	return exists, nil
}

func toVoucher(row model.Voucher) Voucher {
	return Voucher{
		ID:              row.ID,
		Code:            row.Code,
		DiscountType:    row.DiscountType,
		DiscountAmount:  row.DiscountAmount,
		DiscountPercent: row.DiscountPercent,
		MinimumSpend:    row.MinimumSpend,
		PointCost:       row.PointCost,
		StartDate:       row.StartDate,
		EndDate:         row.EndDate,
		Quota:           row.Quota,
		ClaimedCount:    row.ClaimedCount,
		Description:     row.Description,
		Status:          row.Status,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

// escapeLike neutralises the wildcards a user may type, so searching for "50%"
// does not match everything.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
