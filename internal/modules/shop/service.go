package shop

import (
	"context"
	"errors"
	"log/slog"
	"mime/multipart"
	"strconv"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/platform/storage"
)

// imageFolder groups this module's uploads in the storage provider.
const imageFolder = "shops"

// uploader is the slice of the storage port this module needs.
type uploader interface {
	Upload(ctx context.Context, file *multipart.FileHeader, folder string) (storage.Uploaded, error)
}

// Service holds the business rules.
type Service struct {
	repo   Repository
	images uploader
	log    *slog.Logger
}

func NewService(repo Repository, images uploader, log *slog.Logger) *Service {
	return &Service{repo: repo, images: images, log: log}
}

// List returns one page of shops.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Shop, int64, error) {
	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load shops").WithCause(err)
	}
	return items, total, nil
}

// Get returns a single shop with its gallery, stock and staff.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Shop, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Shop{}, s.translate(err, "failed to load shop")
	}
	return item, nil
}

// Create adds a shop.
//
// Everything that can fail without touching the database is checked first, and
// the images are uploaded before the insert: a storage failure must not leave a
// row pointing at a picture that was never stored.
func (s *Service) Create(ctx context.Context, input Input) (Shop, error) {
	if err := s.verifyCashiers(ctx, input.CashierIDs); err != nil {
		return Shop{}, err
	}

	lines, err := s.resolveProducts(ctx, input.Products)
	if err != nil {
		return Shop{}, err
	}

	if input.Cover == nil {
		return Shop{}, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "cover", Message: "is required"}})
	}
	cover, err := s.upload(ctx, input.Cover, "cover")
	if err != nil {
		return Shop{}, err
	}

	gallery, err := s.uploadGallery(ctx, input.Gallery)
	if err != nil {
		return Shop{}, err
	}

	created, err := s.repo.Create(ctx, Record{
		Name:        input.Name,
		Description: input.Description,
		FullAddress: input.FullAddress,
		Lat:         input.Lat,
		Lng:         input.Lng,
		Status:      normalizeStatus(input.Status),
		Cover:       cover,
		CreatedBy:   input.CreatedBy,
	}, Assignments{
		Gallery: gallery,
		// A new shop has nothing to replace, and both relations are always
		// written so the shop is complete the moment it exists.
		Cashiers:    input.CashierIDs,
		SetCashiers: true,
		Products:    lines,
		SetProducts: true,
	})
	if err != nil {
		return Shop{}, httpx.Internal("failed to create shop").WithCause(err)
	}
	return created, nil
}

// Update edits a shop. Fields the caller omitted keep their stored value, and
// the staff and stock lists are replaced rather than appended to.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input Input) (Shop, error) {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		return Shop{}, s.translate(err, "failed to load shop")
	}

	if input.SetCashiers {
		if err := s.verifyCashiers(ctx, input.CashierIDs); err != nil {
			return Shop{}, err
		}
	}

	var (
		lines []ProductLine
		err   error
	)
	if input.SetProducts {
		if lines, err = s.resolveProducts(ctx, input.Products); err != nil {
			return Shop{}, err
		}
	}

	cover := ""
	if input.Cover != nil {
		if cover, err = s.upload(ctx, input.Cover, "cover"); err != nil {
			return Shop{}, err
		}
	}

	gallery, err := s.uploadGallery(ctx, input.Gallery)
	if err != nil {
		return Shop{}, err
	}

	updated, err := s.repo.Update(ctx, id, Record{
		Name:        input.Name,
		Description: input.Description,
		FullAddress: input.FullAddress,
		Lat:         input.Lat,
		Lng:         input.Lng,
		Cover:       cover,
	}, Assignments{
		Gallery:        gallery,
		ReplaceGallery: input.ReplaceGallery,
		Cashiers:       input.CashierIDs,
		SetCashiers:    input.SetCashiers,
		Products:       lines,
		SetProducts:    input.SetProducts,
	})
	if err != nil {
		return Shop{}, s.translate(err, "failed to update shop")
	}
	return updated, nil
}

// SetStatus opens or closes a shop.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status int) error {
	if err := s.repo.UpdateStatus(ctx, id, normalizeStatus(status)); err != nil {
		return s.translate(err, "failed to update status")
	}
	return nil
}

// Delete removes a shop, refusing while orders still reference it.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	hasOrders, err := s.repo.HasOrders(ctx, id)
	if err != nil {
		return httpx.Internal("failed to check whether the shop has orders").WithCause(err)
	}
	if hasOrders {
		// A foreign key violation would surface as a 500; this explains what
		// happened instead.
		return httpx.Conflict("this shop still has orders; close it instead of deleting it")
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete shop")
	}
	return nil
}

// AssignedShops lists the shops the calling staff member works at.
//
// The caller is always the authenticated user: there is no shop id or user id
// to pass, so one cashier cannot enumerate another's outlets.
func (s *Service) AssignedShops(ctx context.Context, staffID uuid.UUID) ([]AssignedShop, error) {
	items, err := s.repo.AssignedShops(ctx, staffID)
	if err != nil {
		return nil, httpx.Internal("failed to load assigned shops").WithCause(err)
	}
	return items, nil
}

// AssignedShop returns one shop the caller is assigned to.
//
// A shop the caller does not work at is reported as not found rather than
// forbidden, so the response cannot be used to probe which shops exist.
func (s *Service) AssignedShop(ctx context.Context, staffID, shopID uuid.UUID) (CashierShop, error) {
	item, err := s.repo.AssignedShop(ctx, staffID, shopID)
	if err != nil {
		return CashierShop{}, s.translate(err, "failed to load shop")
	}
	return item, nil
}

// AssignedShopProducts returns the stock of a shop the caller is assigned to.
func (s *Service) AssignedShopProducts(ctx context.Context, staffID, shopID uuid.UUID) ([]CashierProduct, error) {
	// Checked explicitly rather than relying on the query returning nothing:
	// an empty list and "you may not read this shop" are different answers, and
	// only the check keeps a guessed shop_id from ever reaching the data.
	assigned, err := s.repo.IsAssigned(ctx, staffID, shopID)
	if err != nil {
		return nil, httpx.Internal("failed to verify shop assignment").WithCause(err)
	}
	if !assigned {
		return nil, httpx.NotFound("shop not found")
	}

	items, err := s.repo.AssignedShopProducts(ctx, staffID, shopID)
	if err != nil {
		return nil, httpx.Internal("failed to load shop products").WithCause(err)
	}
	return items, nil
}

// verifyCashiers rejects ids that are not live accounts allowed to run a till,
// so a typo becomes a 422 instead of a dangling assignment row.
func (s *Service) verifyCashiers(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}

	found, err := s.repo.StaffIDs(ctx, ids)
	if err != nil {
		return httpx.Internal("failed to verify cashiers").WithCause(err)
	}

	known := make(map[uuid.UUID]bool, len(found))
	for _, id := range found {
		known[id] = true
	}

	var details []httpx.FieldError
	for i, id := range ids {
		if !known[id] {
			details = append(details, httpx.FieldError{
				Field:   "cashier_ids." + strconv.Itoa(i),
				Message: "is not a cashier account: " + id.String(),
			})
		}
	}
	if len(details) > 0 {
		return httpx.Validation("request validation failed").WithDetails(details)
	}
	return nil
}

// resolveProducts merges the request's per-shop overrides with the catalogue's
// own price and status, which is what an omitted `price` means.
func (s *Service) resolveProducts(ctx context.Context, assignments []ProductAssignment) ([]ProductLine, error) {
	if len(assignments) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, 0, len(assignments))
	for _, assignment := range assignments {
		ids = append(ids, assignment.ProductID)
	}

	defaults, err := s.repo.ProductDefaults(ctx, ids)
	if err != nil {
		return nil, httpx.Internal("failed to load products").WithCause(err)
	}

	var details []httpx.FieldError
	lines := make([]ProductLine, 0, len(assignments))
	for i, assignment := range assignments {
		fallback, ok := defaults[assignment.ProductID]
		if !ok {
			details = append(details, httpx.FieldError{
				Field:   "products." + strconv.Itoa(i) + ".product_id",
				Message: "product not found: " + assignment.ProductID.String(),
			})
			continue
		}
		if assignment.Stock < 0 {
			details = append(details, httpx.FieldError{
				Field:   "products." + strconv.Itoa(i) + ".stock",
				Message: "must not be negative",
			})
			continue
		}

		line := ProductLine{
			ProductID: assignment.ProductID,
			Price:     fallback.Price,
			Stock:     assignment.Stock,
			Status:    normalizeStatus(fallback.Status),
		}
		if assignment.Price != nil {
			if *assignment.Price < 0 {
				details = append(details, httpx.FieldError{
					Field:   "products." + strconv.Itoa(i) + ".price",
					Message: "must not be negative",
				})
				continue
			}
			line.Price = *assignment.Price
		}
		if assignment.Status != nil {
			line.Status = normalizeStatus(*assignment.Status)
		}
		lines = append(lines, line)
	}

	if len(details) > 0 {
		return nil, httpx.Validation("request validation failed").WithDetails(details)
	}
	return lines, nil
}

func (s *Service) uploadGallery(ctx context.Context, files []*multipart.FileHeader) ([]string, error) {
	urls := make([]string, 0, len(files))
	for i, file := range files {
		if file == nil {
			continue
		}
		url, err := s.upload(ctx, file, "gallery."+strconv.Itoa(i))
		if err != nil {
			return nil, err
		}
		urls = append(urls, url)
	}
	return urls, nil
}

func (s *Service) upload(ctx context.Context, file *multipart.FileHeader, field string) (string, error) {
	uploaded, err := s.images.Upload(ctx, file, imageFolder)
	if err != nil {
		if errors.Is(err, storage.ErrNotConfigured) {
			return "", httpx.Unavailable("image uploads are not configured on this server").WithCause(err)
		}
		// A rejected file is the caller's problem; anything else is ours.
		return "", httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: field, Message: err.Error()}}).
			WithCause(err)
	}
	return uploaded.URL, nil
}

// translate turns a repository error into the right HTTP status.
func (s *Service) translate(err error, message string) error {
	if errors.Is(err, ErrNotFound) {
		return httpx.NotFound("shop not found")
	}
	return httpx.Internal(message).WithCause(err)
}

// normalizeStatus collapses anything that is not "active" to inactive, so an
// out-of-range integer cannot reach the database.
func normalizeStatus(status int) int {
	if status == StatusActive {
		return StatusActive
	}
	return StatusInactive
}
