package product

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

// withRelations loads exactly the associations the response shape needs.
// The previous service also preloaded the category's owner and both users'
// roles, none of which the client reads, at the cost of four extra queries per
// page.
func withRelations(tx *gorm.DB) *gorm.DB {
	return tx.
		Preload("Image").
		Preload("TypeProduct").
		Preload("User").
		Preload("ProductImages", func(db *gorm.DB) *gorm.DB {
			return db.Order("product_images.created_at ASC")
		}).
		Preload("ProductImages.Image")
}

func (r *GormRepository) List(ctx context.Context, query ListQuery) ([]Product, int64, error) {
	tx := r.db.WithContext(ctx).Model(&model.Product{})

	if search := strings.TrimSpace(query.Search); search != "" {
		// ILIKE keeps the search case-insensitive without a functional index;
		// the escape guards a literal % or _ typed by the user. Code is
		// searched too because the catalogue screen looks products up by SKU.
		pattern := "%" + escapeLike(search) + "%"
		tx = tx.Where("name ILIKE ? OR code ILIKE ?", pattern, pattern)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count products: %w", err)
	}

	var rows []model.Product
	err := withRelations(tx).
		Order("created_at DESC").
		Limit(query.PerPage).
		Offset((query.Page - 1) * query.PerPage).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list products: %w", err)
	}

	items := make([]Product, 0, len(rows))
	for _, row := range rows {
		items = append(items, toProduct(row))
	}
	return items, total, nil
}

func (r *GormRepository) FindByID(ctx context.Context, id uuid.UUID) (Product, error) {
	var row model.Product
	if err := withRelations(r.db.WithContext(ctx)).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Product{}, ErrNotFound
		}
		return Product{}, fmt.Errorf("find product: %w", err)
	}
	return toProduct(row), nil
}

func (r *GormRepository) ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error) {
	tx := r.db.WithContext(ctx).Model(&model.Product{}).Where("LOWER(name) = LOWER(?)", name)
	if excludeID != uuid.Nil {
		// On update, the row being edited is not a conflict with itself.
		tx = tx.Where("id <> ?", excludeID)
	}

	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check product name: %w", err)
	}
	return count > 0, nil
}

func (r *GormRepository) TypeProductExists(ctx context.Context, id uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.TypeProduct{}).Where("id = ?", id).Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check type product: %w", err)
	}
	return count > 0, nil
}

// Create writes the cover image, the product and the gallery in one
// transaction, so a failure halfway through cannot leave a product whose
// gallery is partially linked.
func (r *GormRepository) Create(ctx context.Context, write Write) (Product, error) {
	id := uuid.New()

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cover := model.Image{ID: uuid.New(), ImagePath: write.Cover.URL, PublicID: write.Cover.PublicID}
		if err := tx.Create(&cover).Error; err != nil {
			return fmt.Errorf("create cover image: %w", err)
		}

		record := model.Product{
			ID:            id,
			Name:          write.Name,
			ImageID:       cover.ID,
			Code:          write.Code,
			Price:         write.Price,
			Unit:          write.Unit,
			Description:   write.Description,
			Status:        write.Status,
			TypeProductID: write.TypeProduct,
			CreatedBy:     write.CreatedBy,
		}
		// Omit the associations: the foreign keys are already set, and letting
		// GORM upsert them would rewrite the category and the user row.
		if err := tx.Omit("Image", "TypeProduct", "User", "ProductImages").Create(&record).Error; err != nil {
			return fmt.Errorf("create product: %w", err)
		}

		return linkGallery(tx, id, write.Name, write.Gallery)
	})
	if err != nil {
		return Product{}, err
	}

	return r.FindByID(ctx, id)
}

// Update edits the scalar columns and, when asked, swaps the cover and the
// gallery. Everything happens in one transaction so an interrupted edit cannot
// leave the product with half of its new gallery and none of its old one.
func (r *GormRepository) Update(ctx context.Context, id uuid.UUID, write Write) (Product, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"name":            write.Name,
			"code":            write.Code,
			"price":           write.Price,
			"unit":            write.Unit,
			"description":     write.Description,
			"type_product_id": write.TypeProduct,
			"updated_at":      time.Now().UTC(),
		}

		// An empty URL means "keep the existing cover", so no image row is
		// written and the column is left alone. The public id moves with the
		// URL: storing one without the other would leave a handle pointing at
		// an asset the record no longer shows.
		if write.Cover.URL != "" {
			cover := model.Image{ID: uuid.New(), ImagePath: write.Cover.URL, PublicID: write.Cover.PublicID}
			if err := tx.Create(&cover).Error; err != nil {
				return fmt.Errorf("create cover image: %w", err)
			}
			updates["image_id"] = cover.ID
		}

		result := tx.Model(&model.Product{}).Where("id = ?", id).Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("update product: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}

		if write.ReplaceGallery {
			if err := unlinkGallery(tx, id); err != nil {
				return err
			}
		}

		return linkGallery(tx, id, write.Name, write.Gallery)
	})
	if err != nil {
		return Product{}, err
	}

	return r.FindByID(ctx, id)
}

func (r *GormRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status int) error {
	result := r.db.WithContext(ctx).Model(&model.Product{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()})

	if result.Error != nil {
		return fmt.Errorf("update product status: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the product and its gallery links together. Dropping only the
// product would leave product_images rows referencing a product that no longer
// exists, which is what the previous service did.
func (r *GormRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := unlinkGallery(tx, id); err != nil {
			return err
		}

		result := tx.Delete(&model.Product{}, "id = ?", id)
		if result.Error != nil {
			return fmt.Errorf("delete product: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// linkGallery stores one image row per upload and joins it to the product. The
// provider's public id is stored alongside the URL, so replacing or deleting
// the gallery later can actually remove the files.
func linkGallery(tx *gorm.DB, productID uuid.UUID, altText string, uploads []UploadedImage) error {
	if len(uploads) == 0 {
		return nil
	}

	images := make([]model.Image, 0, len(uploads))
	for _, upload := range uploads {
		images = append(images, model.Image{ID: uuid.New(), ImagePath: upload.URL, PublicID: upload.PublicID})
	}
	if err := tx.Create(&images).Error; err != nil {
		return fmt.Errorf("create gallery images: %w", err)
	}

	links := make([]model.ProductImage, 0, len(images))
	for _, image := range images {
		links = append(links, model.ProductImage{
			ID:        uuid.New(),
			ProductID: productID,
			ImageID:   image.ID,
			AltText:   altText,
		})
	}
	if err := tx.Omit("Product", "Image").Create(&links).Error; err != nil {
		return fmt.Errorf("link gallery images: %w", err)
	}
	return nil
}

// unlinkGallery drops every gallery join for a product.
func unlinkGallery(tx *gorm.DB, productID uuid.UUID) error {
	err := tx.Where("product_id = ?", productID).Delete(&model.ProductImage{}).Error
	if err != nil {
		return fmt.Errorf("delete product images: %w", err)
	}
	return nil
}

func toProduct(row model.Product) Product {
	item := Product{
		ID:            row.ID,
		Name:          row.Name,
		Code:          row.Code,
		Price:         row.Price,
		Unit:          row.Unit,
		Description:   row.Description,
		Status:        row.Status,
		ProductImages: make([]GalleryImage, 0, len(row.ProductImages)),
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}

	if row.ImageID != uuid.Nil {
		item.Image = &Image{ImagePath: row.Image.ImagePath, PublicID: row.Image.PublicID}
	}
	if row.TypeProductID != uuid.Nil {
		item.TypeProduct = &TypeProductRef{ID: row.TypeProduct.ID, Name: row.TypeProduct.Name}
	}
	if row.CreatedBy != uuid.Nil {
		item.CreatedBy = &Creator{Name: row.User.Name}
	}

	for _, link := range row.ProductImages {
		item.ProductImages = append(item.ProductImages, GalleryImage{
			Image: &Image{ImagePath: link.Image.ImagePath, PublicID: link.Image.PublicID},
		})
	}
	return item
}

// escapeLike neutralises the wildcards a user may type, so searching for "50%"
// does not match everything.
func escapeLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}
