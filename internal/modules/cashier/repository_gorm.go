package cashier

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

// scoped restricts a query to accounts holding the cashier role, so no endpoint
// in this module can read or write a superadmin by guessing an id.
func (r *GormRepository) scoped(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Model(&model.User{}).
		Joins("JOIN roles ON roles.id = users.role_id").
		Where("roles.name = ?", RoleName)
}

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Cashier, int64, error) {
	tx := r.scoped(ctx)

	if search := strings.TrimSpace(query.Search); search != "" {
		// The column is qualified because `roles` is joined in and also has a
		// `name`. ILIKE keeps the search case-insensitive; the escape guards a
		// literal % or _ typed by the user.
		pattern := "%" + escapeLike(search) + "%"
		// The parentheses are explicit so the OR cannot escape the role filter.
		tx = tx.Where("(users.name ILIKE ? OR users.username ILIKE ? OR users.email ILIKE ?)",
			pattern, pattern, pattern)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count cashiers: %w", err)
	}

	var rows []model.User
	err := tx.Preload("Role").
		Order("users.created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list cashiers: %w", err)
	}

	items := make([]Cashier, 0, len(rows))
	for _, row := range rows {
		items = append(items, toCashier(row))
	}
	return items, total, nil
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Cashier, error) {
	var row model.User
	err := r.scoped(ctx).Preload("Role").Where("users.id = ?", id).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Cashier{}, ErrNotFound
		}
		return Cashier{}, fmt.Errorf("find cashier: %w", err)
	}
	return toCashier(row), nil
}

// ExistsByUsername checks the whole users table, not just cashiers: the unique
// index is global, so colliding with a customer is still a collision.
func (r *GormRepository) ExistsByUsername(ctx context.Context, username string, excludeID uuid.UUID) (bool, error) {
	return r.exists(ctx, "username", username, excludeID)
}

func (r *GormRepository) ExistsByEmail(ctx context.Context, email string, excludeID uuid.UUID) (bool, error) {
	return r.exists(ctx, "email", email, excludeID)
}

func (r *GormRepository) exists(ctx context.Context, column, value string, excludeID uuid.UUID) (bool, error) {
	// column is a package-local literal, never caller input.
	tx := r.db.WithContext(ctx).Model(&model.User{}).
		Where("LOWER("+column+") = LOWER(?)", value)
	if excludeID != uuid.Nil {
		tx = tx.Where("id <> ?", excludeID)
	}

	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check cashier %s: %w", column, err)
	}
	return count > 0, nil
}

func (r *GormRepository) FindRoleIDByName(ctx context.Context, name string) (uuid.UUID, error) {
	var row model.Role
	if err := r.db.WithContext(ctx).First(&row, "name = ?", name).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return uuid.Nil, ErrRoleMissing
		}
		return uuid.Nil, fmt.Errorf("find role by name: %w", err)
	}
	return row.ID, nil
}

func (r *GormRepository) Create(ctx context.Context, row Cashier, passwordHash string, roleID uuid.UUID) (Cashier, error) {
	username := row.Username
	email := row.Email

	record := model.User{
		ID:            row.ID,
		Name:          row.Name,
		Username:      &username,
		Email:         &email,
		Password:      &passwordHash,
		Photo:         row.Photo,
		PhotoPublicID: row.PhotoPublicID,
		RoleID:        roleID,
		Status:        row.Status,
	}
	if err := r.db.WithContext(ctx).Create(&record).Error; err != nil {
		return Cashier{}, fmt.Errorf("create cashier: %w", err)
	}

	// Reload so the response carries the role name resolved by the database.
	return r.FindByID(ctx, record.ID)
}

func (r *GormRepository) Update(ctx context.Context, id uuid.UUID, name, username, photo, photoPublicID string, status *int) (Cashier, error) {
	updates := map[string]any{"updated_at": time.Now().UTC()}
	if name != "" {
		updates["name"] = name
	}
	if username != "" {
		updates["username"] = username
	}
	// An empty photo means "keep the existing one", so it must not be written.
	// The public id moves with the URL: writing one without the other would
	// leave a handle pointing at an asset the record no longer shows.
	if photo != "" {
		updates["photo"] = photo
		updates["photo_public_id"] = photoPublicID
	}
	if status != nil {
		updates["status"] = *status
	}

	// The role is re-checked here rather than trusted from an earlier read, so a
	// concurrent role change cannot be used to edit a non-cashier account.
	result := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ? AND role_id IN (SELECT id FROM roles WHERE name = ?)", id, RoleName).
		Updates(updates)

	if result.Error != nil {
		return Cashier{}, fmt.Errorf("update cashier: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return Cashier{}, ErrNotFound
	}
	return r.FindByID(ctx, id)
}

func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("id = ? AND role_id IN (SELECT id FROM roles WHERE name = ?)", id, RoleName).
		Delete(&model.User{})

	if result.Error != nil {
		return fmt.Errorf("delete cashier: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func toCashier(row model.User) Cashier {
	return Cashier{
		ID:            row.ID,
		Name:          row.Name,
		Username:      deref(row.Username),
		Email:         deref(row.Email),
		Photo:         row.Photo,
		PhotoPublicID: row.PhotoPublicID,
		Status:        row.Status,
		Role:          row.Role.Name,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// escapeLike neutralises the wildcards a user may type, so searching for "50%"
// does not match everything.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
