package customeraddress

import (
	"context"
	"errors"
	"fmt"
	"math"
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

// addressRow is the flattened result of the address/user join. The owner's
// display name is read with a join rather than a Preload so listing N addresses
// stays one query instead of N+1.
type addressRow struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	UserName    string
	Label       string
	AddressText string
	Lat         float64
	Lng         float64
	IsDefault   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// selectAddresses builds the shared projection. The deleted_at predicate is
// spelled out because Scan into a non-model struct does not always carry GORM's
// soft-delete clause, and a resurrected address would be a nasty surprise.
func (r *GormRepository) selectAddresses(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("customer_addresses AS ca").
		Select(`ca.id, ca.user_id, COALESCE(u.name, '') AS user_name, ca.label,
		        ca.address_text, ca.lat, ca.lng, ca.is_default,
		        ca.created_at, ca.updated_at`).
		Joins("LEFT JOIN users AS u ON u.id = ca.user_id").
		Where("ca.deleted_at IS NULL")
}

func (r *GormRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]Address, error) {
	var rows []addressRow
	err := r.selectAddresses(ctx).
		Where("ca.user_id = ?", userID).
		Order("ca.is_default DESC").
		Order("ca.created_at DESC").
		// The list is per user and a person cannot have an unbounded number of
		// delivery addresses, but the cap keeps one abusive account from
		// turning this into an unbounded response.
		Limit(maxAddressesPerUser).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list customer addresses: %w", err)
	}

	items := make([]Address, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAddress(row))
	}
	return items, nil
}

func (r *GormRepository) FindByID(ctx context.Context, id, userID uuid.UUID) (Address, error) {
	var row addressRow
	// Both predicates together: looking a row up by id alone and checking the
	// owner afterwards is the same bug with a longer fuse.
	result := r.selectAddresses(ctx).
		Where("ca.id = ? AND ca.user_id = ?", id, userID).
		Limit(1).
		Scan(&row)
	if result.Error != nil {
		return Address{}, fmt.Errorf("find customer address: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return Address{}, ErrNotFound
	}
	return toAddress(row), nil
}

func (r *GormRepository) FindDefault(ctx context.Context, userID uuid.UUID) (Address, error) {
	var row addressRow
	result := r.selectAddresses(ctx).
		Where("ca.user_id = ? AND ca.is_default = true", userID).
		// Ordering makes the answer deterministic even if a historic row set
		// somehow holds two defaults, rather than letting the planner choose.
		Order("ca.created_at DESC").
		Limit(1).
		Scan(&row)
	if result.Error != nil {
		return Address{}, fmt.Errorf("find default customer address: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return Address{}, ErrNotFound
	}
	return toAddress(row), nil
}

func (r *GormRepository) Create(ctx context.Context, userID uuid.UUID, input Input) (Address, error) {
	record := model.CustomerAddress{
		ID:          uuid.New(),
		UserID:      userID,
		Label:       input.Label,
		AddressText: input.AddressText,
		Lat:         input.Lat,
		Lng:         input.Lng,
	}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&model.CustomerAddress{}).
			Where("user_id = ?", userID).
			Count(&existing).Error; err != nil {
			return fmt.Errorf("count customer addresses: %w", err)
		}
		if existing >= maxAddressesPerUser {
			return ErrTooMany
		}
		// The first address a customer saves is their default; deciding this
		// inside the transaction is what stops two concurrent creates from both
		// seeing an empty list and both claiming the flag.
		record.IsDefault = existing == 0

		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("create customer address: %w", err)
		}
		return nil
	})
	if err != nil {
		return Address{}, err
	}

	return r.FindByID(ctx, record.ID, userID)
}

func (r *GormRepository) Update(ctx context.Context, id, userID uuid.UUID, input Input) (Address, error) {
	updates := map[string]any{
		"label":        input.Label,
		"address_text": input.AddressText,
		"lat":          input.Lat,
		"lng":          input.Lng,
		"updated_at":   time.Now().UTC(),
	}

	result := r.db.WithContext(ctx).Model(&model.CustomerAddress{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(updates)
	if result.Error != nil {
		return Address{}, fmt.Errorf("update customer address: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return Address{}, ErrNotFound
	}

	return r.FindByID(ctx, id, userID)
}

func (r *GormRepository) Delete(ctx context.Context, id, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var deleted model.CustomerAddress
		err := tx.Where("id = ? AND user_id = ?", id, userID).First(&deleted).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return fmt.Errorf("load customer address for delete: %w", err)
		}

		result := tx.Delete(&model.CustomerAddress{}, "id = ? AND user_id = ?", id, userID)
		if result.Error != nil {
			return fmt.Errorf("delete customer address: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}

		if !deleted.IsDefault {
			return nil
		}

		// Deleting the default promotes the most recent survivor, in the same
		// transaction as the delete. Leaving the customer with no default at
		// all would send the next checkout down the "please pick an address"
		// path even though they still have several saved.
		var next model.CustomerAddress
		err = tx.Where("user_id = ?", userID).
			Order("created_at DESC").
			First(&next).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// That was the last one; having no default is correct now.
			return nil
		}
		if err != nil {
			return fmt.Errorf("find replacement default address: %w", err)
		}

		if err := tx.Model(&model.CustomerAddress{}).
			Where("id = ? AND user_id = ?", next.ID, userID).
			Updates(map[string]any{"is_default": true, "updated_at": time.Now().UTC()}).
			Error; err != nil {
			return fmt.Errorf("promote replacement default address: %w", err)
		}
		return nil
	})
}

func (r *GormRepository) SetDefault(ctx context.Context, id, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()

		// Demote first, promote second, both inside one transaction: a reader
		// never observes two defaults, and a failure between the two steps
		// rolls the demotion back rather than leaving the customer with none.
		if err := tx.Model(&model.CustomerAddress{}).
			Where("user_id = ? AND is_default = true", userID).
			Updates(map[string]any{"is_default": false, "updated_at": now}).
			Error; err != nil {
			return fmt.Errorf("clear default customer address: %w", err)
		}

		result := tx.Model(&model.CustomerAddress{}).
			Where("id = ? AND user_id = ?", id, userID).
			Updates(map[string]any{"is_default": true, "updated_at": now})
		if result.Error != nil {
			return fmt.Errorf("set default customer address: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			// Not theirs, or gone. Returning the error rolls back the demotion.
			return ErrNotFound
		}
		return nil
	})
}

// shopRow is the nearby query's projection: the shop card's fields plus the
// computed distance.
type shopRow struct {
	ID          uuid.UUID
	Name        string
	Cover       string
	Description string
	FullAddress string
	Lat         float64
	Lng         float64
	Status      int
	DistanceKM  float64
}

// nearbySQL ranks active shops by great-circle distance from a point.
//
// The ordering, the radius filter and the row cap are all the database's job.
// Loading every shop and sorting in Go would mean transferring the whole table
// on every storefront open, and the endpoint has an unauthenticated variant.
//
// The bounding box on lat/lng is a cheap superset of the circle that an index
// on (lat, lng) can use; the haversine term then trims it to the exact radius.
// LEAST/GREATEST clamp the cosine before acos, because floating point can push
// it a hair past 1 for a point compared against itself and turn the distance
// into NaN.
const nearbySQL = `
SELECT s.id,
       s.name,
       s.cover,
       s.description,
       s.full_address,
       s.lat,
       s.lng,
       s.status,
       (6371 * acos(LEAST(1, GREATEST(-1,
           cos(radians(@lat)) * cos(radians(s.lat)) * cos(radians(s.lng) - radians(@lng)) +
           sin(radians(@lat)) * sin(radians(s.lat))
       )))) AS distance_km
FROM shops AS s
WHERE s.deleted_at IS NULL
  AND s.status = @status
  AND s.lat IS NOT NULL
  AND s.lng IS NOT NULL
  AND s.lat BETWEEN @minLat AND @maxLat
  AND s.lng BETWEEN @minLng AND @maxLng
  AND (6371 * acos(LEAST(1, GREATEST(-1,
          cos(radians(@lat)) * cos(radians(s.lat)) * cos(radians(s.lng) - radians(@lng)) +
          sin(radians(@lat)) * sin(radians(s.lat))
      )))) <= @radius
ORDER BY distance_km ASC, s.id ASC
LIMIT @limit`

func (r *GormRepository) NearbyShops(ctx context.Context, origin Point, radiusKM float64, limit int) ([]NearbyShop, error) {
	box := boundingBox(origin, radiusKM)

	var rows []shopRow
	err := r.db.WithContext(ctx).Raw(nearbySQL,
		map[string]any{
			"lat":    origin.Lat,
			"lng":    origin.Lng,
			"status": shopStatusActive,
			"radius": radiusKM,
			"limit":  limit,
			"minLat": box.minLat,
			"maxLat": box.maxLat,
			"minLng": box.minLng,
			"maxLng": box.maxLng,
		}).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("find nearby shops: %w", err)
	}
	if len(rows) == 0 {
		return []NearbyShop{}, nil
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}

	gallery, err := r.galleryByShop(ctx, ids)
	if err != nil {
		return nil, err
	}

	items := make([]NearbyShop, 0, len(rows))
	for _, row := range rows {
		images := gallery[row.ID]
		if images == nil {
			// A nil slice would marshal as null; the clients expect a list.
			images = []string{}
		}
		items = append(items, NearbyShop{
			ID:          row.ID,
			Name:        row.Name,
			Cover:       row.Cover,
			Description: row.Description,
			FullAddress: row.FullAddress,
			Lat:         row.Lat,
			Lng:         row.Lng,
			Status:      row.Status,
			ShopImages:  images,
			Distance:    fmt.Sprintf("%.2f Km", row.DistanceKM),
		})
	}
	return items, nil
}

// galleryByShop loads every shop's pictures in one query, keyed by shop.
func (r *GormRepository) galleryByShop(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	var rows []struct {
		ShopID    uuid.UUID
		ImagePath string
	}
	err := r.db.WithContext(ctx).
		Table("shop_images").
		Select("shop_images.shop_id", "images.image_path").
		Joins("JOIN images ON images.id = shop_images.image_id").
		Where("shop_images.shop_id IN ?", ids).
		Where("shop_images.deleted_at IS NULL").
		Order("shop_images.created_at ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load shop gallery: %w", err)
	}

	gallery := map[uuid.UUID][]string{}
	for _, row := range rows {
		gallery[row.ShopID] = append(gallery[row.ShopID], row.ImagePath)
	}
	return gallery, nil
}

// kmPerDegreeLat is one degree of latitude in kilometres; it is constant, while
// a degree of longitude narrows towards the poles.
const kmPerDegreeLat = 111.045

type latLngBox struct {
	minLat, maxLat float64
	minLng, maxLng float64
}

// boundingBox returns a rectangle guaranteed to contain the search circle.
//
// It only ever admits extra rows, never excludes a real match, so the exact
// haversine filter that follows stays authoritative.
func boundingBox(origin Point, radiusKM float64) latLngBox {
	deltaLat := radiusKM / kmPerDegreeLat

	box := latLngBox{
		minLat: origin.Lat - deltaLat,
		maxLat: origin.Lat + deltaLat,
		minLng: -180,
		maxLng: 180,
	}

	// Near the poles a degree of longitude collapses and the box would have to
	// wrap the whole globe; leaving the longitude bounds open is correct there.
	cosLat := math.Cos(origin.Lat * math.Pi / 180)
	if cosLat > 0.01 {
		deltaLng := radiusKM / (kmPerDegreeLat * cosLat)
		if deltaLng < 180 {
			// Skip the bound entirely when the box would cross the antimeridian,
			// where min > max and a BETWEEN matches nothing.
			if origin.Lng-deltaLng >= -180 && origin.Lng+deltaLng <= 180 {
				box.minLng = origin.Lng - deltaLng
				box.maxLng = origin.Lng + deltaLng
			}
		}
	}
	return box
}

func toAddress(row addressRow) Address {
	return Address{
		ID:          row.ID,
		User:        row.UserName,
		Label:       row.Label,
		AddressText: row.AddressText,
		Lat:         row.Lat,
		Lng:         row.Lng,
		IsDefault:   row.IsDefault,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}
