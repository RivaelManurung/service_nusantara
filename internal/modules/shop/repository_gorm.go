package shop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"service_nusantara/internal/model"
)

// staffRoles are the roles allowed to be assigned to a shop and therefore to
// read it through the /cashier endpoints.
var staffRoles = []string{"admin", "cashier"}

// GormRepository is the PostgreSQL implementation of Repository.
type GormRepository struct {
	db *gorm.DB
}

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Shop, int64, error) {
	tx := r.db.WithContext(ctx).Model(&model.Shop{})

	if search := strings.TrimSpace(query.Search); search != "" {
		// ILIKE keeps the search case-insensitive; the escape stops a literal
		// % or _ typed by the user from matching everything.
		tx = tx.Where("name ILIKE ?", "%"+escapeLike(search)+"%")
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count shops: %w", err)
	}

	var rows []model.Shop
	err := tx.Preload("Creator").
		Order("created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list shops: %w", err)
	}

	items, err := r.hydrate(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Shop, error) {
	var row model.Shop
	err := r.db.WithContext(ctx).Preload("Creator").First(&row, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Shop{}, ErrNotFound
		}
		return Shop{}, fmt.Errorf("find shop: %w", err)
	}

	items, err := r.hydrate(ctx, []model.Shop{row})
	if err != nil {
		return Shop{}, err
	}
	return items[0], nil
}

func (r *GormRepository) Create(ctx context.Context, record Record, assign Assignments) (Shop, error) {
	id := uuid.New()

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := model.Shop{
			ID:            id,
			Name:          record.Name,
			Cover:         record.Cover,
			CoverPublicID: record.CoverPublicID,
			Description:   record.Description,
			FullAddress:   record.FullAddress,
			Status:        record.Status,
			CreatedBy:     record.CreatedBy,
		}
		if record.Lat != nil {
			row.Lat = *record.Lat
		}
		if record.Lng != nil {
			row.Lng = *record.Lng
		}

		// Associations are omitted so a zero-valued Creator cannot be written
		// back over the real user row.
		if err := tx.Omit(clause.Associations).Create(&row).Error; err != nil {
			return fmt.Errorf("create shop: %w", err)
		}
		return writeAssignments(tx, id, record.Name, assign)
	})
	if err != nil {
		return Shop{}, err
	}

	return r.FindByID(ctx, id)
}

func (r *GormRepository) Update(ctx context.Context, id uuid.UUID, record Record, assign Assignments) (Shop, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"updated_at": time.Now().UTC()}
		// An empty value means "keep what is stored", so it must not be written.
		if record.Name != "" {
			updates["name"] = record.Name
		}
		if record.Description != "" {
			updates["description"] = record.Description
		}
		if record.FullAddress != "" {
			updates["full_address"] = record.FullAddress
		}
		if record.Lat != nil {
			updates["lat"] = *record.Lat
		}
		if record.Lng != nil {
			updates["lng"] = *record.Lng
		}
		// The public id moves with the URL: writing one without the other would
		// leave a handle pointing at an asset the record no longer shows.
		if record.Cover != "" {
			updates["cover"] = record.Cover
			updates["cover_public_id"] = record.CoverPublicID
		}

		result := tx.Model(&model.Shop{}).Where("id = ?", id).Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("update shop: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}

		name := record.Name
		if name == "" {
			// The alt text falls back to whatever the row already carries.
			var stored model.Shop
			if err := tx.Select("name").First(&stored, "id = ?", id).Error; err == nil {
				name = stored.Name
			}
		}
		return writeAssignments(tx, id, name, assign)
	})
	if err != nil {
		return Shop{}, err
	}

	return r.FindByID(ctx, id)
}

func (r *GormRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status int) error {
	result := r.db.WithContext(ctx).Model(&model.Shop{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()})

	if result.Error != nil {
		return fmt.Errorf("update shop status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete soft-deletes the shop together with the rows that only exist to
// describe it, so a re-created shop does not inherit the old gallery.
func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&model.Shop{}, "id = ?", id)
		if result.Error != nil {
			return fmt.Errorf("delete shop: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}

		if err := tx.Where("shop_id = ?", id).Delete(&model.ShopImage{}).Error; err != nil {
			return fmt.Errorf("delete shop gallery: %w", err)
		}
		if err := tx.Where("shop_id = ?", id).Delete(&model.ShopCashier{}).Error; err != nil {
			return fmt.Errorf("delete shop cashiers: %w", err)
		}
		if err := tx.Where("shop_id = ?", id).Delete(&model.ShopProducts{}).Error; err != nil {
			return fmt.Errorf("delete shop products: %w", err)
		}
		return nil
	})
}

func (r *GormRepository) HasOrders(ctx context.Context, id uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.WithContext(ctx).
		Raw(`SELECT EXISTS (
		       SELECT 1 FROM orders
		       WHERE shop_id = ? AND deleted_at IS NULL
		     )`, id).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("check shop orders: %w", err)
	}
	return exists, nil
}

func (r *GormRepository) StaffIDs(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var found []uuid.UUID
	err := r.db.WithContext(ctx).
		Table("users").
		Joins("JOIN roles ON roles.id = users.role_id").
		Where("users.id IN ?", ids).
		Where("users.deleted_at IS NULL").
		Where("roles.name IN ?", staffRoles).
		Pluck("users.id", &found).Error
	if err != nil {
		return nil, fmt.Errorf("verify cashiers: %w", err)
	}
	return found, nil
}

func (r *GormRepository) ProductDefaults(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]ProductDefaults, error) {
	defaults := map[uuid.UUID]ProductDefaults{}
	if len(ids) == 0 {
		return defaults, nil
	}

	var rows []struct {
		ID     uuid.UUID
		Price  float64
		Status int
	}
	err := r.db.WithContext(ctx).
		Model(&model.Product{}).
		Select("id", "price", "status").
		Where("id IN ?", ids).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load product defaults: %w", err)
	}

	for _, row := range rows {
		defaults[row.ID] = ProductDefaults{Price: row.Price, Status: row.Status}
	}
	return defaults, nil
}

func (r *GormRepository) AssignedShops(ctx context.Context, staffID uuid.UUID) ([]AssignedShop, error) {
	shops := []AssignedShop{}
	err := r.db.WithContext(ctx).
		Table("shops").
		Select("shops.id", "shops.name").
		Joins("JOIN shop_cashiers ON shop_cashiers.shop_id = shops.id").
		Where("shops.deleted_at IS NULL").
		Where("shop_cashiers.cashier_id = ?", staffID).
		Where("shop_cashiers.deleted_at IS NULL").
		Order("shop_cashiers.created_at ASC").
		Scan(&shops).Error
	if err != nil {
		return nil, fmt.Errorf("list assigned shops: %w", err)
	}
	return shops, nil
}

func (r *GormRepository) IsAssigned(ctx context.Context, staffID, shopID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.WithContext(ctx).
		Raw(`SELECT EXISTS (
		       SELECT 1 FROM shop_cashiers
		       JOIN shops ON shops.id = shop_cashiers.shop_id
		       WHERE shop_cashiers.shop_id = ?
		         AND shop_cashiers.cashier_id = ?
		         AND shop_cashiers.deleted_at IS NULL
		         AND shops.deleted_at IS NULL
		     )`, shopID, staffID).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("check shop assignment: %w", err)
	}
	return exists, nil
}

func (r *GormRepository) AssignedShop(ctx context.Context, staffID, shopID uuid.UUID) (CashierShop, error) {
	var row model.Shop
	// The join is the authorisation: a shop the caller is not assigned to
	// simply does not exist as far as this query is concerned.
	err := r.db.WithContext(ctx).
		Model(&model.Shop{}).
		Select("shops.*").
		Joins("JOIN shop_cashiers ON shop_cashiers.shop_id = shops.id AND shop_cashiers.deleted_at IS NULL").
		Where("shops.id = ?", shopID).
		Where("shop_cashiers.cashier_id = ?", staffID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CashierShop{}, ErrNotFound
		}
		return CashierShop{}, fmt.Errorf("find assigned shop: %w", err)
	}

	gallery, err := r.galleryByShop(ctx, []uuid.UUID{shopID})
	if err != nil {
		return CashierShop{}, err
	}

	return CashierShop{
		ID:          row.ID,
		Name:        row.Name,
		Cover:       row.Cover,
		Description: row.Description,
		FullAddress: row.FullAddress,
		Lat:         row.Lat,
		Lng:         row.Lng,
		Status:      row.Status,
		ShopImages:  galleryURLs(gallery[shopID]),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}, nil
}

func (r *GormRepository) AssignedShopProducts(ctx context.Context, staffID, shopID uuid.UUID) ([]CashierProduct, error) {
	var rows []model.ShopProducts
	err := r.db.WithContext(ctx).
		Model(&model.ShopProducts{}).
		Joins("JOIN shop_cashiers ON shop_cashiers.shop_id = shop_products.shop_id AND shop_cashiers.deleted_at IS NULL").
		Where("shop_products.shop_id = ?", shopID).
		Where("shop_cashiers.cashier_id = ?", staffID).
		Preload("Product").
		Preload("Product.Image").
		Preload("Product.TypeProduct").
		Preload("Product.ProductImages.Image").
		Order("shop_products.created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list assigned shop products: %w", err)
	}

	items := make([]CashierProduct, 0, len(rows))
	for _, row := range rows {
		product := row.Product

		images := make([]string, 0, len(product.ProductImages))
		for _, image := range product.ProductImages {
			images = append(images, image.Image.ImagePath)
		}

		items = append(items, CashierProduct{
			ID:            product.ID,
			Name:          product.Name,
			Image:         product.Image.ImagePath,
			Code:          product.Code,
			Price:         row.Price,
			Unit:          product.Unit,
			Stock:         row.Stock,
			Description:   product.Description,
			Status:        row.Status,
			TypeProduct:   product.TypeProduct.Name,
			ProductImages: images,
			CreatedAt:     row.CreatedAt,
			UpdatedAt:     row.UpdatedAt,
		})
	}
	return items, nil
}

// hydrate attaches the gallery, stock and staff to a page of shop rows.
//
// The three relations are fetched once for the whole page rather than per row,
// because the previous service's per-shop preloads turned a 20-row list into
// dozens of round trips.
func (r *GormRepository) hydrate(ctx context.Context, rows []model.Shop) ([]Shop, error) {
	items := make([]Shop, 0, len(rows))
	if len(rows) == 0 {
		return items, nil
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}

	gallery, err := r.galleryByShop(ctx, ids)
	if err != nil {
		return nil, err
	}
	products, err := r.productsByShop(ctx, ids)
	if err != nil {
		return nil, err
	}
	cashiers, err := r.cashiersByShop(ctx, ids)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		items = append(items, Shop{
			ID:           row.ID,
			Name:         row.Name,
			Cover:        row.Cover,
			Description:  row.Description,
			FullAddress:  row.FullAddress,
			Lat:          row.Lat,
			Lng:          row.Lng,
			Status:       row.Status,
			CreatedBy:    row.Creator.Name,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
			ShopImages:   galleryURLs(gallery[row.ID]),
			ShopProducts: orEmptyProducts(products[row.ID]),
			ShopCashiers: orEmptyCashiers(cashiers[row.ID]),

			CoverPublicID:    row.CoverPublicID,
			GalleryPublicIDs: publicIDs(gallery[row.ID]),
		})
	}
	return items, nil
}

// galleryByShop reads the gallery of every listed shop. The public id is read
// alongside the URL so a caller that replaces or deletes the gallery knows what
// to remove from the storage provider -- once the join rows are gone, nothing
// else records the handles.
func (r *GormRepository) galleryByShop(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]GalleryImage, error) {
	var rows []struct {
		ShopID    uuid.UUID
		ImagePath string
		PublicID  string
	}
	err := r.db.WithContext(ctx).
		Table("shop_images").
		Select("shop_images.shop_id", "images.image_path", "images.public_id").
		Joins("JOIN images ON images.id = shop_images.image_id").
		Where("shop_images.shop_id IN ?", ids).
		Where("shop_images.deleted_at IS NULL").
		Order("shop_images.created_at ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load shop gallery: %w", err)
	}

	gallery := map[uuid.UUID][]GalleryImage{}
	for _, row := range rows {
		gallery[row.ShopID] = append(gallery[row.ShopID],
			GalleryImage{URL: row.ImagePath, PublicID: row.PublicID})
	}
	return gallery, nil
}

// galleryURLs is the client-facing half of a gallery, kept non-nil so an empty
// gallery renders as [] rather than null.
func galleryURLs(images []GalleryImage) []string {
	urls := make([]string, 0, len(images))
	for _, image := range images {
		urls = append(urls, image.URL)
	}
	return urls
}

func (r *GormRepository) productsByShop(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]ShopProduct, error) {
	var rows []model.ShopProducts
	err := r.db.WithContext(ctx).
		Where("shop_id IN ?", ids).
		Preload("Product").
		Preload("Product.Image").
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load shop products: %w", err)
	}

	products := map[uuid.UUID][]ShopProduct{}
	for _, row := range rows {
		item := ShopProduct{
			ID:     row.ProductID,
			Name:   row.Product.Name,
			Code:   row.Product.Code,
			Price:  row.Price,
			Stock:  row.Stock,
			Status: row.Status,
		}
		if path := row.Product.Image.ImagePath; path != "" {
			item.Image = &Image{ImagePath: path}
		}
		products[row.ShopID] = append(products[row.ShopID], item)
	}
	return products, nil
}

func (r *GormRepository) cashiersByShop(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]Cashier, error) {
	var rows []struct {
		ShopID   uuid.UUID
		ID       uuid.UUID
		Name     string
		Username *string
		Email    *string
		Photo    string
		Status   int
	}
	err := r.db.WithContext(ctx).
		Table("shop_cashiers").
		Select("shop_cashiers.shop_id, users.id, users.name, users.username, users.email, users.photo, users.status").
		Joins("JOIN users ON users.id = shop_cashiers.cashier_id").
		Where("shop_cashiers.shop_id IN ?", ids).
		Where("shop_cashiers.deleted_at IS NULL").
		Where("users.deleted_at IS NULL").
		Order("shop_cashiers.created_at ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load shop cashiers: %w", err)
	}

	cashiers := map[uuid.UUID][]Cashier{}
	for _, row := range rows {
		cashiers[row.ShopID] = append(cashiers[row.ShopID], Cashier{
			ID:       row.ID,
			Name:     row.Name,
			Username: deref(row.Username),
			Email:    deref(row.Email),
			Photo:    row.Photo,
			Status:   row.Status,
		})
	}
	return cashiers, nil
}

// writeAssignments replaces the gallery, staff and stock of one shop inside the
// caller's transaction. Each relation is only touched when the request asked
// for it, so a partial edit cannot silently wipe the rest.
func writeAssignments(tx *gorm.DB, shopID uuid.UUID, altText string, assign Assignments) error {
	if assign.ReplaceGallery {
		if err := tx.Where("shop_id = ?", shopID).Delete(&model.ShopImage{}).Error; err != nil {
			return fmt.Errorf("clear shop gallery: %w", err)
		}
	}
	for _, picture := range assign.Gallery {
		image := model.Image{ID: uuid.New(), ImagePath: picture.URL, PublicID: picture.PublicID}
		if err := tx.Omit(clause.Associations).Create(&image).Error; err != nil {
			return fmt.Errorf("create gallery image: %w", err)
		}
		link := model.ShopImage{
			ID:      uuid.New(),
			ShopID:  shopID,
			ImageID: image.ID,
			Altext:  altText,
		}
		if err := tx.Omit(clause.Associations).Create(&link).Error; err != nil {
			return fmt.Errorf("attach gallery image: %w", err)
		}
	}

	if assign.SetCashiers {
		// Replace, never append: the form sends the complete list it wants.
		if err := tx.Where("shop_id = ?", shopID).Delete(&model.ShopCashier{}).Error; err != nil {
			return fmt.Errorf("clear shop cashiers: %w", err)
		}
		rows := make([]model.ShopCashier, 0, len(assign.Cashiers))
		for _, cashierID := range assign.Cashiers {
			rows = append(rows, model.ShopCashier{
				ID:         uuid.New(),
				ShopID:     shopID,
				CashierID:  cashierID,
				AssignedAt: time.Now().UTC(),
			})
		}
		if len(rows) > 0 {
			if err := tx.Omit(clause.Associations).Create(&rows).Error; err != nil {
				return fmt.Errorf("assign shop cashiers: %w", err)
			}
		}
	}

	if assign.SetProducts {
		if err := tx.Where("shop_id = ?", shopID).Delete(&model.ShopProducts{}).Error; err != nil {
			return fmt.Errorf("clear shop products: %w", err)
		}
		rows := make([]model.ShopProducts, 0, len(assign.Products))
		for _, line := range assign.Products {
			rows = append(rows, model.ShopProducts{
				ID:         uuid.New(),
				ShopID:     shopID,
				ProductID:  line.ProductID,
				Price:      line.Price,
				Stock:      line.Stock,
				Status:     line.Status,
				AssignedAt: time.Now().UTC(),
			})
		}
		if len(rows) > 0 {
			if err := tx.Omit(clause.Associations).Create(&rows).Error; err != nil {
				return fmt.Errorf("assign shop products: %w", err)
			}
		}
	}

	return nil
}

// escapeLike neutralises the wildcards a user may type, so searching for "50%"
// does not match everything.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// The orEmpty helpers keep an absent relation rendering as [] rather than null,
// which is what the web client's `?? []` fallbacks assume but the mobile
// clients do not tolerate.
func orEmptyProducts(values []ShopProduct) []ShopProduct {
	if values == nil {
		return []ShopProduct{}
	}
	return values
}

func orEmptyCashiers(values []Cashier) []Cashier {
	if values == nil {
		return []Cashier{}
	}
	return values
}
