package devicetoken

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"service_nusantara/internal/model"
)

// GormRepository is the PostgreSQL implementation of Repository.
type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

// Save upserts on the token.
//
// The conflict target is the token, not (user_id, token): FCM hands the same
// registration to whoever signs in on that installation, so a second account
// on one phone must move the row rather than add a second one. Reassigning is
// what stops the previous owner from continuing to receive notifications meant
// for the person now holding the device.
//
// deleted_at is reset as part of the update: a device that signed out and back
// in again would otherwise conflict with its own soft-deleted row forever.
func (r *GormRepository) Save(ctx context.Context, registration Registration, now time.Time) (DeviceToken, error) {
	row := model.DeviceToken{
		UserID:     registration.UserID,
		Token:      registration.Token,
		Platform:   model.DevicePlatform(registration.Platform),
		AppVersion: registration.AppVersion,
		LastSeenAt: now,
	}

	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "token"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"user_id", "platform", "app_version", "last_seen_at", "updated_at", "deleted_at",
			}),
		}).
		Create(&row).Error
	if err != nil {
		return DeviceToken{}, fmt.Errorf("save device token: %w", err)
	}

	// PostgreSQL returns the row on upsert, so this is normally already filled
	// in. The reload covers the case where it is not, rather than answering
	// the client with a zero id.
	if row.ID == uuid.Nil {
		if err := r.db.WithContext(ctx).Where("token = ?", registration.Token).First(&row).Error; err != nil {
			return DeviceToken{}, fmt.Errorf("reload device token: %w", err)
		}
	}

	return toDeviceToken(row), nil
}

func (r *GormRepository) Delete(ctx context.Context, userID uuid.UUID, token string) error {
	// The owner is part of the WHERE clause rather than checked beforehand, so
	// this can never unregister another account's device.
	result := r.db.WithContext(ctx).
		Where("user_id = ? AND token = ?", userID, token).
		Delete(&model.DeviceToken{})

	if result.Error != nil {
		return fmt.Errorf("delete device token: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) ListForUser(ctx context.Context, userID uuid.UUID) ([]DeviceToken, error) {
	var rows []model.DeviceToken
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("last_seen_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list device tokens: %w", err)
	}

	items := make([]DeviceToken, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDeviceToken(row))
	}
	return items, nil
}

// Device is one deliverable address, as the notification module needs it: the
// registration plus the account it belongs to, so a per-recipient deep link
// can be built without a second lookup.
type Device struct {
	UserID uuid.UUID
	Token  string
}

// TokensFor returns every live registration of the given accounts.
//
// It is the read half of a broadcast and lives here rather than in the
// notification module so that exactly one package owns this table.
func (r *GormRepository) TokensFor(ctx context.Context, userIDs []uuid.UUID) ([]Device, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}

	var rows []model.DeviceToken
	err := r.db.WithContext(ctx).
		Select("user_id", "token").
		Where("user_id IN ?", userIDs).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load device tokens: %w", err)
	}

	devices := make([]Device, 0, len(rows))
	for _, row := range rows {
		devices = append(devices, Device{UserID: row.UserID, Token: row.Token})
	}
	return devices, nil
}

// DeleteTokens removes registrations FCM reported as gone.
//
// This is a hard delete, unlike Delete above. A soft-deleted row keeps the
// unique index occupied, and these tokens are dead: the installation is gone,
// so nothing will re-register them and the row would only block a future
// token that happened to be issued the same value.
func (r *GormRepository) DeleteTokens(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}

	err := r.db.WithContext(ctx).
		Unscoped().
		Where("token IN ?", tokens).
		Delete(&model.DeviceToken{}).Error
	if err != nil {
		return fmt.Errorf("delete stale device tokens: %w", err)
	}
	return nil
}

func toDeviceToken(row model.DeviceToken) DeviceToken {
	return DeviceToken{
		ID:         row.ID,
		Platform:   string(row.Platform),
		AppVersion: row.AppVersion,
		LastSeenAt: row.LastSeenAt,
		CreatedAt:  row.CreatedAt,
	}
}
