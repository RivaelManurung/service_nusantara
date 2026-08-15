package notification

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

// activeUser is the status a usable account carries. A suspended account is
// not worth notifying, and pushing to one is a support ticket waiting to
// happen.
const activeUser = 1

// Recipients resolves an audience to account ids.
//
// A list of ids rather than a join into the insert: the same list is needed
// again to find the devices, and re-running the segment query would risk the
// two halves disagreeing if an account signed up in between.
func (r *GormRepository) Recipients(ctx context.Context, audience Audience) ([]uuid.UUID, error) {
	tx := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("status = ?", activeUser)

	switch audience.Mode {
	case AudienceUsers:
		tx = tx.Where("id IN ?", audience.UserIDs)

	case AudienceSegment:
		segment := audience.Segment

		if segment.RoleName != "" {
			// A sub-query rather than a join, so the outer SELECT keeps
			// returning one column and no duplicate rows can appear.
			roles := r.db.Model(&model.Role{}).
				Select("id").
				Where("LOWER(name) = ?", strings.ToLower(segment.RoleName))
			tx = tx.Where("role_id IN (?)", roles)
		}

		if segment.HasOrdered {
			// EXISTS rather than a join for the same reason: a customer with
			// forty orders must appear once, not forty times.
			tx = tx.Where("EXISTS (SELECT 1 FROM orders WHERE orders.user_id = users.id AND orders.deleted_at IS NULL)")
		}

		if segment.RegisteredFrom != nil {
			tx = tx.Where("users.created_at >= ?", *segment.RegisteredFrom)
		}
		if segment.RegisteredTo != nil {
			tx = tx.Where("users.created_at <= ?", *segment.RegisteredTo)
		}

	case AudienceAll:
		// Every active account. No extra clause.
	}

	var ids []uuid.UUID
	err := tx.
		Order("users.created_at ASC").
		// One past the cap, so the service can tell "a big audience" from
		// "an audience quietly truncated to look small".
		Limit(maxRecipients+1).
		Pluck("users.id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("resolve notification recipients: %w", err)
	}

	return ids, nil
}

// SearchCustomers lists candidate recipients, newest first.
//
// Only active accounts appear: offering a suspended one in the picker would
// let an operator build an audience the send then silently drops.
func (r *GormRepository) SearchCustomers(ctx context.Context, query CustomerQuery) ([]Customer, int64, error) {
	tx := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("status = ?", activeUser)

	if query.Search != "" {
		// ILIKE rather than LIKE: an operator typing "budi" expects to find
		// "Budi". The wildcards are bound as a parameter, so a search for
		// "100%" is a search, not a pattern.
		pattern := "%" + query.Search + "%"
		tx = tx.Where("name ILIKE ? OR email ILIKE ? OR phone ILIKE ?", pattern, pattern, pattern)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count customers: %w", err)
	}

	var rows []model.User
	err := tx.
		Select("id", "name", "email", "phone").
		Order("created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("search customers: %w", err)
	}

	items := make([]Customer, 0, len(rows))
	for _, row := range rows {
		customer := Customer{ID: row.ID, Name: row.Name}
		// Email and Phone are nullable: an account created by phone sign-in
		// has no email, and dereferencing blindly would panic on it.
		if row.Email != nil {
			customer.Email = *row.Email
		}
		if row.Phone != nil {
			customer.Phone = *row.Phone
		}
		items = append(items, customer)
	}

	return items, total, nil
}

// insertBatchSize bounds one INSERT. PostgreSQL allows 65535 bound parameters
// per statement and each row here binds eight, so a broadcast to twenty
// thousand customers as a single statement would be rejected outright.
const insertBatchSize = 500

// CreateMany writes one inbox row per recipient.
//
// CreateInBatches runs inside one transaction, which is what makes a partial
// broadcast impossible: a promo that reached half its audience before failing
// is not a state anybody can reason about, and the retry would then
// double-notify the first half.
func (r *GormRepository) CreateMany(ctx context.Context, messages []NewNotification) (int64, error) {
	if len(messages) == 0 {
		return 0, nil
	}

	rows := make([]model.Notification, 0, len(messages))
	for _, message := range messages {
		rows = append(rows, model.Notification{
			UserID:      message.UserID,
			Channel:     model.NotificationChannel(message.Channel),
			Title:       message.Title,
			Body:        message.Body,
			Type:        model.NotificationType(message.Type),
			TargetType:  message.TargetType,
			TargetRoute: message.TargetRoute,
			ReferenceID: message.ReferenceID,
		})
	}

	result := r.db.WithContext(ctx).CreateInBatches(&rows, insertBatchSize)
	if result.Error != nil {
		return 0, fmt.Errorf("create notifications: %w", result.Error)
	}

	return result.RowsAffected, nil
}

// RecordBroadcast files one send.
//
// Deliberately NOT inside the transaction that writes the inbox rows: the
// messages are the product, the history entry is bookkeeping, and a failure to
// record must never roll back four hundred delivered notifications. The service
// logs a failure here and still reports the send as done, because it was.
func (r *GormRepository) RecordBroadcast(ctx context.Context, broadcast Broadcast) (Broadcast, error) {
	row := model.NotificationBroadcast{
		Title:          broadcast.Title,
		Body:           broadcast.Body,
		Channel:        model.NotificationChannel(broadcast.Channel),
		Type:           model.NotificationType(broadcast.Type),
		TargetType:     broadcast.TargetType,
		TargetRoute:    broadcast.TargetRoute,
		AudienceMode:   broadcast.AudienceMode,
		RecipientCount: broadcast.RecipientCount,
		SavedCount:     broadcast.SavedCount,
		PushRequested:  broadcast.PushRequested,
		PushEnabled:    broadcast.PushEnabled,
		PushSent:       broadcast.PushSent,
		PushFailed:     broadcast.PushFailed,
		PushError:      broadcast.PushError,
		ActorID:        broadcast.ActorID,
	}

	if err := r.db.WithContext(ctx).Omit("Actor").Create(&row).Error; err != nil {
		return Broadcast{}, fmt.Errorf("record broadcast: %w", err)
	}

	broadcast.ID = row.ID
	broadcast.CreatedAt = row.CreatedAt
	return broadcast, nil
}

// ListBroadcasts returns the send history, newest first.
func (r *GormRepository) ListBroadcasts(ctx context.Context, page, perPage int) ([]Broadcast, int64, error) {
	base := r.db.WithContext(ctx).Table("notification_broadcasts")

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count broadcasts: %w", err)
	}
	if total == 0 {
		return []Broadcast{}, 0, nil
	}

	rows := []Broadcast{}
	err := base.Session(&gorm.Session{}).
		Select(`notification_broadcasts.*,
		        COALESCE(users.name, '') AS actor_name`).
		Joins("LEFT JOIN users ON users.id = notification_broadcasts.actor_id").
		Order("notification_broadcasts.created_at DESC").
		Limit(perPage).
		Offset((page - 1) * perPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list broadcasts: %w", err)
	}

	return rows, total, nil
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
