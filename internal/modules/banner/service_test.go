package banner_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/banner"
	"service_nusantara/internal/platform/storage"
)

// --- fakes -------------------------------------------------------------
//
// The port is small enough that an in-memory fake covers the business rules
// without PostgreSQL, which is what makes these tests fast enough to run on
// every save.

type fakeRepo struct {
	rows      map[uuid.UUID]banner.Banner
	failOn    error
	lastPhoto string
	// failWrites, when set, is returned by Create, Update and Delete so a test
	// can drive the "the upload happened but the row did not" path.
	failWrites error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]banner.Banner{}}
}

func (f *fakeRepo) List(_ context.Context, query banner.ListQuery) ([]banner.Banner, int64, error) {
	if f.failOn != nil {
		return nil, 0, f.failOn
	}
	items := make([]banner.Banner, 0, len(f.rows))
	for _, row := range f.rows {
		items = append(items, row)
	}
	return items, int64(len(items)), nil
}

func (f *fakeRepo) ListActive(context.Context) ([]banner.Banner, error) {
	if f.failOn != nil {
		return nil, f.failOn
	}
	items := make([]banner.Banner, 0, len(f.rows))
	for _, row := range f.rows {
		if row.Status == banner.StatusActive {
			items = append(items, row)
		}
	}
	return items, nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (banner.Banner, error) {
	row, ok := f.rows[id]
	if !ok {
		return banner.Banner{}, banner.ErrNotFound
	}
	return row, nil
}

func (f *fakeRepo) FindActiveByID(_ context.Context, id uuid.UUID) (banner.Banner, error) {
	row, ok := f.rows[id]
	if !ok || row.Status != banner.StatusActive {
		return banner.Banner{}, banner.ErrNotFound
	}
	return row, nil
}

func (f *fakeRepo) ExistsByName(_ context.Context, name string, excludeID uuid.UUID) (bool, error) {
	for id, row := range f.rows {
		if id != excludeID && row.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) Create(_ context.Context, row banner.Banner, createdBy uuid.UUID) (banner.Banner, error) {
	if f.failWrites != nil {
		return banner.Banner{}, f.failWrites
	}
	f.rows[row.ID] = row
	f.lastPhoto = row.Photo
	return row, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, name, description, photo, photoPublicID string) (banner.Banner, error) {
	if f.failWrites != nil {
		return banner.Banner{}, f.failWrites
	}
	row, ok := f.rows[id]
	if !ok {
		return banner.Banner{}, banner.ErrNotFound
	}
	row.Name = name
	row.Description = description
	if photo != "" {
		row.Photo = photo
		row.PhotoPublicID = photoPublicID
	}
	f.rows[id] = row
	return row, nil
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id uuid.UUID, status int) error {
	row, ok := f.rows[id]
	if !ok {
		return banner.ErrNotFound
	}
	row.Status = status
	f.rows[id] = row
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.rows[id]; !ok {
		return banner.ErrNotFound
	}
	if f.failWrites != nil {
		return f.failWrites
	}
	delete(f.rows, id)
	return nil
}

type fakeUploader struct {
	url      string
	publicID string
	err      error
	call     int
}

func (f *fakeUploader) Upload(context.Context, *multipart.FileHeader, string) (storage.Uploaded, error) {
	f.call++
	if f.err != nil {
		return storage.Uploaded{}, f.err
	}
	publicID := f.publicID
	if publicID == "" {
		publicID = "public-id"
	}
	return storage.Uploaded{URL: f.url, PublicID: publicID}, nil
}

// fakeReaper records what the service asked to have deleted, which is the only
// way to prove an orphan was cleaned up without talking to a provider.
type fakeReaper struct {
	discarded []string
}

func (f *fakeReaper) Discard(_ context.Context, publicID string) {
	if publicID == "" {
		// The real Reaper ignores empty ids; recording them would make every
		// assertion below depend on how many no-op calls happened to be made.
		return
	}
	f.discarded = append(f.discarded, publicID)
}

func newService(repo banner.Repository, images *fakeUploader) *banner.Service {
	return newServiceWithReaper(repo, images, &fakeReaper{})
}

func newServiceWithReaper(repo banner.Repository, images *fakeUploader, cleaner *fakeReaper) *banner.Service {
	return banner.NewService(repo, images, cleaner, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func anyFile() *multipart.FileHeader {
	return &multipart.FileHeader{Filename: "artwork.png", Size: 128}
}

// statusOf asserts err is an *httpx.Error and returns its HTTP status.
func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- tests -------------------------------------------------------------

func TestCreateUploadsBeforeInserting(t *testing.T) {
	// Arrange
	repo := newFakeRepo()
	images := &fakeUploader{url: "https://cdn.example/banner.png"}
	service := newService(repo, images)

	// Act
	created, err := service.Create(context.Background(), banner.Input{
		Name:        "Ramadan Sale",
		Description: "Discounts all month",
		Status:      banner.StatusActive,
		Image:       anyFile(),
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example/banner.png", created.Photo)
	assert.Equal(t, banner.StatusActive, created.Status)
	assert.Equal(t, 1, images.call)
}

func TestCreateWithoutImageIsRejectedBeforeAnyInsert(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{})

	_, err := service.Create(context.Background(), banner.Input{
		Name:        "No artwork",
		Description: "…",
	})

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Empty(t, repo.rows, "no row may be written when the image is missing")
}

func TestCreateLeavesNoRowWhenTheUploadFails(t *testing.T) {
	repo := newFakeRepo()
	images := &fakeUploader{err: errors.New("provider is down")}
	service := newService(repo, images)

	_, err := service.Create(context.Background(), banner.Input{
		Name: "Broken", Description: "…", Image: anyFile(),
	})

	require.Error(t, err)
	assert.Empty(t, repo.rows, "a storage failure must not leave a row behind")
}

func TestCreateReportsUnconfiguredStorageAsUnavailable(t *testing.T) {
	service := newService(newFakeRepo(), &fakeUploader{err: storage.ErrNotConfigured})

	_, err := service.Create(context.Background(), banner.Input{
		Name: "Any", Description: "…", Image: anyFile(),
	})

	assert.Equal(t, http.StatusServiceUnavailable, statusOf(t, err))
}

func TestCreateRejectsADuplicateName(t *testing.T) {
	repo := newFakeRepo()
	existing := uuid.New()
	repo.rows[existing] = banner.Banner{ID: existing, Name: "Ramadan Sale"}
	service := newService(repo, &fakeUploader{url: "https://cdn.example/x.png"})

	_, err := service.Create(context.Background(), banner.Input{
		Name: "Ramadan Sale", Description: "…", Image: anyFile(),
	})

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
}

func TestCreateClampsAnOutOfRangeStatus(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/x.png"})

	created, err := service.Create(context.Background(), banner.Input{
		Name: "Weird", Description: "…", Status: 7, Image: anyFile(),
	})

	require.NoError(t, err)
	assert.Equal(t, banner.StatusInactive, created.Status)
}

func TestUpdateWithoutAFileKeepsTheExistingArtwork(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{ID: id, Name: "Old", Photo: "https://cdn.example/old.png"}
	images := &fakeUploader{url: "https://cdn.example/new.png"}
	service := newService(repo, images)

	updated, err := service.Update(context.Background(), id, banner.Input{
		Name: "New name", Description: "New description",
	})

	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example/old.png", updated.Photo)
	assert.Equal(t, 0, images.call, "no upload may happen when no file was sent")
}

func TestUpdateDoesNotChangeStatus(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{ID: id, Name: "Old", Status: banner.StatusActive}
	service := newService(repo, &fakeUploader{})

	updated, err := service.Update(context.Background(), id, banner.Input{
		Name: "New", Description: "…", Status: banner.StatusInactive,
	})

	require.NoError(t, err)
	assert.Equal(t, banner.StatusActive, updated.Status,
		"status moves only through /edit-status")
}

func TestUpdateOfAMissingBannerIsNotFound(t *testing.T) {
	service := newService(newFakeRepo(), &fakeUploader{})

	_, err := service.Update(context.Background(), uuid.New(), banner.Input{
		Name: "Ghost", Description: "…",
	})

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestUpdateRejectsANameOwnedByAnotherBanner(t *testing.T) {
	repo := newFakeRepo()
	mine, theirs := uuid.New(), uuid.New()
	repo.rows[mine] = banner.Banner{ID: mine, Name: "Mine"}
	repo.rows[theirs] = banner.Banner{ID: theirs, Name: "Theirs"}
	service := newService(repo, &fakeUploader{})

	_, err := service.Update(context.Background(), mine, banner.Input{
		Name: "Theirs", Description: "…",
	})

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
}

func TestUpdateAcceptsTheBannersOwnName(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{ID: id, Name: "Mine"}
	service := newService(repo, &fakeUploader{})

	_, err := service.Update(context.Background(), id, banner.Input{
		Name: "Mine", Description: "Rewritten",
	})

	require.NoError(t, err)
}

func TestSetStatusClampsAndPersists(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{ID: id, Status: banner.StatusActive}
	service := newService(repo, &fakeUploader{})

	require.NoError(t, service.SetStatus(context.Background(), id, 42))

	assert.Equal(t, banner.StatusInactive, repo.rows[id].Status)
}

func TestSetStatusOfAMissingBannerIsNotFound(t *testing.T) {
	service := newService(newFakeRepo(), &fakeUploader{})

	err := service.SetStatus(context.Background(), uuid.New(), banner.StatusActive)

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestGetReturnsTheStoredBannerAndNotFoundOtherwise(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{ID: id, Name: "Mine", Status: banner.StatusInactive}
	service := newService(repo, &fakeUploader{})

	found, err := service.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "Mine", found.Name)

	_, err = service.Get(context.Background(), uuid.New())
	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestDeleteOfAMissingBannerIsNotFound(t *testing.T) {
	service := newService(newFakeRepo(), &fakeUploader{})

	err := service.Delete(context.Background(), uuid.New())

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

// --- image cleanup -----------------------------------------------------

func TestCreatePersistsTheStorageHandle(t *testing.T) {
	repo := newFakeRepo()
	images := &fakeUploader{url: "https://cdn.example/new.png", publicID: "banners/new"}
	service := newService(repo, images)

	created, err := service.Create(context.Background(), banner.Input{
		Name: "Handle", Description: "…", Image: anyFile(),
	})

	require.NoError(t, err)
	assert.Equal(t, "banners/new", created.PhotoPublicID,
		"without the handle nothing can ever delete this asset")
}

func TestCreateDiscardsTheUploadWhenTheInsertFails(t *testing.T) {
	repo := newFakeRepo()
	repo.failWrites = errors.New("insert exploded")
	images := &fakeUploader{url: "https://cdn.example/new.png", publicID: "banners/new"}
	cleaner := &fakeReaper{}
	service := newServiceWithReaper(repo, images, cleaner)

	_, err := service.Create(context.Background(), banner.Input{
		Name: "Doomed", Description: "…", Image: anyFile(),
	})

	require.Error(t, err)
	assert.Equal(t, []string{"banners/new"}, cleaner.discarded,
		"an upload with no row to point at it must not be left behind")
}

func TestUpdateDiscardsTheOldImageOnlyAfterTheRowCommits(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{
		ID: id, Name: "Old",
		Photo: "https://cdn.example/old.png", PhotoPublicID: "banners/old",
	}
	images := &fakeUploader{url: "https://cdn.example/new.png", publicID: "banners/new"}
	cleaner := &fakeReaper{}
	service := newServiceWithReaper(repo, images, cleaner)

	updated, err := service.Update(context.Background(), id, banner.Input{
		Name: "New", Description: "…", Image: anyFile(),
	})

	require.NoError(t, err)
	assert.Equal(t, "banners/new", updated.PhotoPublicID)
	assert.Equal(t, []string{"banners/old"}, cleaner.discarded,
		"the replaced artwork is the one that must go, not the new one")
}

func TestUpdateDiscardsTheNewImageWhenTheRowFailsToWrite(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{
		ID: id, Name: "Old",
		Photo: "https://cdn.example/old.png", PhotoPublicID: "banners/old",
	}
	repo.failWrites = errors.New("update exploded")
	images := &fakeUploader{url: "https://cdn.example/new.png", publicID: "banners/new"}
	cleaner := &fakeReaper{}
	service := newServiceWithReaper(repo, images, cleaner)

	_, err := service.Update(context.Background(), id, banner.Input{
		Name: "New", Description: "…", Image: anyFile(),
	})

	require.Error(t, err)
	assert.Equal(t, []string{"banners/new"}, cleaner.discarded,
		"the row still shows the old artwork, so only the new upload is stranded")
}

func TestUpdateWithoutAFileDiscardsNothing(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{ID: id, Name: "Old", PhotoPublicID: "banners/old"}
	cleaner := &fakeReaper{}
	service := newServiceWithReaper(repo, &fakeUploader{}, cleaner)

	_, err := service.Update(context.Background(), id, banner.Input{Name: "New", Description: "…"})

	require.NoError(t, err)
	assert.Empty(t, cleaner.discarded, "the row still points at that image")
}

func TestDeleteDiscardsTheBannersImage(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{ID: id, Name: "Gone", PhotoPublicID: "banners/gone"}
	cleaner := &fakeReaper{}
	service := newServiceWithReaper(repo, &fakeUploader{}, cleaner)

	require.NoError(t, service.Delete(context.Background(), id))

	assert.Equal(t, []string{"banners/gone"}, cleaner.discarded)
}

func TestDeleteDiscardsNothingWhenTheRowSurvives(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{ID: id, Name: "Stays", PhotoPublicID: "banners/stays"}
	repo.failWrites = errors.New("delete exploded")
	cleaner := &fakeReaper{}
	service := newServiceWithReaper(repo, &fakeUploader{}, cleaner)

	require.Error(t, service.Delete(context.Background(), id))

	assert.Empty(t, cleaner.discarded, "a row that is still there must keep its image")
}

// discardPanics stands in for a storage provider that is down. The real Reaper
// swallows that failure; this asserts the service does not depend on it doing
// so being optional -- a discard must never turn into the caller's error.
type recordingFailReaper struct{ calls int }

func (r *recordingFailReaper) Discard(context.Context, string) { r.calls++ }

func TestADiscardNeverFailsTheRequest(t *testing.T) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.rows[id] = banner.Banner{ID: id, Name: "Old", PhotoPublicID: "banners/old"}
	cleaner := &recordingFailReaper{}
	service := banner.NewService(repo, &fakeUploader{url: "https://cdn.example/new.png"},
		cleaner, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := service.Update(context.Background(), id, banner.Input{
		Name: "New", Description: "…", Image: anyFile(),
	})

	require.NoError(t, err, "cleanup is best effort and must not surface to the caller")
	assert.Equal(t, 1, cleaner.calls)
}

func TestPublicReadsOnlySeeActiveBanners(t *testing.T) {
	repo := newFakeRepo()
	live, hidden := uuid.New(), uuid.New()
	repo.rows[live] = banner.Banner{ID: live, Status: banner.StatusActive}
	repo.rows[hidden] = banner.Banner{ID: hidden, Status: banner.StatusInactive}
	service := newService(repo, &fakeUploader{})

	items, err := service.ListPublic(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, live, items[0].ID)

	_, err = service.GetPublic(context.Background(), hidden)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err),
		"an inactive banner is reported as missing, not as forbidden")
}

func TestRepositoryFailuresNeverLeakTheDriverMessage(t *testing.T) {
	repo := newFakeRepo()
	repo.failOn = errors.New("pq: relation \"banners\" does not exist")
	service := newService(repo, &fakeUploader{})

	_, _, err := service.List(context.Background(), banner.ListQuery{Page: 1, PerPage: 20})

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusInternalServerError, appErr.Status)
	assert.NotContains(t, appErr.Message, "pq:")
}
