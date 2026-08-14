package banner

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

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Banner, int64, error) {
	tx := r.db.WithContext(ctx).Model(&model.Banner{})

	if search := strings.TrimSpace(query.Search); search != "" {
		// ILIKE keeps the search case-insensitive without a functional index;
		// the escape guards a literal % or _ typed by the user.
		tx = tx.Where("name ILIKE ?", "%"+escapeLike(search)+"%")
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count banners: %w", err)
	}

	var rows []model.Banner
	err := tx.Order("created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list banners: %w", err)
	}

	return toBanners(rows), total, nil
}

func (r *GormRepository) ListActive(ctx context.Context) ([]Banner, error) {
	var rows []model.Banner
	err := r.db.WithContext(ctx).
		Where("status = ?", StatusActive).
		Order("created_at DESC").
		// The storefront carousel is a fixed-size strip, not a page, but the
		// query is still bounded so a runaway table cannot be dumped in one call.
		Limit(maxPublicBanners).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list active banners: %w", err)
	}
	return toBanners(rows), nil
}

// maxPublicBanners bounds the unauthenticated storefront feed.
const maxPublicBanners = 50

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Banner, error) {
	var row model.Banner
	if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return Banner{}, wrapFind(err)
	}
	return toBanner(row), nil
}

func (r *GormRepository) FindActiveByID(ctx context.Context, id uuid.UUID) (Banner, error) {
	var row model.Banner
	err := r.db.WithContext(ctx).First(&row, "id = ? AND status = ?", id, StatusActive).Error
	if err != nil {
		return Banner{}, wrapFind(err)
	}
	return toBanner(row), nil
}

func (r *GormRepository) ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error) {
	tx := r.db.WithContext(ctx).Model(&model.Banner{}).Where("LOWER(name) = LOWER(?)", name)
	if excludeID != uuid.Nil {
		// On update, the row being edited is not a conflict with itself.
		tx = tx.Where("id <> ?", excludeID)
	}

	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check banner name: %w", err)
	}
	return count > 0, nil
}

func (r *GormRepository) Create(ctx context.Context, row Banner, createdBy uuid.UUID) (Banner, error) {
	record := model.Banner{
		ID:            row.ID,
		Name:          row.Name,
		Photo:         row.Photo,
		PhotoPublicID: row.PhotoPublicID,
		Description:   row.Description,
		Status:        row.Status,
		UserID:        createdBy,
	}
	if err := r.db.WithContext(ctx).Create(&record).Error; err != nil {
		return Banner{}, fmt.Errorf("create banner: %w", err)
	}
	return toBanner(record), nil
}

func (r *GormRepository) Update(ctx context.Context, id uuid.UUID, name, description, photo, photoPublicID string) (Banner, error) {
	updates := map[string]any{
		"name":        name,
		"description": description,
		"updated_at":  time.Now().UTC(),
	}
	// An empty photo means "keep the existing one", so it must not be written.
	// The public id moves with the URL: writing one without the other would
	// leave a handle pointing at an asset the record no longer shows.
	if photo != "" {
		updates["photo"] = photo
		updates["photo_public_id"] = photoPublicID
	}

	result := r.db.WithContext(ctx).Model(&model.Banner{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return Banner{}, fmt.Errorf("update banner: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return Banner{}, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

func (r *GormRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status int) error {
	result := r.db.WithContext(ctx).Model(&model.Banner{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()})

	if result.Error != nil {
		return fmt.Errorf("update banner status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&model.Banner{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete banner: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func wrapFind(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("find banner: %w", err)
}

func toBanners(rows []model.Banner) []Banner {
	items := make([]Banner, 0, len(rows))
	for _, row := range rows {
		items = append(items, toBanner(row))
	}
	return items
}

func toBanner(row model.Banner) Banner {
	return Banner{
		ID:            row.ID,
		Name:          row.Name,
		Photo:         row.Photo,
		PhotoPublicID: row.PhotoPublicID,
		Description:   row.Description,
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
