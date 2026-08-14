package notification

import (
	"context"
	"errors"
	"fmt"
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

// scope is the only entry point to the notifications table: it always pins the
// owner, so no query in this file can accidentally read across accounts.
func (r *GormRepository) scope(ctx context.Context, userID uuid.UUID, channel string) *gorm.DB {
	tx := r.db.WithContext(ctx).Model(&model.Notification{}).Where("user_id = ?", userID)
	if channel != "" {
		tx = tx.Where("channel = ?", channel)
	}
	return tx
}

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Notification, int64, error) {
	var total int64
	if err := r.scope(ctx, query.UserID, query.Channel).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count notifications: %w", err)
	}

	var rows []model.Notification
	err := r.scope(ctx, query.UserID, query.Channel).
		Order("created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}

	items := make([]Notification, 0, len(rows))
	for _, row := range rows {
		items = append(items, toNotification(row))
	}
	return items, total, nil
}

func (r *GormRepository) UnreadByChannel(ctx context.Context, userID uuid.UUID) (map[string]int, error) {
	type tally struct {
		Channel string
		Total   int64
	}

	var rows []tally
	err := r.scope(ctx, userID, "").
		Select("channel, COUNT(*) AS total").
		Where("read_at IS NULL").
		Group("channel").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("count unread notifications: %w", err)
	}

	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.Channel] = int(row.Total)
	}
	return counts, nil
}

func (r *GormRepository) FindByIDForUser(ctx context.Context, id, userID uuid.UUID) (Notification, error) {
	var row model.Notification
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Notification{}, ErrNotFound
		}
		return Notification{}, fmt.Errorf("find notification: %w", err)
	}
	return toNotification(row), nil
}

func (r *GormRepository) MarkRead(ctx context.Context, id, userID uuid.UUID) error {
	// The owner is part of the WHERE clause, not something checked beforehand,
	// so the update itself can never touch another account's row.
	result := r.db.WithContext(ctx).Model(&model.Notification{}).
		Where("id = ? AND user_id = ? AND read_at IS NULL", id, userID).
		Update("read_at", time.Now().UTC())

	if result.Error != nil {
		return fmt.Errorf("mark notification read: %w", result.Error)
	}
	// Zero rows means it was already read; re-reading an opened message is not
	// an error, so the caller is not told about it.
	return nil
}

func (r *GormRepository) MarkAllRead(ctx context.Context, userID uuid.UUID, channel string) (int64, error) {
	result := r.scope(ctx, userID, channel).
		Where("read_at IS NULL").
		Update("read_at", time.Now().UTC())

	if result.Error != nil {
		return 0, fmt.Errorf("mark notifications read: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func toNotification(row model.Notification) Notification {
	return Notification{
		ID:          row.ID,
		Channel:     string(row.Channel),
		Title:       row.Title,
		Body:        row.Body,
		Type:        string(row.Type),
		ReferenceID: row.ReferenceID,
		TargetType:  row.TargetType,
		TargetRoute: row.TargetRoute,
		IsRead:      row.IsRead(),
		ReadAt:      row.ReadAt,
		CreatedAt:   row.CreatedAt,
	}
}
