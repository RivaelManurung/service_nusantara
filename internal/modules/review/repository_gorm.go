package review

import (
	"context"
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

// row is the flattened join result. It is separate from model.Review because
// the response carries the product and reviewer names, which live in other
// tables and would otherwise require preloading two whole records per review.
type row struct {
	ID           uuid.UUID
	ProductID    uuid.UUID
	ProductName  string
	UserID       uuid.UUID
	ReviewerName string
	OrderID      *uuid.UUID
	Rating       int
	Comment      string
	Status       int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// selectColumns names every column explicitly so adding one to the table cannot
// silently widen the response.
const selectColumns = `reviews.id AS id,
	reviews.product_id AS product_id,
	COALESCE(products.name, '') AS product_name,
	reviews.user_id AS user_id,
	COALESCE(users.name, '') AS reviewer_name,
	reviews.order_id AS order_id,
	reviews.rating AS rating,
	COALESCE(reviews.comment, '') AS comment,
	reviews.status AS status,
	reviews.created_at AS created_at,
	reviews.updated_at AS updated_at`

// base builds the join shared by every read.
//
// The joins are LEFT so a soft-deleted product or a removed account does not
// make its reviews disappear from moderation -- those are exactly the rows an
// administrator most needs to be able to find and take down.
func (r *GormRepository) base(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Model(&model.Review{}).
		Joins("LEFT JOIN products ON products.id = reviews.product_id").
		Joins("LEFT JOIN users ON users.id = reviews.user_id")
}

// filtered applies the list screen's search and filters.
func filtered(tx *gorm.DB, query ListQuery) *gorm.DB {
	if search := strings.TrimSpace(query.Search); search != "" {
		// ILIKE keeps the search case-insensitive without a functional index;
		// the escape guards a literal % or _ typed by the user. The parentheses
		// are explicit so the OR cannot escape the filters below.
		pattern := "%" + escapeLike(search) + "%"
		tx = tx.Where("(products.name ILIKE ? OR reviews.comment ILIKE ?)", pattern, pattern)
	}
	if query.Rating != nil {
		tx = tx.Where("reviews.rating = ?", *query.Rating)
	}
	if query.Status != nil {
		tx = tx.Where("reviews.status = ?", *query.Status)
	}
	return tx
}

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Review, int64, error) {
	var total int64
	if err := filtered(r.base(ctx), query).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count reviews: %w", err)
	}

	// A second statement rather than a reused one: Count rewrites the SELECT
	// list, and sharing a *gorm.DB between the two makes the second query
	// depend on what the first left behind.
	var rows []row
	err := filtered(r.base(ctx), query).
		Select(selectColumns).
		Order("reviews.created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list reviews: %w", err)
	}

	items := make([]Review, 0, len(rows))
	for _, record := range rows {
		items = append(items, toReview(record))
	}
	return items, total, nil
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Review, error) {
	var rows []row
	err := r.base(ctx).
		Select(selectColumns).
		Where("reviews.id = ?", id).
		Limit(1).
		Scan(&rows).Error
	if err != nil {
		return Review{}, fmt.Errorf("find review: %w", err)
	}
	if len(rows) == 0 {
		return Review{}, ErrNotFound
	}
	return toReview(rows[0]), nil
}

func (r *GormRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status int) error {
	result := r.db.WithContext(ctx).Model(&model.Review{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()})

	if result.Error != nil {
		return fmt.Errorf("update review status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&model.Review{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete review: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func toReview(record row) Review {
	return Review{
		ID:           record.ID,
		ProductID:    record.ProductID,
		ProductName:  record.ProductName,
		UserID:       record.UserID,
		ReviewerName: record.ReviewerName,
		OrderID:      record.OrderID,
		Rating:       record.Rating,
		Comment:      record.Comment,
		Status:       record.Status,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
	}
}

// escapeLike neutralises the wildcards a user may type, so searching for "50%"
// does not match everything.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
