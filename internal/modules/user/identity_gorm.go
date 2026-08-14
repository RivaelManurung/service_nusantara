package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"service_nusantara/internal/model"
)

// GormIdentityRepository is the PostgreSQL implementation of IdentityRepository.
type GormIdentityRepository struct {
	db *gorm.DB
}

func NewGormIdentityRepository(db *gorm.DB) *GormIdentityRepository {
	return &GormIdentityRepository{db: db}
}

func (r *GormIdentityRepository) FindBySubject(ctx context.Context, provider, subject string) (Identity, error) {
	var row model.UserIdentity
	err := r.db.WithContext(ctx).
		First(&row, "provider = ? AND subject = ?", provider, subject).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Identity{}, ErrNotFound
		}
		return Identity{}, fmt.Errorf("find identity: %w", err)
	}
	return mapIdentity(row), nil
}

func (r *GormIdentityRepository) Link(ctx context.Context, identity Identity) (Identity, error) {
	row := model.UserIdentity{
		ID:       uuid.New(),
		UserID:   identity.UserID,
		Provider: identity.Provider,
		Subject:  identity.Subject,
		Email:    identity.Email,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return Identity{}, fmt.Errorf("link identity: %w", err)
	}
	return mapIdentity(row), nil
}

func (r *GormIdentityRepository) TouchLogin(ctx context.Context, id uuid.UUID, at time.Time) error {
	err := r.db.WithContext(ctx).
		Model(&model.UserIdentity{}).
		Where("id = ?", id).
		Update("last_login_at", at).Error
	if err != nil {
		return fmt.Errorf("touch identity: %w", err)
	}
	return nil
}

func (r *GormIdentityRepository) ListForUser(ctx context.Context, userID uuid.UUID) ([]Identity, error) {
	var rows []model.UserIdentity
	if err := r.db.WithContext(ctx).Find(&rows, "user_id = ?", userID).Error; err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}

	identities := make([]Identity, 0, len(rows))
	for _, row := range rows {
		identities = append(identities, mapIdentity(row))
	}
	return identities, nil
}

func mapIdentity(row model.UserIdentity) Identity {
	return Identity{
		ID:       row.ID,
		UserID:   row.UserID,
		Provider: row.Provider,
		Subject:  row.Subject,
		Email:    row.Email,
	}
}
