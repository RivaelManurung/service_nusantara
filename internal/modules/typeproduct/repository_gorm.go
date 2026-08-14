package typeproduct

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

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]TypeProduct, int64, error) {
	tx := r.db.WithContext(ctx).Model(&model.TypeProduct{})

	if search := strings.TrimSpace(query.Search); search != "" {
		// ILIKE keeps the search case-insensitive without a functional index;
		// the escape guards a literal % or _ typed by the user.
		tx = tx.Where("name ILIKE ?", "%"+escapeLike(search)+"%")
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count type products: %w", err)
	}

	var rows []model.TypeProduct
	err := tx.Order("created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list type products: %w", err)
	}

	items := make([]TypeProduct, 0, len(rows))
	for _, row := range rows {
		items = append(items, toTypeProduct(row))
	}
	return items, total, nil
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (TypeProduct, error) {
	var row model.TypeProduct
	if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return TypeProduct{}, ErrNotFound
		}
		return TypeProduct{}, fmt.Errorf("find type product: %w", err)
	}
	return toTypeProduct(row), nil
}

func (r *GormRepository) ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error) {
	tx := r.db.WithContext(ctx).Model(&model.TypeProduct{}).Where("LOWER(name) = LOWER(?)", name)
	if excludeID != uuid.Nil {
		// On update, the row being edited is not a conflict with itself.
		tx = tx.Where("id <> ?", excludeID)
	}

	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check type product name: %w", err)
	}
	return count > 0, nil
}

func (r *GormRepository) Create(ctx context.Context, row TypeProduct, createdBy uuid.UUID) (TypeProduct, error) {
	record := model.TypeProduct{
		ID:            row.ID,
		Name:          row.Name,
		Image:         row.Image,
		ImagePublicID: row.ImagePublicID,
		Status:        row.Status,
		UserID:        createdBy,
	}
	if err := r.db.WithContext(ctx).Create(&record).Error; err != nil {
		return TypeProduct{}, fmt.Errorf("create type product: %w", err)
	}
	return toTypeProduct(record), nil
}

func (r *GormRepository) Update(ctx context.Context, id uuid.UUID, name, image, imagePublicID string) (TypeProduct, error) {
	updates := map[string]any{"name": name, "updated_at": time.Now().UTC()}
	// An empty image means "keep the existing one", so it must not be written.
	// The public id moves with the URL: writing one without the other would
	// leave a handle pointing at an asset the record no longer shows.
	if image != "" {
		updates["image"] = image
		updates["image_public_id"] = imagePublicID
	}

	result := r.db.WithContext(ctx).Model(&model.TypeProduct{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return TypeProduct{}, fmt.Errorf("update type product: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return TypeProduct{}, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

func (r *GormRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status int) error {
	result := r.db.WithContext(ctx).Model(&model.TypeProduct{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()})

	if result.Error != nil {
		return fmt.Errorf("update type product status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&model.TypeProduct{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete type product: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) InUse(ctx context.Context, id uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.WithContext(ctx).
		Raw(`SELECT EXISTS (
		       SELECT 1 FROM products
		       WHERE type_product_id = ? AND deleted_at IS NULL
		     )`, id).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("check type product usage: %w", err)
	}
	return exists, nil
}

func toTypeProduct(row model.TypeProduct) TypeProduct {
	return TypeProduct{
		ID:            row.ID,
		Name:          row.Name,
		Image:         row.Image,
		ImagePublicID: row.ImagePublicID,
		Status:        row.Status,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

// escapeLike neutralises the wildcards a user may type, so searching for "50%"
// does not match everything.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
