package shop_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/shop"
	"service_nusantara/internal/platform/storage"
)

// --- fakes -------------------------------------------------------------
//
// The port is small enough that an in-memory fake covers every business rule
// without PostgreSQL, which is what keeps these tests fast enough to run on
// every save.

type fakeRepo struct {
	shops map[uuid.UUID]shop.Shop

	staff    map[uuid.UUID]bool
	products map[uuid.UUID]shop.ProductDefaults
	assigned map[uuid.UUID]map[uuid.UUID]bool // staff id -> shop id

	hasOrders bool
	failWith  error
	// failWrite makes Create and Update fail the way a constraint violation
	// would, which is the case that strands a just-uploaded picture.
	failWrite error

	createdRecord shop.Record
	createdAssign shop.Assignments
	updatedAssign shop.Assignments
	creates       int
	updates       int
	productReads  int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		shops:    map[uuid.UUID]shop.Shop{},
		staff:    map[uuid.UUID]bool{},
		products: map[uuid.UUID]shop.ProductDefaults{},
		assigned: map[uuid.UUID]map[uuid.UUID]bool{},
	}
}

func (f *fakeRepo) List(context.Context, shop.ListQuery) ([]shop.Shop, int64, error) {
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	items := make([]shop.Shop, 0, len(f.shops))
	for _, item := range f.shops {
		items = append(items, item)
	}
	return items, int64(len(items)), nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (shop.Shop, error) {
	item, ok := f.shops[id]
	if !ok {
		return shop.Shop{}, shop.ErrNotFound
	}
	return item, nil
}

func (f *fakeRepo) Create(_ context.Context, record shop.Record, assign shop.Assignments) (shop.Shop, error) {
	if f.failWrite != nil {
		return shop.Shop{}, f.failWrite
	}
	f.creates++
	f.createdRecord = record
	f.createdAssign = assign

	created := shop.Shop{
		ID:               uuid.New(),
		Name:             record.Name,
		Cover:            record.Cover,
		Status:           record.Status,
		CoverPublicID:    record.CoverPublicID,
		ShopImages:       galleryURLs(assign.Gallery),
		GalleryPublicIDs: galleryPublicIDs(assign.Gallery),
	}
	f.shops[created.ID] = created
	return created, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, record shop.Record, assign shop.Assignments) (shop.Shop, error) {
	item, ok := f.shops[id]
	if !ok {
		return shop.Shop{}, shop.ErrNotFound
	}
	if f.failWrite != nil {
		return shop.Shop{}, f.failWrite
	}
	f.updates++
	f.updatedAssign = assign

	if record.Name != "" {
		item.Name = record.Name
	}
	// An empty cover means "keep the stored one", and the handle moves with it.
	if record.Cover != "" {
		item.Cover = record.Cover
		item.CoverPublicID = record.CoverPublicID
	}
	if assign.ReplaceGallery {
		item.ShopImages = nil
		item.GalleryPublicIDs = nil
	}
	item.ShopImages = append(item.ShopImages, galleryURLs(assign.Gallery)...)
	item.GalleryPublicIDs = append(item.GalleryPublicIDs, galleryPublicIDs(assign.Gallery)...)

	f.shops[id] = item
	return item, nil
}

func galleryURLs(images []shop.GalleryImage) []string {
	urls := make([]string, 0, len(images))
	for _, image := range images {
		urls = append(urls, image.URL)
	}
	return urls
}

func galleryPublicIDs(images []shop.GalleryImage) []string {
	ids := make([]string, 0, len(images))
	for _, image := range images {
		ids = append(ids, image.PublicID)
	}
	return ids
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id uuid.UUID, status int) error {
	item, ok := f.shops[id]
	if !ok {
		return shop.ErrNotFound
	}
	item.Status = status
	f.shops[id] = item
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.shops[id]; !ok {
		return shop.ErrNotFound
	}
	delete(f.shops, id)
	return nil
}

func (f *fakeRepo) HasOrders(context.Context, uuid.UUID) (bool, error) {
	return f.hasOrders, nil
}

func (f *fakeRepo) StaffIDs(_ context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	found := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if f.staff[id] {
			found = append(found, id)
		}
	}
	return found, nil
}

func (f *fakeRepo) ProductDefaults(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]shop.ProductDefaults, error) {
	f.productReads++
	out := map[uuid.UUID]shop.ProductDefaults{}
	for _, id := range ids {
		if defaults, ok := f.products[id]; ok {
			out[id] = defaults
		}
	}
	return out, nil
}

func (f *fakeRepo) AssignedShops(_ context.Context, staffID uuid.UUID) ([]shop.AssignedShop, error) {
	items := []shop.AssignedShop{}
	for shopID := range f.assigned[staffID] {
		items = append(items, shop.AssignedShop{ID: shopID, Name: f.shops[shopID].Name})
	}
	return items, nil
}

func (f *fakeRepo) IsAssigned(_ context.Context, staffID, shopID uuid.UUID) (bool, error) {
	return f.assigned[staffID][shopID], nil
}

func (f *fakeRepo) AssignedShop(_ context.Context, staffID, shopID uuid.UUID) (shop.CashierShop, error) {
	if !f.assigned[staffID][shopID] {
		return shop.CashierShop{}, shop.ErrNotFound
	}
	return shop.CashierShop{ID: shopID, Name: f.shops[shopID].Name}, nil
}

func (f *fakeRepo) AssignedShopProducts(_ context.Context, staffID, shopID uuid.UUID) ([]shop.CashierProduct, error) {
	if !f.assigned[staffID][shopID] {
		return nil, shop.ErrNotFound
	}
	return []shop.CashierProduct{{ID: uuid.New(), Name: "Bakpia"}}, nil
}

type fakeUploader struct {
	calls int
	err   error
	// failAfter lets a test upload the cover successfully and then fail on a
	// gallery file, which is the case that strands a half-finished upload set.
	failAfter int
}

func (f *fakeUploader) Upload(context.Context, *multipart.FileHeader, string) (storage.Uploaded, error) {
	f.calls++
	if f.err != nil && f.calls > f.failAfter {
		return storage.Uploaded{}, f.err
	}
	// The suffix makes every upload distinguishable, so a test can name exactly
	// which handle should have been discarded.
	return storage.Uploaded{
		URL:      fmt.Sprintf("https://cdn.test/image-%d.jpg", f.calls),
		PublicID: fmt.Sprintf("image-%d", f.calls),
	}, nil
}

// fakeReaper records what the service asked to have deleted.
type fakeReaper struct {
	discarded []string
}

func (f *fakeReaper) Discard(_ context.Context, publicID string) {
	if publicID == "" {
		return
	}
	f.discarded = append(f.discarded, publicID)
}

func (f *fakeReaper) DiscardAll(ctx context.Context, publicIDs []string) {
	for _, id := range publicIDs {
		f.Discard(ctx, id)
	}
}

func newService(repo *fakeRepo, images *fakeUploader) *shop.Service {
	return shop.NewService(repo, images, &fakeReaper{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newReapingService is newService with a reaper the caller can inspect.
func newReapingService(repo *fakeRepo, images *fakeUploader, reaper *fakeReaper) *shop.Service {
	return shop.NewService(repo, images, reaper, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func anyFile() *multipart.FileHeader {
	return &multipart.FileHeader{Filename: "cover.jpg", Size: 1024}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- create ------------------------------------------------------------

func TestCreateStoresTheResolvedAssignments(t *testing.T) {
	// Arrange
	repo := newFakeRepo()
	cashierID := uuid.New()
	productID := uuid.New()
	repo.staff[cashierID] = true
	repo.products[productID] = shop.ProductDefaults{Price: 25000, Status: shop.StatusActive}
	images := &fakeUploader{}

	// Act
	created, err := newService(repo, images).Create(context.Background(), shop.Input{
		Name:        "Toko Malioboro",
		Description: "Oleh-oleh",
		FullAddress: "Jl. Malioboro 1",
		Status:      shop.StatusActive,
		Cover:       anyFile(),
		Gallery:     []*multipart.FileHeader{anyFile(), anyFile()},
		CashierIDs:  []uuid.UUID{cashierID},
		SetCashiers: true,
		Products:    []shop.ProductAssignment{{ProductID: productID, Stock: 12}},
		SetProducts: true,
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "Toko Malioboro", created.Name)
	assert.Equal(t, 3, images.calls, "cover plus both gallery files are uploaded")
	assert.Equal(t, "https://cdn.test/image-1.jpg", repo.createdRecord.Cover)
	assert.Len(t, repo.createdAssign.Gallery, 2)
	assert.Equal(t, []uuid.UUID{cashierID}, repo.createdAssign.Cashiers)
	require.Len(t, repo.createdAssign.Products, 1)
	assert.Equal(t, shop.ProductLine{ProductID: productID, Price: 25000, Stock: 12, Status: shop.StatusActive},
		repo.createdAssign.Products[0])
}

func TestCreateUsesThePerShopPriceAndStatusWhenSent(t *testing.T) {
	repo := newFakeRepo()
	productID := uuid.New()
	repo.products[productID] = shop.ProductDefaults{Price: 25000, Status: shop.StatusActive}

	price := 19500.0
	inactive := shop.StatusInactive

	_, err := newService(repo, &fakeUploader{}).Create(context.Background(), shop.Input{
		Name:        "Toko Braga",
		Cover:       anyFile(),
		Products:    []shop.ProductAssignment{{ProductID: productID, Stock: 3, Price: &price, Status: &inactive}},
		SetProducts: true,
	})

	require.NoError(t, err)
	require.Len(t, repo.createdAssign.Products, 1)
	assert.Equal(t, 19500.0, repo.createdAssign.Products[0].Price)
	assert.Equal(t, shop.StatusInactive, repo.createdAssign.Products[0].Status)
}

func TestCreateRejectsAMissingCover(t *testing.T) {
	repo := newFakeRepo()

	_, err := newService(repo, &fakeUploader{}).Create(context.Background(), shop.Input{Name: "Toko Kuta"})

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.creates)
}

func TestCreateRejectsAnIDThatIsNotACashier(t *testing.T) {
	repo := newFakeRepo()
	images := &fakeUploader{}

	_, err := newService(repo, images).Create(context.Background(), shop.Input{
		Name:        "Toko Kuta",
		Cover:       anyFile(),
		CashierIDs:  []uuid.UUID{uuid.New()},
		SetCashiers: true,
	})

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.creates)
	assert.Zero(t, images.calls, "an invalid assignment is caught before anything is uploaded")
}

func TestCreateRejectsAnUnknownProduct(t *testing.T) {
	repo := newFakeRepo()

	_, err := newService(repo, &fakeUploader{}).Create(context.Background(), shop.Input{
		Name:        "Toko Kuta",
		Cover:       anyFile(),
		Products:    []shop.ProductAssignment{{ProductID: uuid.New(), Stock: 1}},
		SetProducts: true,
	})

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.creates)
}

// A storage failure must not leave a row pointing at an image that was never
// stored, so nothing is written when the upload fails.
func TestCreateDoesNotWriteWhenTheUploadFails(t *testing.T) {
	repo := newFakeRepo()

	_, err := newService(repo, &fakeUploader{err: storage.ErrNotConfigured}).
		Create(context.Background(), shop.Input{Name: "Toko Kuta", Cover: anyFile()})

	assert.Equal(t, http.StatusServiceUnavailable, statusOf(t, err))
	assert.Zero(t, repo.creates)
}

func TestCreateClampsAnOutOfRangeStatus(t *testing.T) {
	repo := newFakeRepo()

	_, err := newService(repo, &fakeUploader{}).Create(context.Background(), shop.Input{
		Name:   "Toko Kuta",
		Cover:  anyFile(),
		Status: 7,
	})

	require.NoError(t, err)
	assert.Equal(t, shop.StatusInactive, repo.createdRecord.Status)
}

// --- update ------------------------------------------------------------

func TestUpdateKeepsTheCoverWhenNoFileIsSent(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, Name: "Toko Braga", Cover: "https://cdn.test/old.jpg"}
	images := &fakeUploader{}

	updated, err := newService(repo, images).Update(context.Background(), id, shop.Input{Name: "Toko Braga Baru"})

	require.NoError(t, err)
	assert.Equal(t, "Toko Braga Baru", updated.Name)
	assert.Equal(t, "https://cdn.test/old.jpg", updated.Cover)
	assert.Zero(t, images.calls)
}

// Omitting a relation must leave it alone; sending it replaces it wholesale.
func TestUpdateOnlyTouchesTheRelationsItWasSent(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, Name: "Toko Braga"}
	cashierID := uuid.New()
	repo.staff[cashierID] = true

	_, err := newService(repo, &fakeUploader{}).Update(context.Background(), id, shop.Input{
		CashierIDs:  []uuid.UUID{cashierID},
		SetCashiers: true,
	})

	require.NoError(t, err)
	assert.True(t, repo.updatedAssign.SetCashiers)
	assert.False(t, repo.updatedAssign.SetProducts, "stock is untouched when `products` was not sent")
	assert.Zero(t, repo.productReads)
}

func TestUpdateReplacesTheGalleryOnlyWhenAsked(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, Name: "Toko Braga"}

	_, err := newService(repo, &fakeUploader{}).Update(context.Background(), id, shop.Input{
		Gallery:        []*multipart.FileHeader{anyFile()},
		ReplaceGallery: true,
	})

	require.NoError(t, err)
	assert.True(t, repo.updatedAssign.ReplaceGallery)
	assert.Len(t, repo.updatedAssign.Gallery, 1)
}

func TestUpdateReportsAMissingShopAsNotFound(t *testing.T) {
	repo := newFakeRepo()

	_, err := newService(repo, &fakeUploader{}).Update(context.Background(), uuid.New(), shop.Input{Name: "Ghost"})

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
	assert.Zero(t, repo.updates)
}

// --- status and delete -------------------------------------------------

func TestSetStatusClampsAnythingThatIsNotActive(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, Status: shop.StatusActive}

	require.NoError(t, newService(repo, &fakeUploader{}).SetStatus(context.Background(), id, 9))

	assert.Equal(t, shop.StatusInactive, repo.shops[id].Status)
}

func TestDeleteRefusesWhileOrdersReferenceTheShop(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id}
	repo.hasOrders = true

	err := newService(repo, &fakeUploader{}).Delete(context.Background(), id)

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
	assert.Len(t, repo.shops, 1)
}

func TestDeleteRemovesAShopWithNoOrders(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id}

	require.NoError(t, newService(repo, &fakeUploader{}).Delete(context.Background(), id))
	assert.Empty(t, repo.shops)
}

func TestDeleteReportsAMissingShopAsNotFound(t *testing.T) {
	repo := newFakeRepo()

	err := newService(repo, &fakeUploader{}).Delete(context.Background(), uuid.New())

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

// --- image cleanup -----------------------------------------------------
//
// Nothing but the row records a storage handle, so every path that stops
// pointing at a picture has to hand it to the reaper or the asset leaks forever.

func TestCreatePersistsTheStorageHandles(t *testing.T) {
	// Arrange
	repo := newFakeRepo()

	// Act
	_, err := newService(repo, &fakeUploader{}).Create(context.Background(), shop.Input{
		Name:    "Toko Malioboro",
		Cover:   anyFile(),
		Gallery: []*multipart.FileHeader{anyFile(), anyFile()},
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "image-1", repo.createdRecord.CoverPublicID,
		"the handle is the only thing that can delete the asset later")
	assert.Equal(t, []string{"image-2", "image-3"}, galleryPublicIDs(repo.createdAssign.Gallery))
}

func TestCreateDiscardsEveryUploadWhenTheInsertFails(t *testing.T) {
	// Arrange
	repo := newFakeRepo()
	repo.failWrite = errors.New("pq: duplicate key value violates unique constraint")
	reaper := &fakeReaper{}

	// Act
	_, err := newReapingService(repo, &fakeUploader{}, reaper).
		Create(context.Background(), shop.Input{
			Name:    "Toko Malioboro",
			Cover:   anyFile(),
			Gallery: []*multipart.FileHeader{anyFile()},
		})

	// Assert
	require.Error(t, err)
	assert.ElementsMatch(t, []string{"image-1", "image-2"}, reaper.discarded,
		"no row points at the uploads, so none may be left behind")
}

// A gallery file rejected halfway through leaves the files before it with
// nothing to point at them.
func TestCreateDiscardsEarlierUploadsWhenAGalleryFileIsRejected(t *testing.T) {
	repo := newFakeRepo()
	reaper := &fakeReaper{}
	// The cover and the first gallery file succeed; the second is rejected.
	images := &fakeUploader{err: errors.New("unsupported image type"), failAfter: 2}

	_, err := newReapingService(repo, images, reaper).Create(context.Background(), shop.Input{
		Name:    "Toko Malioboro",
		Cover:   anyFile(),
		Gallery: []*multipart.FileHeader{anyFile(), anyFile()},
	})

	require.Error(t, err)
	assert.Zero(t, repo.creates)
	assert.ElementsMatch(t, []string{"image-1", "image-2"}, reaper.discarded)
}

func TestUpdateDiscardsTheOldCoverOnlyAfterTheRowCommits(t *testing.T) {
	// Arrange
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, Name: "Toko Braga", Cover: "https://cdn.test/old.jpg",
		CoverPublicID: "old-cover"}
	reaper := &fakeReaper{}

	// Act
	_, err := newReapingService(repo, &fakeUploader{}, reaper).
		Update(context.Background(), id, shop.Input{Cover: anyFile()})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, []string{"old-cover"}, reaper.discarded, "the replaced cover is dropped")
	assert.Equal(t, "image-1", repo.shops[id].CoverPublicID)
}

func TestUpdateDiscardsTheNewUploadsWhenTheWriteFails(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, CoverPublicID: "old-cover",
		GalleryPublicIDs: []string{"old-gallery"}}
	repo.failWrite = errors.New("pq: deadlock detected")
	reaper := &fakeReaper{}

	_, err := newReapingService(repo, &fakeUploader{}, reaper).
		Update(context.Background(), id, shop.Input{
			Cover:          anyFile(),
			Gallery:        []*multipart.FileHeader{anyFile()},
			ReplaceGallery: true,
		})

	require.Error(t, err)
	assert.ElementsMatch(t, []string{"image-1", "image-2"}, reaper.discarded,
		"the row still shows the old pictures, so it is the new uploads that are stranded")
	assert.NotContains(t, reaper.discarded, "old-cover")
	assert.NotContains(t, reaper.discarded, "old-gallery")
}

// replace_gallery=true drops every picture it replaced, not just the cover.
func TestUpdateReplacingTheGalleryDiscardsEveryReplacedPicture(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, CoverPublicID: "old-cover",
		GalleryPublicIDs: []string{"old-gallery-1", "old-gallery-2"}}
	reaper := &fakeReaper{}

	_, err := newReapingService(repo, &fakeUploader{}, reaper).
		Update(context.Background(), id, shop.Input{
			Gallery:        []*multipart.FileHeader{anyFile()},
			ReplaceGallery: true,
		})

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"old-gallery-1", "old-gallery-2"}, reaper.discarded)
	assert.NotContains(t, reaper.discarded, "old-cover", "the cover was not replaced")
}

// Appending to the gallery replaces nothing, so nothing may be deleted.
func TestUpdateAppendingToTheGalleryDiscardsNothing(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, CoverPublicID: "old-cover",
		GalleryPublicIDs: []string{"old-gallery"}}
	reaper := &fakeReaper{}

	_, err := newReapingService(repo, &fakeUploader{}, reaper).
		Update(context.Background(), id, shop.Input{Gallery: []*multipart.FileHeader{anyFile()}})

	require.NoError(t, err)
	assert.Empty(t, reaper.discarded, "the row still points at all of them")
}

func TestDeleteDiscardsTheCoverAndTheWholeGallery(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, CoverPublicID: "cover",
		GalleryPublicIDs: []string{"gallery-1", "gallery-2"}}
	reaper := &fakeReaper{}

	require.NoError(t, newReapingService(repo, &fakeUploader{}, reaper).
		Delete(context.Background(), id))

	assert.ElementsMatch(t, []string{"cover", "gallery-1", "gallery-2"}, reaper.discarded)
}

func TestDeleteDiscardsNothingWhileOrdersReferenceTheShop(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.shops[id] = shop.Shop{ID: id, CoverPublicID: "cover"}
	repo.hasOrders = true
	reaper := &fakeReaper{}

	err := newReapingService(repo, &fakeUploader{}, reaper).Delete(context.Background(), id)

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
	assert.Empty(t, reaper.discarded, "the shop still shows those pictures")
}

// --- cashier-scoped reads ----------------------------------------------

func TestAssignedShopsOnlyReturnsTheCallersOwnShops(t *testing.T) {
	repo := newFakeRepo()
	mine, theirs := uuid.New(), uuid.New()
	me, someoneElse := uuid.New(), uuid.New()
	repo.shops[mine] = shop.Shop{ID: mine, Name: "Toko Malioboro"}
	repo.shops[theirs] = shop.Shop{ID: theirs, Name: "Toko Braga"}
	repo.assigned[me] = map[uuid.UUID]bool{mine: true}
	repo.assigned[someoneElse] = map[uuid.UUID]bool{theirs: true}

	items, err := newService(repo, &fakeUploader{}).AssignedShops(context.Background(), me)

	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, mine, items[0].ID)
}

// The shop id comes from the URL, so the assignment is what stops a caller from
// reading an outlet they do not work at.
func TestAssignedShopRefusesAShopTheCallerIsNotAssignedTo(t *testing.T) {
	repo := newFakeRepo()
	theirs := uuid.New()
	me := uuid.New()
	repo.shops[theirs] = shop.Shop{ID: theirs, Name: "Toko Braga"}
	repo.assigned[uuid.New()] = map[uuid.UUID]bool{theirs: true}

	_, err := newService(repo, &fakeUploader{}).AssignedShop(context.Background(), me, theirs)

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestAssignedShopProductsRefusesAShopTheCallerIsNotAssignedTo(t *testing.T) {
	repo := newFakeRepo()
	theirs := uuid.New()
	me := uuid.New()
	repo.shops[theirs] = shop.Shop{ID: theirs}
	repo.assigned[uuid.New()] = map[uuid.UUID]bool{theirs: true}

	_, err := newService(repo, &fakeUploader{}).AssignedShopProducts(context.Background(), me, theirs)

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestAssignedShopProductsReturnsStockForAnAssignedShop(t *testing.T) {
	repo := newFakeRepo()
	mine := uuid.New()
	me := uuid.New()
	repo.shops[mine] = shop.Shop{ID: mine}
	repo.assigned[me] = map[uuid.UUID]bool{mine: true}

	items, err := newService(repo, &fakeUploader{}).AssignedShopProducts(context.Background(), me, mine)

	require.NoError(t, err)
	assert.Len(t, items, 1)
}

// --- list --------------------------------------------------------------

func TestListHidesTheDriverErrorBehindA500(t *testing.T) {
	repo := newFakeRepo()
	repo.failWith = errors.New(`pq: relation "shops" does not exist`)

	_, _, err := newService(repo, &fakeUploader{}).List(context.Background(), shop.ListQuery{Page: 1, PerPage: 20})

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, statusOf(t, err))

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.NotContains(t, appErr.Message, "relation")
}
