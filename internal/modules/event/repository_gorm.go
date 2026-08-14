package event

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

// The child tables. They are named here rather than inlined so the one query
// shared by the two bundle sides cannot be handed an arbitrary string.
const (
	tableBundleBuys    = "event_bundle_buys"
	tableBundleRewards = "event_bundle_rewards"
)

// GormRepository is the PostgreSQL implementation of Repository.
type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Event, int64, error) {
	tx := r.db.WithContext(ctx).Model(&model.Event{})

	if search := strings.TrimSpace(query.Search); search != "" {
		// ILIKE keeps the search case-insensitive without a functional index;
		// the escape guards a literal % or _ typed by the user.
		tx = tx.Where("name ILIKE ?", "%"+escapeLike(search)+"%")
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count events: %w", err)
	}

	var rows []model.Event
	err := tx.Order("created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list events: %w", err)
	}

	items := make([]Event, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		items = append(items, toEvent(row))
		ids = append(ids, row.ID)
	}

	// One query per child table for the whole page, rather than three per row.
	if err := r.attachChildren(ctx, items, ids); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Event, error) {
	var row model.Event
	if err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Event{}, ErrNotFound
		}
		return Event{}, fmt.Errorf("find event: %w", err)
	}

	items := []Event{toEvent(row)}
	if err := r.attachChildren(ctx, items, []uuid.UUID{row.ID}); err != nil {
		return Event{}, err
	}
	return items[0], nil
}

func (r *GormRepository) ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error) {
	tx := r.db.WithContext(ctx).Model(&model.Event{}).Where("LOWER(name) = LOWER(?)", name)
	if excludeID != uuid.Nil {
		// On update, the row being edited is not a conflict with itself.
		tx = tx.Where("id <> ?", excludeID)
	}

	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check event name: %w", err)
	}
	return count > 0, nil
}

func (r *GormRepository) ProductPrices(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	prices := map[uuid.UUID]int{}
	if len(ids) == 0 {
		return prices, nil
	}

	var rows []struct {
		ID    uuid.UUID
		Price int
	}
	err := r.db.WithContext(ctx).Model(&model.Product{}).
		Select("id", "price").
		Where("id IN ?", ids).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load product prices: %w", err)
	}

	for _, row := range rows {
		prices[row.ID] = row.Price
	}
	return prices, nil
}

func (r *GormRepository) Create(ctx context.Context, row Event, children Children, createdBy uuid.UUID) (Event, error) {
	record := model.Event{
		ID:            row.ID,
		Name:          row.Name,
		TypeEvent:     model.EventType(row.TypeEvent),
		StartDate:     row.StartDate,
		EndDate:       row.EndDate,
		Cover:         row.Cover,
		CoverPublicID: row.CoverPublicID,
		Status:        row.Status,
		CreatedBy:     createdBy,
	}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("create event: %w", err)
		}
		return insertChildren(tx, record.ID, children)
	})
	if err != nil {
		return Event{}, err
	}

	return r.FindByID(ctx, record.ID)
}

func (r *GormRepository) Update(ctx context.Context, id uuid.UUID, row Event, children Children) (Event, error) {
	updates := map[string]any{
		"name":       row.Name,
		"type_event": model.EventType(row.TypeEvent),
		"start_date": row.StartDate,
		"end_date":   row.EndDate,
		"updated_at": time.Now().UTC(),
	}
	// An empty cover means "keep the existing one", so it must not be written.
	// The public id moves with the URL: writing one without the other would
	// leave a handle pointing at an asset the record no longer shows.
	if row.Cover != "" {
		updates["cover"] = row.Cover
		updates["cover_public_id"] = row.CoverPublicID
	}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Event{}).Where("id = ?", id).Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("update event: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}

		// Replace, never append: every child table is cleared, including the
		// one belonging to the other event type, so switching DISKON to BUNDLE
		// cannot leave rows behind that would make the discount ambiguous.
		if err := deleteChildren(tx, id); err != nil {
			return err
		}
		return insertChildren(tx, id, children)
	})
	if err != nil {
		return Event{}, err
	}

	return r.FindByID(ctx, id)
}

func (r *GormRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status int) error {
	result := r.db.WithContext(ctx).Model(&model.Event{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()})

	if result.Error != nil {
		return fmt.Errorf("update event status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The children go first: the event is soft deleted, so the database
		// cascade never fires and the rows would otherwise outlive it.
		if err := deleteChildren(tx, id); err != nil {
			return err
		}

		result := tx.Delete(&model.Event{}, "id = ?", id)
		if result.Error != nil {
			return fmt.Errorf("delete event: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// insertChildren writes the rows belonging to one event.
func insertChildren(tx *gorm.DB, eventID uuid.UUID, children Children) error {
	if len(children.Products) > 0 {
		rows := make([]model.EventProduct, 0, len(children.Products))
		for _, item := range children.Products {
			rows = append(rows, model.EventProduct{
				ID:              uuid.New(),
				EventID:         eventID,
				ProductID:       item.ProductID,
				DiscountPercent: item.DiscountPercent,
				DiscountAmount:  item.DiscountAmount,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("create event products: %w", err)
		}
	}

	if len(children.BundleBuys) > 0 {
		rows := make([]model.EventBundleBuy, 0, len(children.BundleBuys))
		for _, item := range children.BundleBuys {
			rows = append(rows, model.EventBundleBuy{
				ID:        uuid.New(),
				EventID:   eventID,
				ProductID: item.ProductID,
				Quantity:  item.Quantity,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("create event bundle buys: %w", err)
		}
	}

	if len(children.BundleRewards) > 0 {
		rows := make([]model.EventBundleReward, 0, len(children.BundleRewards))
		for _, item := range children.BundleRewards {
			rows = append(rows, model.EventBundleReward{
				ID:        uuid.New(),
				EventID:   eventID,
				ProductID: item.ProductID,
				Quantity:  item.Quantity,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("create event bundle rewards: %w", err)
		}
	}

	return nil
}

// deleteChildren removes every child row of one event. The delete is unscoped:
// these are join rows with no history worth keeping, and soft deleting them
// would grow the tables without bound every time an event is edited.
func deleteChildren(tx *gorm.DB, eventID uuid.UUID) error {
	if err := tx.Unscoped().Where("event_id = ?", eventID).Delete(&model.EventProduct{}).Error; err != nil {
		return fmt.Errorf("clear event products: %w", err)
	}
	if err := tx.Unscoped().Where("event_id = ?", eventID).Delete(&model.EventBundleBuy{}).Error; err != nil {
		return fmt.Errorf("clear event bundle buys: %w", err)
	}
	if err := tx.Unscoped().Where("event_id = ?", eventID).Delete(&model.EventBundleReward{}).Error; err != nil {
		return fmt.Errorf("clear event bundle rewards: %w", err)
	}
	return nil
}

// attachChildren fills in the child rows of every event in items.
func (r *GormRepository) attachChildren(ctx context.Context, items []Event, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}

	discounts, err := r.discountsByEvent(ctx, ids)
	if err != nil {
		return err
	}
	buys, err := r.bundleItemsByEvent(ctx, tableBundleBuys, ids)
	if err != nil {
		return err
	}
	rewards, err := r.bundleItemsByEvent(ctx, tableBundleRewards, ids)
	if err != nil {
		return err
	}

	// Only overwrite when rows were found, so an event without children keeps
	// the empty slice and serialises as [] rather than null.
	for i := range items {
		if rows, ok := discounts[items[i].ID]; ok {
			items[i].EventProducts = rows
		}
		if rows, ok := buys[items[i].ID]; ok {
			items[i].EventBundleBuys = rows
		}
		if rows, ok := rewards[items[i].ID]; ok {
			items[i].EventBundleRewards = rows
		}
	}
	return nil
}

// childScan is one joined child row, shared by the discount and bundle queries.
type childScan struct {
	ID              uuid.UUID
	EventID         uuid.UUID
	DiscountPercent int
	DiscountAmount  float64
	Quantity        int
	ProductID       uuid.UUID
	Name            string
	Code            string
	Price           int
	Unit            string
	ImagePath       string
}

func (c childScan) product() Product {
	return Product{
		ID:    c.ProductID,
		Name:  c.Name,
		Code:  c.Code,
		Price: c.Price,
		Unit:  c.Unit,
		Image: c.ImagePath,
	}
}

func (r *GormRepository) discountsByEvent(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]ProductDiscount, error) {
	var rows []childScan
	err := r.db.WithContext(ctx).Raw(`
		SELECT c.id, c.event_id, c.discount_percent, c.discount_amount,
		       p.id AS product_id, p.name, p.code, p.price, p.unit,
		       COALESCE(i.image_path, '') AS image_path
		FROM event_products c
		JOIN products p ON p.id = c.product_id AND p.deleted_at IS NULL
		LEFT JOIN images i ON i.id = p.image_id
		WHERE c.event_id IN ? AND c.deleted_at IS NULL
		ORDER BY c.created_at`, ids).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load event products: %w", err)
	}

	grouped := make(map[uuid.UUID][]ProductDiscount, len(ids))
	for _, row := range rows {
		grouped[row.EventID] = append(grouped[row.EventID], ProductDiscount{
			ID:              row.ID,
			Product:         row.product(),
			DiscountPercent: row.DiscountPercent,
			DiscountAmount:  row.DiscountAmount,
		})
	}
	return grouped, nil
}

// bundleItemsByEvent reads one of the two bundle tables. The table name comes
// from a package constant, never from a request.
func (r *GormRepository) bundleItemsByEvent(ctx context.Context, table string, ids []uuid.UUID) (map[uuid.UUID][]BundleItem, error) {
	var rows []childScan
	query := fmt.Sprintf(`
		SELECT c.id, c.event_id, c.quantity,
		       p.id AS product_id, p.name, p.code, p.price, p.unit,
		       COALESCE(i.image_path, '') AS image_path
		FROM %s c
		JOIN products p ON p.id = c.product_id AND p.deleted_at IS NULL
		LEFT JOIN images i ON i.id = p.image_id
		WHERE c.event_id IN ? AND c.deleted_at IS NULL
		ORDER BY c.created_at`, table)

	if err := r.db.WithContext(ctx).Raw(query, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load %s: %w", table, err)
	}

	grouped := make(map[uuid.UUID][]BundleItem, len(ids))
	for _, row := range rows {
		grouped[row.EventID] = append(grouped[row.EventID], BundleItem{
			ID:       row.ID,
			Product:  row.product(),
			Quantity: row.Quantity,
		})
	}
	return grouped, nil
}

func toEvent(row model.Event) Event {
	return Event{
		ID:                 row.ID,
		Name:               row.Name,
		TypeEvent:          Type(row.TypeEvent),
		StartDate:          row.StartDate,
		EndDate:            row.EndDate,
		Cover:              row.Cover,
		CoverPublicID:      row.CoverPublicID,
		Status:             row.Status,
		EventProducts:      []ProductDiscount{},
		EventBundleBuys:    []BundleItem{},
		EventBundleRewards: []BundleItem{},
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

// escapeLike neutralises the wildcards a user may type, so searching for "50%"
// does not match everything.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
