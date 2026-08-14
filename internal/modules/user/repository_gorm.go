package user

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

func (r *GormRepository) Create(ctx context.Context, account Account) (Account, error) {
	now := time.Now().UTC()

	row := model.User{
		ID:       account.ID,
		Name:     account.Name,
		Username: optional(account.Username),
		Email:    optional(account.Email),
		Phone:    optional(account.Phone),
		Password: optional(account.PasswordHash),
		Photo:    account.Photo,
		RoleID:   account.RoleID,
		Status:   account.Status,
	}
	if account.EmailVerified {
		row.EmailVerifiedAt = &now
	}
	if account.PhoneVerified {
		row.PhoneVerifiedAt = &now
	}

	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return Account{}, fmt.Errorf("create user: %w", err)
	}

	// Reload so the response carries the role name resolved by the database.
	return r.FindByID(ctx, row.ID)
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Account, error) {
	var row model.User
	err := r.db.WithContext(ctx).Preload("Role").First(&row, "id = ?", id).Error
	return mapUser(row, err)
}

func (r *GormRepository) FindByEmail(ctx context.Context, email string) (Account, error) {
	var row model.User
	err := r.db.WithContext(ctx).Preload("Role").First(&row, "email = ?", email).Error
	return mapUser(row, err)
}

func (r *GormRepository) FindByPhone(ctx context.Context, phone string) (Account, error) {
	var row model.User
	err := r.db.WithContext(ctx).Preload("Role").First(&row, "phone = ?", phone).Error
	return mapUser(row, err)
}

func (r *GormRepository) ExistsByUsernameOrEmail(ctx context.Context, username, email string) (bool, error) {
	// One query instead of the two round trips the previous service made, and
	// SELECT 1 rather than loading whole rows just to test for presence.
	var exists bool
	err := r.db.WithContext(ctx).
		Raw(`SELECT EXISTS (
		       SELECT 1 FROM users
		       WHERE deleted_at IS NULL AND (username = ? OR email = ?)
		     )`, username, email).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("check user uniqueness: %w", err)
	}
	return exists, nil
}

func (r *GormRepository) RoleExists(ctx context.Context, roleID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.WithContext(ctx).
		Raw(`SELECT EXISTS (SELECT 1 FROM roles WHERE id = ?)`, roleID).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("check role: %w", err)
	}
	return exists, nil
}

func (r *GormRepository) FindRoleIDByName(ctx context.Context, name string) (uuid.UUID, error) {
	var row model.Role
	err := r.db.WithContext(ctx).First(&row, "name = ?", name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("find role by name: %w", err)
	}
	return row.ID, nil
}

func (r *GormRepository) UpdatePassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	result := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", id).
		Updates(map[string]any{"password": passwordHash, "updated_at": time.Now().UTC()})

	if result.Error != nil {
		return fmt.Errorf("update password: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) MarkEmailVerified(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	err := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ? AND email_verified_at IS NULL", id).
		Update("email_verified_at", now).Error
	if err != nil {
		return fmt.Errorf("mark email verified: %w", err)
	}
	return nil
}

// FindProfile returns the read model together with its row timestamps.
func (r *GormRepository) FindProfile(ctx context.Context, id uuid.UUID) (Profile, error) {
	var row model.User
	if err := r.db.WithContext(ctx).Preload("Role").First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Profile{}, ErrNotFound
		}
		return Profile{}, fmt.Errorf("find profile: %w", err)
	}

	account, err := mapUser(row, nil)
	if err != nil {
		return Profile{}, err
	}
	return account.toProfile(row.CreatedAt), nil
}

func mapUser(row model.User, err error) (Account, error) {
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Account{}, ErrNotFound
		}
		return Account{}, fmt.Errorf("query user: %w", err)
	}

	return Account{
		ID:            row.ID,
		Name:          row.Name,
		Username:      deref(row.Username),
		Email:         deref(row.Email),
		EmailVerified: row.EmailVerifiedAt != nil,
		Phone:         deref(row.Phone),
		PhoneVerified: row.PhoneVerifiedAt != nil,
		Photo:         row.Photo,
		PasswordHash:  deref(row.Password),
		RoleID:        row.RoleID,
		RoleName:      row.Role.Name,
		Status:        row.Status,
	}, nil
}

// optional maps an empty string to NULL, so two accounts without a phone number
// do not collide on the unique index.
func optional(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
