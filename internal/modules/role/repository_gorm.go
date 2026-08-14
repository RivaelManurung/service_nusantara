package role

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"service_nusantara/internal/model"
)

// GormRepository is the PostgreSQL implementation of Repository.
type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Role, int64, error) {
	tx := r.db.WithContext(ctx).Model(&model.Role{})

	if search := strings.TrimSpace(query.Search); search != "" {
		// ILIKE keeps the search case-insensitive without a functional index;
		// the escape guards a literal % or _ typed by the user.
		tx = tx.Where("name ILIKE ?", "%"+escapeLike(search)+"%")
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count roles: %w", err)
	}

	var rows []model.Role
	// Roles have no created_at column, so name is the only stable ordering.
	err := tx.Order("name ASC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list roles: %w", err)
	}

	items := make([]Role, 0, len(rows))
	for _, row := range rows {
		items = append(items, toRole(row))
	}
	return items, total, nil
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Role, error) {
	var row model.Role
	if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Role{}, ErrNotFound
		}
		return Role{}, fmt.Errorf("find role: %w", err)
	}
	return toRole(row), nil
}

func (r *GormRepository) ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error) {
	tx := r.db.WithContext(ctx).Model(&model.Role{}).Where("LOWER(name) = LOWER(?)", name)
	if excludeID != uuid.Nil {
		// On update, the row being edited is not a conflict with itself.
		tx = tx.Where("id <> ?", excludeID)
	}

	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check role name: %w", err)
	}
	return count > 0, nil
}

func (r *GormRepository) Create(ctx context.Context, row Role) (Role, error) {
	record := model.Role{ID: row.ID, Name: row.Name}
	if err := r.db.WithContext(ctx).Create(&record).Error; err != nil {
		return Role{}, fmt.Errorf("create role: %w", err)
	}
	return toRole(record), nil
}

func (r *GormRepository) Update(ctx context.Context, id uuid.UUID, name string) (Role, error) {
	result := r.db.WithContext(ctx).Model(&model.Role{}).
		Where("id = ?", id).
		Update("name", name)

	if result.Error != nil {
		return Role{}, fmt.Errorf("update role: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return Role{}, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&model.Role{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("delete role: %w", result.Error)
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
		       SELECT 1 FROM users
		       WHERE role_id = ? AND deleted_at IS NULL
		     )`, id).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("check role usage: %w", err)
	}
	return exists, nil
}

func toRole(row model.Role) Role {
	return Role{ID: row.ID, Name: row.Name}
}

// escapeLike neutralises the wildcards a user may type, so searching for "50%"
// does not match everything.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
