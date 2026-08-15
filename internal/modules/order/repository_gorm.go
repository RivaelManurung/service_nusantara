package order

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"service_nusantara/internal/model"
)

// GormRepository is the Postgres implementation of Repository.
type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

// listRow is the flat shape the list query selects into.
//
// The list joins users and shops rather than preloading them: an operator
// paging through orders wants two names per row, and Preload would fetch two
// whole records plus their own associations for each one.
type listRow struct {
	ID            uuid.UUID
	Code          string
	Status        string
	OrderType     string
	PaymentMethod string
	CustomerID    uuid.UUID
	CustomerName  string
	ShopID        uuid.UUID
	ShopName      string
	ItemCount     int
	Total         float64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// List returns one page of orders, newest first.
func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Summary, int64, error) {
	base := r.scoped(ctx, query.Filters)

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []Summary{}, 0, nil
	}

	offset := (query.Page - 1) * query.PerPage

	var rows []listRow
	err := base.Session(&gorm.Session{}).
		Select(`orders.id,
		        orders.code,
		        orders.status,
		        orders.order_type,
		        orders.payment_method,
		        orders.user_id AS customer_id,
		        users.name     AS customer_name,
		        orders.shop_id,
		        shops.name     AS shop_name,
		        orders.total,
		        orders.created_at,
		        orders.updated_at,
		        (SELECT COALESCE(SUM(oi.quantity), 0)
		           FROM order_items oi
		          WHERE oi.order_id = orders.id) AS item_count`).
		Order("orders.created_at DESC").
		Limit(query.PerPage).
		Offset(offset).
		Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}

	now := time.Now()
	out := make([]Summary, 0, len(rows))
	for _, row := range rows {
		out = append(out, Summary{
			ID:                row.ID,
			Code:              row.Code,
			Status:            row.Status,
			OrderType:         row.OrderType,
			PaymentMethod:     row.PaymentMethod,
			CustomerID:        row.CustomerID,
			CustomerName:      row.CustomerName,
			ShopID:            row.ShopID,
			ShopName:          row.ShopName,
			ItemCount:         row.ItemCount,
			Total:             row.Total,
			CreatedAt:         row.CreatedAt,
			UpdatedAt:         row.UpdatedAt,
			StalledForMinutes: int64(now.Sub(row.UpdatedAt).Minutes()),
		})
	}
	return out, total, nil
}

// scoped builds the filtered query shared by List and its count.
func (r *GormRepository) scoped(ctx context.Context, filters Filters) *gorm.DB {
	q := r.db.WithContext(ctx).
		Model(&model.Order{}).
		Joins("LEFT JOIN users ON users.id = orders.user_id").
		Joins("LEFT JOIN shops ON shops.id = orders.shop_id").
		Where("orders.deleted_at IS NULL")

	// nil means unrestricted; an empty non-nil slice means "assigned to no
	// shop", which must return nothing rather than everything. Writing that as
	// `IN (?)` with an empty slice would produce `IN (NULL)` in some drivers, so
	// it is stated explicitly.
	if filters.ScopedShopIDs != nil {
		if len(filters.ScopedShopIDs) == 0 {
			return q.Where("1 = 0")
		}
		q = q.Where("orders.shop_id IN ?", filters.ScopedShopIDs)
	}

	if filters.Status != "" {
		q = q.Where("orders.status = ?", filters.Status)
	}
	if filters.OrderType != "" {
		q = q.Where("orders.order_type = ?", filters.OrderType)
	}
	if filters.PaymentMethod != "" {
		q = q.Where("orders.payment_method = ?", filters.PaymentMethod)
	}
	if filters.ShopID != uuid.Nil {
		q = q.Where("orders.shop_id = ?", filters.ShopID)
	}
	if filters.CustomerID != uuid.Nil {
		q = q.Where("orders.user_id = ?", filters.CustomerID)
	}
	if filters.From != nil {
		q = q.Where("orders.created_at >= ?", *filters.From)
	}
	if filters.To != nil {
		// Half-open: orders carry a timestamp, so `<= to` would drop everything
		// placed after midnight on the final day.
		q = q.Where("orders.created_at < ?", filters.To.AddDate(0, 0, 1))
	}
	if search := strings.TrimSpace(filters.Search); search != "" {
		pattern := "%" + escapeLike(search) + "%"
		q = q.Where("orders.code ILIKE ? OR users.name ILIKE ?", pattern, pattern)
	}

	return q
}

// FindByID loads one order with everything the detail screen shows.
func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Detail, error) {
	var row model.Order
	err := r.db.WithContext(ctx).
		Preload("User").
		Preload("Shop").
		Preload("CustomerAddress").
		First(&row, "orders.id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}

	detail := Detail{
		Summary: Summary{
			ID:                row.ID,
			Code:              row.Code,
			Status:            string(row.Status),
			OrderType:         string(row.OrderType),
			PaymentMethod:     string(row.PaymentMethod),
			CustomerID:        row.UserID,
			CustomerName:      row.User.Name,
			ShopID:            row.ShopID,
			ShopName:          row.Shop.Name,
			Total:             row.Total,
			CreatedAt:         row.CreatedAt,
			UpdatedAt:         row.UpdatedAt,
			StalledForMinutes: int64(time.Since(row.UpdatedAt).Minutes()),
		},
		CustomerEmail:   derefString(row.User.Email),
		CustomerPhone:   derefString(row.User.Phone),
		ShopAddress:     row.Shop.FullAddress,
		SubTotal:        row.SubTotal,
		DiscountEvent:   row.DiscountEvent,
		DiscountVoucher: row.DiscountVoucher,
		ShippingFee:     row.ShippingFee,
		Note:            derefString(row.Note),
	}

	if row.CustomerAddress != nil {
		detail.Address = &Address{
			Label: row.CustomerAddress.Label,
			Full:  row.CustomerAddress.AddressText,
			Lat:   row.CustomerAddress.Lat,
			Lng:   row.CustomerAddress.Lng,
		}
	}

	items, err := r.items(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	detail.Items = items
	for _, item := range items {
		detail.ItemCount += item.Quantity
	}

	if detail.Vouchers, err = r.vouchers(ctx, id); err != nil {
		return Detail{}, err
	}
	if detail.Events, err = r.events(ctx, id); err != nil {
		return Detail{}, err
	}

	return detail, nil
}

func (r *GormRepository) items(ctx context.Context, orderID uuid.UUID) ([]Item, error) {
	var rows []Item
	err := r.db.WithContext(ctx).
		Table("order_items").
		Select(`order_items.id,
		        order_items.product_id,
		        COALESCE(products.name, '') AS product_name,
		        COALESCE(products.code, '') AS product_code,
		        COALESCE(images.image_path, '') AS image,
		        order_items.quantity,
		        order_items.sub_total`).
		Joins("LEFT JOIN products ON products.id = order_items.product_id").
		Joins("LEFT JOIN images ON images.id = products.image_id").
		Where("order_items.order_id = ?", orderID).
		Order("order_items.created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Item{}
	}
	return rows, nil
}

func (r *GormRepository) vouchers(ctx context.Context, orderID uuid.UUID) ([]AppliedVoucher, error) {
	var rows []AppliedVoucher
	err := r.db.WithContext(ctx).
		Table("order_vouchers").
		Select(`order_vouchers.voucher_id,
		        COALESCE(vouchers.code, '')        AS code,
		        COALESCE(vouchers.description, '') AS description`).
		Joins("LEFT JOIN vouchers ON vouchers.id = order_vouchers.voucher_id").
		Where("order_vouchers.order_id = ?", orderID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []AppliedVoucher{}
	}
	return rows, nil
}

func (r *GormRepository) events(ctx context.Context, orderID uuid.UUID) ([]AppliedEvent, error) {
	var rows []AppliedEvent
	err := r.db.WithContext(ctx).
		Table("order_events").
		Select(`order_events.event_id,
		        COALESCE(events.name, '') AS name,
		        COALESCE(order_events.type, '') AS type,
		        COALESCE(order_events.discount, 0) AS discount`).
		Joins("LEFT JOIN events ON events.id = order_events.event_id").
		Where("order_events.order_id = ?", orderID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []AppliedEvent{}
	}
	return rows, nil
}

// Timeline returns the recorded transitions, newest first.
func (r *GormRepository) Timeline(ctx context.Context, orderID uuid.UUID) ([]TimelineEntry, error) {
	var rows []TimelineEntry
	err := r.db.WithContext(ctx).
		Table("order_status_histories").
		Select(`order_status_histories.id,
		        order_status_histories.from_status,
		        order_status_histories.to_status,
		        order_status_histories.reason,
		        order_status_histories.actor_id,
		        COALESCE(users.name, '') AS actor_name,
		        order_status_histories.created_at`).
		Joins("LEFT JOIN users ON users.id = order_status_histories.actor_id").
		Where("order_status_histories.order_id = ?", orderID).
		Order("order_status_histories.created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []TimelineEntry{}
	}
	return rows, nil
}

// ApplyStatus writes the transition and its audit row atomically.
//
// The UPDATE is guarded by the status the caller read, so two operators acting
// on the same order at once cannot both succeed: the second one's WHERE matches
// no row, and it is told the order moved rather than silently overwriting the
// first decision.
func (r *GormRepository) ApplyStatus(ctx context.Context, change StatusChange) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Order{}).
			Where("id = ? AND status = ? AND deleted_at IS NULL", change.OrderID, change.From).
			Updates(map[string]any{
				"status":     change.To,
				"updated_at": time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}

		history := model.OrderStatusHistory{
			OrderID:    change.OrderID,
			FromStatus: change.From,
			ToStatus:   change.To,
			Reason:     change.Reason,
		}
		if change.ActorID != uuid.Nil {
			actor := change.ActorID
			history.ActorID = &actor
		}

		return tx.Omit("Order", "Actor").Create(&history).Error
	})
}

// AssignedShopIDs lists the shops this member of staff works in.
func (r *GormRepository) AssignedShopIDs(ctx context.Context, staffID uuid.UUID) ([]uuid.UUID, error) {
	ids := []uuid.UUID{}
	err := r.db.WithContext(ctx).
		Table("shop_cashiers").
		Where("cashier_id = ? AND deleted_at IS NULL", staffID).
		Distinct().
		Pluck("shop_id", &ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// escapeLike neutralises the wildcards in a user's search term, so a query for
// "100%" does not match every code in the table.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
