package product_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/product"
	"service_nusantara/internal/platform/storage"
)

// --- fakes -------------------------------------------------------------
//
// The ports are small enough that in-memory fakes cover the business rules
// without PostgreSQL or a storage provider.

type fakeRepo struct {
	items map[uuid.UUID]product.Product
	// writes records what the service asked the repository to persist, so the
	// tests can assert on the uploaded URLs rather than on a database.
	writes []product.Write
	types  map[uuid.UUID]bool

	failList   error
	failCreate error
	failUpdate error
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		items: map[uuid.UUID]product.Product{},
		types: map[uuid.UUID]bool{},
	}
}

func (f *fakeRepo) List(context.Context, product.ListQuery) ([]product.Product, int64, error) {
	if f.failList != nil {
		return nil, 0, f.failList
	}
	items := make([]product.Product, 0, len(f.items))
	for _, item := range f.items {
		items = append(items, item)
	}
	return items, int64(len(items)), nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (product.Product, error) {
	item, ok := f.items[id]
	if !ok {
		return product.Product{}, product.ErrNotFound
	}
	return item, nil
}

func (f *fakeRepo) ExistsByName(_ context.Context, name string, excludeID uuid.UUID) (bool, error) {
	for id, item := range f.items {
		if id != excludeID && item.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) TypeProductExists(_ context.Context, id uuid.UUID) (bool, error) {
	return f.types[id], nil
}

func (f *fakeRepo) Create(_ context.Context, write product.Write) (product.Product, error) {
	if f.failCreate != nil {
		return product.Product{}, f.failCreate
	}
	f.writes = append(f.writes, write)

	id := uuid.New()
	item := product.Product{
		ID:     id,
		Name:   write.Name,
		Code:   write.Code,
		Price:  write.Price,
		Status: write.Status,
		Image:  &product.Image{ImagePath: write.Cover.URL, PublicID: write.Cover.PublicID},
	}
	for _, upload := range write.Gallery {
		item.ProductImages = append(item.ProductImages, product.GalleryImage{
			Image: &product.Image{ImagePath: upload.URL, PublicID: upload.PublicID},
		})
	}
	f.items[id] = item
	return item, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, write product.Write) (product.Product, error) {
	if f.failUpdate != nil {
		return product.Product{}, f.failUpdate
	}
	item, ok := f.items[id]
	if !ok {
		return product.Product{}, product.ErrNotFound
	}
	f.writes = append(f.writes, write)

	item.Name = write.Name
	item.Code = write.Code
	item.Price = write.Price
	if write.Cover.URL != "" {
		item.Image = &product.Image{ImagePath: write.Cover.URL, PublicID: write.Cover.PublicID}
	}
	if write.ReplaceGallery {
		item.ProductImages = nil
	}
	for _, upload := range write.Gallery {
		item.ProductImages = append(item.ProductImages, product.GalleryImage{
			Image: &product.Image{ImagePath: upload.URL, PublicID: upload.PublicID},
		})
	}
	f.items[id] = item
	return item, nil
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id uuid.UUID, status int) error {
	item, ok := f.items[id]
	if !ok {
		return product.ErrNotFound
	}
	item.Status = status
	f.items[id] = item
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.items[id]; !ok {
		return product.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

type fakeUploader struct {
	calls int
	// failAfter makes the nth upload fail, to model a provider dying halfway
	// through a gallery.
	failAfter int
	err       error
}

func (f *fakeUploader) Upload(_ context.Context, file *multipart.FileHeader, folder string) (storage.Uploaded, error) {
	f.calls++
	if f.err != nil && f.calls > f.failAfter {
		return storage.Uploaded{}, f.err
	}
	return storage.Uploaded{
		URL:      "https://cdn.test/" + folder + "/" + file.Filename,
		PublicID: folder + "/" + file.Filename,
	}, nil
}

// fakeReaper records what the service asked to have removed from the provider.
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

// --- helpers -----------------------------------------------------------

func fileHeader(name string) *multipart.FileHeader {
	return &multipart.FileHeader{Filename: name, Size: 1}
}

func newService(repo *fakeRepo, images *fakeUploader) *product.Service {
	return newServiceWithReaper(repo, images, &fakeReaper{})
}

func newServiceWithReaper(repo *fakeRepo, images *fakeUploader, reaper *fakeReaper) *product.Service {
	return product.NewService(repo, images, reaper, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func validInput(typeID uuid.UUID) product.Input {
	return product.Input{
		Name:        "Kopi Gayo",
		Code:        "KP-001",
		Price:       25000,
		Unit:        "pcs",
		Description: "single origin",
		Status:      product.StatusActive,
		TypeProduct: typeID,
		Cover:       fileHeader("cover.jpg"),
		CreatedBy:   uuid.New(),
	}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- tests -------------------------------------------------------------

func TestCreateUploadsCoverAndGalleryBeforePersisting(t *testing.T) {
	// Arrange
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	images := &fakeUploader{}

	input := validInput(typeID)
	input.Gallery = []*multipart.FileHeader{fileHeader("a.jpg"), fileHeader("b.png")}

	// Act
	created, err := newService(repo, images).Create(context.Background(), input)

	// Assert
	require.NoError(t, err)
	require.Len(t, repo.writes, 1)
	assert.Equal(t, "https://cdn.test/products/cover.jpg", repo.writes[0].Cover.URL)
	assert.Equal(t, []product.UploadedImage{
		{URL: "https://cdn.test/products/a.jpg", PublicID: "products/a.jpg"},
		{URL: "https://cdn.test/products/b.png", PublicID: "products/b.png"},
	}, repo.writes[0].Gallery)
	assert.Len(t, created.ProductImages, 2)
	assert.Equal(t, 3, images.calls)
}

func TestCreateRejectsMissingCover(t *testing.T) {
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true

	input := validInput(typeID)
	input.Cover = nil

	_, err := newService(repo, &fakeUploader{}).Create(context.Background(), input)

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
}

func TestCreateRejectsDuplicateName(t *testing.T) {
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	repo.items[uuid.New()] = product.Product{Name: "Kopi Gayo"}

	_, err := newService(repo, &fakeUploader{}).Create(context.Background(), validInput(typeID))

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
}

func TestCreateRejectsUnknownTypeProduct(t *testing.T) {
	repo := newRepo()

	_, err := newService(repo, &fakeUploader{}).Create(context.Background(), validInput(uuid.New()))

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestCreateWritesNothingWhenAGalleryUploadFails(t *testing.T) {
	// Arrange: the cover uploads, the second gallery image does not.
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	images := &fakeUploader{failAfter: 2, err: errors.New("provider exploded")}
	reaper := &fakeReaper{}

	input := validInput(typeID)
	input.Gallery = []*multipart.FileHeader{fileHeader("a.jpg"), fileHeader("b.jpg")}

	// Act
	_, err := newServiceWithReaper(repo, images, reaper).Create(context.Background(), input)

	// Assert: a partial gallery must never reach the database, and what did
	// reach the provider must not be left behind.
	require.Error(t, err)
	assert.Empty(t, repo.writes)
	assert.Empty(t, repo.items)
	assert.ElementsMatch(t, []string{"products/cover.jpg", "products/a.jpg"}, reaper.discarded)
}

func TestCreateDiscardsEveryUploadWhenTheInsertFails(t *testing.T) {
	// Arrange: the uploads succeed, the row does not.
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	repo.failCreate = errors.New("pq: deadlock detected")
	reaper := &fakeReaper{}

	input := validInput(typeID)
	input.Gallery = []*multipart.FileHeader{fileHeader("a.jpg")}

	// Act
	_, err := newServiceWithReaper(repo, &fakeUploader{}, reaper).Create(context.Background(), input)

	// Assert: nothing points at the assets, so none of them may survive.
	require.Error(t, err)
	assert.ElementsMatch(t, []string{"products/cover.jpg", "products/a.jpg"}, reaper.discarded)
}

func TestCreateReportsUnconfiguredStorageAsUnavailable(t *testing.T) {
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	images := &fakeUploader{err: storage.ErrNotConfigured}

	_, err := newService(repo, images).Create(context.Background(), validInput(typeID))

	assert.Equal(t, http.StatusServiceUnavailable, statusOf(t, err))
}

func TestCreateClampsOutOfRangeStatus(t *testing.T) {
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true

	input := validInput(typeID)
	input.Status = 7

	created, err := newService(repo, &fakeUploader{}).Create(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(t, product.StatusInactive, created.Status)
}

func TestUpdateWithoutCoverKeepsTheStoredOne(t *testing.T) {
	// Arrange
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{
		ID:    id,
		Name:  "Kopi Gayo",
		Image: &product.Image{ImagePath: "https://cdn.test/products/old.jpg"},
	}
	images := &fakeUploader{}

	input := validInput(typeID)
	input.Cover = nil

	// Act
	updated, err := newService(repo, images).Update(context.Background(), id, input)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 0, images.calls)
	assert.Equal(t, "", repo.writes[0].Cover.URL)
	assert.Equal(t, "https://cdn.test/products/old.jpg", updated.Image.ImagePath)
}

func TestUpdateReplacesGalleryOnlyWhenAsked(t *testing.T) {
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{
		ID:            id,
		Name:          "Kopi Gayo",
		ProductImages: []product.GalleryImage{{Image: &product.Image{ImagePath: "old.jpg"}}},
	}

	input := validInput(typeID)
	input.Cover = nil
	input.ReplaceGallery = true
	input.Gallery = []*multipart.FileHeader{fileHeader("new.jpg")}

	updated, err := newService(repo, &fakeUploader{}).Update(context.Background(), id, input)

	require.NoError(t, err)
	assert.True(t, repo.writes[0].ReplaceGallery)
	require.Len(t, updated.ProductImages, 1)
	assert.Equal(t, "https://cdn.test/products/new.jpg", updated.ProductImages[0].Image.ImagePath)
}

func TestUpdateDiscardsTheReplacedCoverOnlyAfterTheRowCommits(t *testing.T) {
	// Arrange
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{
		ID:   id,
		Name: "Kopi Gayo",
		Image: &product.Image{
			ImagePath: "https://cdn.test/products/old.jpg",
			PublicID:  "products/old.jpg",
		},
	}
	reaper := &fakeReaper{}

	// Act
	updated, err := newServiceWithReaper(repo, &fakeUploader{}, reaper).
		Update(context.Background(), id, validInput(typeID))

	// Assert: the new cover is stored and the one it replaced is gone.
	require.NoError(t, err)
	assert.Equal(t, "products/cover.jpg", repo.writes[0].Cover.PublicID)
	assert.Equal(t, "https://cdn.test/products/cover.jpg", updated.Image.ImagePath)
	assert.Equal(t, []string{"products/old.jpg"}, reaper.discarded)
}

func TestUpdateDiscardsTheNewCoverWhenTheRowFails(t *testing.T) {
	// Arrange
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{
		ID:    id,
		Name:  "Kopi Gayo",
		Image: &product.Image{ImagePath: "https://cdn.test/products/old.jpg", PublicID: "products/old.jpg"},
	}
	repo.failUpdate = errors.New("pq: deadlock detected")
	reaper := &fakeReaper{}

	// Act
	_, err := newServiceWithReaper(repo, &fakeUploader{}, reaper).
		Update(context.Background(), id, validInput(typeID))

	// Assert: the row still shows the old cover, so only the new one goes.
	require.Error(t, err)
	assert.Equal(t, []string{"products/cover.jpg"}, reaper.discarded)
}

func TestUpdateDiscardsEveryReplacedGalleryImage(t *testing.T) {
	// Arrange
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{
		ID:    id,
		Name:  "Kopi Gayo",
		Image: &product.Image{ImagePath: "old.jpg", PublicID: "products/old-cover.jpg"},
		ProductImages: []product.GalleryImage{
			{Image: &product.Image{ImagePath: "g1.jpg", PublicID: "products/old-g1.jpg"}},
			{Image: &product.Image{ImagePath: "g2.jpg", PublicID: "products/old-g2.jpg"}},
		},
	}
	reaper := &fakeReaper{}

	input := validInput(typeID)
	input.Cover = nil
	input.ReplaceGallery = true
	input.Gallery = []*multipart.FileHeader{fileHeader("new.jpg")}

	// Act
	_, err := newServiceWithReaper(repo, &fakeUploader{}, reaper).Update(context.Background(), id, input)

	// Assert: the whole replaced gallery is dropped, and the kept cover is not.
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"products/old-g1.jpg", "products/old-g2.jpg"}, reaper.discarded)
}

func TestUpdateKeepsTheGalleryWhenItIsNotReplaced(t *testing.T) {
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{
		ID:            id,
		Name:          "Kopi Gayo",
		ProductImages: []product.GalleryImage{{Image: &product.Image{ImagePath: "g1.jpg", PublicID: "products/old-g1.jpg"}}},
	}
	reaper := &fakeReaper{}

	input := validInput(typeID)
	input.Cover = nil
	input.Gallery = []*multipart.FileHeader{fileHeader("new.jpg")}

	_, err := newServiceWithReaper(repo, &fakeUploader{}, reaper).Update(context.Background(), id, input)

	require.NoError(t, err)
	assert.Empty(t, reaper.discarded)
}

func TestUpdateDoesNotChangeStatus(t *testing.T) {
	// The edit form omits status; accepting it here would deactivate a product
	// on every save.
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{ID: id, Name: "Kopi Gayo", Status: product.StatusActive}

	input := validInput(typeID)
	input.Cover = nil
	input.Status = product.StatusInactive

	updated, err := newService(repo, &fakeUploader{}).Update(context.Background(), id, input)

	require.NoError(t, err)
	assert.Equal(t, product.StatusActive, updated.Status)
}

func TestUpdateMissingProductIsNotFound(t *testing.T) {
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true

	_, err := newService(repo, &fakeUploader{}).Update(context.Background(), uuid.New(), validInput(typeID))

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestUpdateRejectsANameAnotherProductUses(t *testing.T) {
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{ID: id, Name: "Teh Tarik"}
	repo.items[uuid.New()] = product.Product{Name: "Kopi Gayo"}

	input := validInput(typeID)
	input.Cover = nil

	_, err := newService(repo, &fakeUploader{}).Update(context.Background(), id, input)

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
}

func TestSetStatusClampsAndReportsMissingRows(t *testing.T) {
	repo := newRepo()
	id := uuid.New()
	repo.items[id] = product.Product{ID: id, Status: product.StatusActive}
	service := newService(repo, &fakeUploader{})

	require.NoError(t, service.SetStatus(context.Background(), id, 42))
	assert.Equal(t, product.StatusInactive, repo.items[id].Status)

	err := service.SetStatus(context.Background(), uuid.New(), product.StatusActive)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestDelete(t *testing.T) {
	repo := newRepo()
	id := uuid.New()
	repo.items[id] = product.Product{ID: id}
	service := newService(repo, &fakeUploader{})

	require.NoError(t, service.Delete(context.Background(), id))
	assert.Empty(t, repo.items)

	err := service.Delete(context.Background(), id)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestDeleteDiscardsTheCoverAndTheWholeGallery(t *testing.T) {
	// Arrange
	repo := newRepo()
	id := uuid.New()
	repo.items[id] = product.Product{
		ID:    id,
		Image: &product.Image{ImagePath: "cover.jpg", PublicID: "products/cover.jpg"},
		ProductImages: []product.GalleryImage{
			{Image: &product.Image{ImagePath: "a.jpg", PublicID: "products/a.jpg"}},
			{Image: &product.Image{ImagePath: "b.jpg", PublicID: "products/b.jpg"}},
		},
	}
	reaper := &fakeReaper{}

	// Act
	require.NoError(t, newServiceWithReaper(repo, &fakeUploader{}, reaper).Delete(context.Background(), id))

	// Assert: the row is gone, so nothing can address these assets again.
	assert.Empty(t, repo.items)
	assert.ElementsMatch(t,
		[]string{"products/cover.jpg", "products/a.jpg", "products/b.jpg"},
		reaper.discarded)
}

func TestListHidesTheDriverError(t *testing.T) {
	repo := newRepo()
	repo.failList = errors.New("pq: relation \"products\" does not exist")

	_, _, err := newService(repo, &fakeUploader{}).List(context.Background(), product.ListQuery{Page: 1, PerPage: 20})

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, statusOf(t, err))
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.NotContains(t, appErr.Message, "pq:")
}

// --- handler tests -----------------------------------------------------
//
// The multipart field names are a contract with a client already in the field,
// so they are asserted here rather than left to a manual check.

func multipartRequest(t *testing.T, method, target string, fields map[string]string, files map[string][]string) *http.Request {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for name, value := range fields {
		require.NoError(t, writer.WriteField(name, value))
	}
	for field, names := range files {
		for _, name := range names {
			part, err := writer.CreateFormFile(field, name)
			require.NoError(t, err)
			_, err = part.Write([]byte("png-bytes"))
			require.NoError(t, err)
		}
	}
	require.NoError(t, writer.Close())

	r := httptest.NewRequest(method, target, body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{
		UserID: uuid.New().String(),
		Role:   "superadmin",
	}))
}

func textFields(typeID uuid.UUID) map[string]string {
	return map[string]string{
		"name":            "Kopi Gayo",
		"code":            "KP-001",
		"price":           "25000",
		"unit":            "pcs",
		"description":     "single origin",
		"type_product_id": typeID.String(),
	}
}

func TestHandlerCreateReadsCoverAndGalleryFields(t *testing.T) {
	// Arrange
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	handler := product.NewHandler(newService(repo, &fakeUploader{}))

	fields := textFields(typeID)
	fields["status"] = "1"
	r := multipartRequest(t, http.MethodPost, "/api/v1/product/create", fields, map[string][]string{
		"cover":   {"cover.jpg"},
		"gallery": {"a.jpg", "b.jpg"},
	})
	w := httptest.NewRecorder()

	// Act
	require.NoError(t, handler.Create(w, r))

	// Assert
	assert.Equal(t, http.StatusCreated, w.Code)
	require.Len(t, repo.writes, 1)
	write := repo.writes[0]
	assert.Equal(t, "Kopi Gayo", write.Name)
	assert.Equal(t, "KP-001", write.Code)
	assert.Equal(t, 25000, write.Price)
	assert.Equal(t, "pcs", write.Unit)
	assert.Equal(t, typeID, write.TypeProduct)
	assert.Equal(t, product.StatusActive, write.Status)
	assert.Equal(t, "https://cdn.test/products/cover.jpg", write.Cover.URL)
	assert.Len(t, write.Gallery, 2)
	assert.NotEqual(t, uuid.Nil, write.CreatedBy)
}

func TestHandlerCreateReportsEveryMissingFieldAtOnce(t *testing.T) {
	repo := newRepo()
	handler := product.NewHandler(newService(repo, &fakeUploader{}))

	r := multipartRequest(t, http.MethodPost, "/api/v1/product/create", map[string]string{}, nil)

	err := handler.Create(httptest.NewRecorder(), r)

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	details, ok := appErr.Details.([]httpx.FieldError)
	require.True(t, ok)

	reported := map[string]bool{}
	for _, detail := range details {
		reported[detail.Field] = true
	}
	for _, field := range []string{"name", "code", "unit", "type_product_id", "cover"} {
		assert.True(t, reported[field], "expected %s to be reported", field)
	}
}

func TestHandlerUpdateReadsNewCoverAndReplaceGallery(t *testing.T) {
	// Arrange
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{ID: id, Name: "Teh Tarik"}
	handler := product.NewHandler(newService(repo, &fakeUploader{}))

	fields := textFields(typeID)
	fields["replace_gallery"] = "true"
	r := multipartRequest(t, http.MethodPut, "/api/v1/product/"+id.String()+"/edit", fields, map[string][]string{
		"new_cover":   {"fresh.jpg"},
		"new_gallery": {"g1.jpg"},
	})
	r.SetPathValue("id", id.String())
	w := httptest.NewRecorder()

	// Act
	require.NoError(t, handler.Update(w, r))

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, repo.writes, 1)
	assert.Equal(t, "https://cdn.test/products/fresh.jpg", repo.writes[0].Cover.URL)
	assert.Equal(t, []product.UploadedImage{
		{URL: "https://cdn.test/products/g1.jpg", PublicID: "products/g1.jpg"},
	}, repo.writes[0].Gallery)
	assert.True(t, repo.writes[0].ReplaceGallery)
}

func TestHandlerUpdateLeavesGalleryAloneWhenReplaceIsFalse(t *testing.T) {
	repo := newRepo()
	typeID := uuid.New()
	repo.types[typeID] = true
	id := uuid.New()
	repo.items[id] = product.Product{ID: id, Name: "Teh Tarik"}
	handler := product.NewHandler(newService(repo, &fakeUploader{}))

	fields := textFields(typeID)
	fields["replace_gallery"] = "false"
	r := multipartRequest(t, http.MethodPut, "/api/v1/product/"+id.String()+"/edit", fields, nil)
	r.SetPathValue("id", id.String())

	require.NoError(t, handler.Update(httptest.NewRecorder(), r))

	assert.False(t, repo.writes[0].ReplaceGallery)
	assert.Empty(t, repo.writes[0].Gallery)
	assert.Empty(t, repo.writes[0].Cover.URL)
}

// TestHandlerListEmitsTheKeysTheWebClientMaps guards the response contract:
// web_nusantara/src/features/product/types.ts reads these exact keys.
func TestHandlerGetEmitsTheKeysTheWebClientMaps(t *testing.T) {
	repo := newRepo()
	id := uuid.New()
	typeID := uuid.New()
	repo.items[id] = product.Product{
		ID:            id,
		Name:          "Kopi Gayo",
		Code:          "KP-001",
		Price:         25000,
		Unit:          "pcs",
		Description:   "single origin",
		Status:        product.StatusActive,
		Image:         &product.Image{ImagePath: "https://cdn.test/products/cover.jpg"},
		TypeProduct:   &product.TypeProductRef{ID: typeID, Name: "Minuman"},
		ProductImages: []product.GalleryImage{{Image: &product.Image{ImagePath: "https://cdn.test/products/a.jpg"}}},
		CreatedBy:     &product.Creator{Name: "Admin Satu"},
	}
	handler := product.NewHandler(newService(repo, &fakeUploader{}))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/product/"+id.String(), nil)
	r.SetPathValue("id", id.String())
	w := httptest.NewRecorder()

	require.NoError(t, handler.Get(w, r))

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))

	data := envelope.Data
	for _, key := range []string{"id", "name", "code", "price", "unit", "description", "status", "created_at"} {
		assert.Contains(t, data, key)
	}
	assert.Equal(t, "https://cdn.test/products/cover.jpg", data["image"].(map[string]any)["image_path"])
	assert.Equal(t, "Minuman", data["type_product"].(map[string]any)["name"])
	assert.Equal(t, typeID.String(), data["type_product"].(map[string]any)["id"])
	assert.Equal(t, "Admin Satu", data["created_by"].(map[string]any)["name"])

	gallery := data["product_images"].([]any)
	require.Len(t, gallery, 1)
	assert.Equal(t, "https://cdn.test/products/a.jpg", gallery[0].(map[string]any)["image"].(map[string]any)["image_path"])
}
