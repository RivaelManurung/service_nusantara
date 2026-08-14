package cashier_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/cashier"
	"service_nusantara/internal/platform/storage"
)

// --- fakes -------------------------------------------------------------

type stored struct {
	row          cashier.Cashier
	passwordHash string
	roleID       uuid.UUID
}

type fakeRepo struct {
	rows      map[uuid.UUID]*stored
	roleNames map[string]uuid.UUID
	failOn    error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		rows:      map[uuid.UUID]*stored{},
		roleNames: map[string]uuid.UUID{cashier.RoleName: uuid.New()},
	}
}

func (f *fakeRepo) List(context.Context, cashier.ListQuery) ([]cashier.Cashier, int64, error) {
	if f.failOn != nil {
		return nil, 0, f.failOn
	}
	items := make([]cashier.Cashier, 0, len(f.rows))
	for _, row := range f.rows {
		items = append(items, row.row)
	}
	return items, int64(len(items)), nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (cashier.Cashier, error) {
	row, ok := f.rows[id]
	if !ok {
		return cashier.Cashier{}, cashier.ErrNotFound
	}
	return row.row, nil
}

func (f *fakeRepo) ExistsByUsername(_ context.Context, username string, excludeID uuid.UUID) (bool, error) {
	for id, row := range f.rows {
		if id != excludeID && strings.EqualFold(row.row.Username, username) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) ExistsByEmail(_ context.Context, email string, excludeID uuid.UUID) (bool, error) {
	for id, row := range f.rows {
		if id != excludeID && strings.EqualFold(row.row.Email, email) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) FindRoleIDByName(_ context.Context, name string) (uuid.UUID, error) {
	id, ok := f.roleNames[name]
	if !ok {
		return uuid.Nil, cashier.ErrRoleMissing
	}
	return id, nil
}

func (f *fakeRepo) Create(_ context.Context, row cashier.Cashier, passwordHash string, roleID uuid.UUID) (cashier.Cashier, error) {
	row.Role = cashier.RoleName
	f.rows[row.ID] = &stored{row: row, passwordHash: passwordHash, roleID: roleID}
	return row, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, name, username, photo string, status *int) (cashier.Cashier, error) {
	entry, ok := f.rows[id]
	if !ok {
		return cashier.Cashier{}, cashier.ErrNotFound
	}
	if name != "" {
		entry.row.Name = name
	}
	if username != "" {
		entry.row.Username = username
	}
	if photo != "" {
		entry.row.Photo = photo
	}
	if status != nil {
		entry.row.Status = *status
	}
	return entry.row, nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.rows[id]; !ok {
		return cashier.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

type fakeUploader struct {
	url  string
	err  error
	call int
}

func (f *fakeUploader) Upload(context.Context, *multipart.FileHeader, string) (storage.Uploaded, error) {
	f.call++
	if f.err != nil {
		return storage.Uploaded{}, f.err
	}
	return storage.Uploaded{URL: f.url, PublicID: "public-id"}, nil
}

func newService(repo cashier.Repository, images *fakeUploader) *cashier.Service {
	// The real hasher, at the cheapest legal cost: hashing is the behaviour
	// under test, so faking it would test nothing.
	return cashier.NewService(repo, images, auth.NewHasher(4),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func anyFile() *multipart.FileHeader {
	return &multipart.FileHeader{Filename: "avatar.png", Size: 128}
}

func validInput() cashier.CreateInput {
	return cashier.CreateInput{
		Name:     "Siti",
		Username: "siti",
		Email:    "siti@example.com",
		Password: "correct horse",
		Status:   cashier.StatusActive,
		Image:    anyFile(),
	}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- tests -------------------------------------------------------------

func TestCreateStoresABcryptHashAndNeverThePlaintext(t *testing.T) {
	// Arrange
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})
	input := validInput()

	// Act
	created, err := service.Create(context.Background(), input)

	// Assert
	require.NoError(t, err)
	entry := repo.rows[created.ID]
	require.NotNil(t, entry)
	assert.NotEqual(t, input.Password, entry.passwordHash)
	assert.True(t, strings.HasPrefix(entry.passwordHash, "$2"),
		"the stored value must be a bcrypt digest, got %q", entry.passwordHash)
	require.NoError(t, auth.NewHasher(4).Compare(entry.passwordHash, input.Password))
}

func TestCreateResponseCarriesNoPasswordField(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})

	created, err := service.Create(context.Background(), validInput())

	require.NoError(t, err)
	// The response struct is the contract; a password field would have to be
	// added deliberately, unlike the legacy entity which carried one by default.
	assert.NotContains(t, marshal(t, created), "password")
}

func TestCreateResolvesTheRoleByName(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})

	created, err := service.Create(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, repo.roleNames[cashier.RoleName], repo.rows[created.ID].roleID)
	assert.Equal(t, cashier.RoleName, created.Role)
}

func TestCreateIsUnavailableWhenTheRoleIsNotSeeded(t *testing.T) {
	repo := newFakeRepo()
	delete(repo.roleNames, cashier.RoleName)
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})

	_, err := service.Create(context.Background(), validInput())

	assert.Equal(t, http.StatusServiceUnavailable, statusOf(t, err))
	assert.Empty(t, repo.rows)
}

func TestCreateRejectsAShortPasswordBeforeTouchingTheRepository(t *testing.T) {
	repo := newFakeRepo()
	images := &fakeUploader{url: "https://cdn.example/a.png"}
	service := newService(repo, images)

	input := validInput()
	input.Password = "short"

	_, err := service.Create(context.Background(), input)

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Empty(t, repo.rows)
	assert.Equal(t, 0, images.call)
}

func TestCreateRejectsAMalformedEmail(t *testing.T) {
	service := newService(newFakeRepo(), &fakeUploader{url: "https://cdn.example/a.png"})

	input := validInput()
	input.Email = "not-an-address"

	_, err := service.Create(context.Background(), input)

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
}

func TestCreateRejectsADuplicateUsernameOrEmail(t *testing.T) {
	tests := map[string]func(*cashier.CreateInput){
		"username": func(in *cashier.CreateInput) { in.Email = "other@example.com" },
		"email":    func(in *cashier.CreateInput) { in.Username = "other" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			repo := newFakeRepo()
			service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})
			_, err := service.Create(context.Background(), validInput())
			require.NoError(t, err)

			second := validInput()
			mutate(&second)

			_, err = service.Create(context.Background(), second)
			assert.Equal(t, http.StatusConflict, statusOf(t, err))
		})
	}
}

func TestCreateLeavesNoAccountWhenTheUploadFails(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{err: errors.New("provider is down")})

	_, err := service.Create(context.Background(), validInput())

	require.Error(t, err)
	assert.Empty(t, repo.rows, "a storage failure must not leave an account behind")
}

func TestCreateWithoutAnImageIsRejected(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{})

	input := validInput()
	input.Image = nil

	_, err := service.Create(context.Background(), input)

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Empty(t, repo.rows)
}

func TestCreateClampsAnOutOfRangeStatus(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})

	input := validInput()
	input.Status = 9

	created, err := service.Create(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(t, cashier.StatusInactive, created.Status)
}

func TestCreateLowercasesTheEmail(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})

	input := validInput()
	input.Email = "Siti@Example.COM"

	created, err := service.Create(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(t, "siti@example.com", created.Email)
}

// The status toggle and the edit form share PUT /cashier/{id}/edit, so a
// status-only update must not blank the name or the username.
func TestUpdateWithOnlyStatusLeavesEverythingElseAlone(t *testing.T) {
	repo := newFakeRepo()
	images := &fakeUploader{url: "https://cdn.example/a.png"}
	service := newService(repo, images)
	created, err := service.Create(context.Background(), validInput())
	require.NoError(t, err)

	inactive := cashier.StatusInactive
	updated, err := service.Update(context.Background(), created.ID, cashier.UpdateInput{
		Status: &inactive,
	})

	require.NoError(t, err)
	assert.Equal(t, cashier.StatusInactive, updated.Status)
	assert.Equal(t, "Siti", updated.Name)
	assert.Equal(t, "siti", updated.Username)
	assert.Equal(t, "https://cdn.example/a.png", updated.Photo)
	assert.Equal(t, 1, images.call, "a status toggle uploads nothing")
}

func TestUpdateWithoutAFileKeepsTheExistingPhoto(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})
	created, err := service.Create(context.Background(), validInput())
	require.NoError(t, err)

	name := "Siti Rahma"
	updated, err := service.Update(context.Background(), created.ID, cashier.UpdateInput{Name: &name})

	require.NoError(t, err)
	assert.Equal(t, "Siti Rahma", updated.Name)
	assert.Equal(t, "https://cdn.example/a.png", updated.Photo)
}

func TestUpdateRejectsAUsernameOwnedByAnotherAccount(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})

	first, err := service.Create(context.Background(), validInput())
	require.NoError(t, err)

	second := validInput()
	second.Username = "budi"
	second.Email = "budi@example.com"
	_, err = service.Create(context.Background(), second)
	require.NoError(t, err)

	taken := "budi"
	_, err = service.Update(context.Background(), first.ID, cashier.UpdateInput{Username: &taken})

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
}

func TestUpdateAcceptsTheAccountsOwnUsername(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})
	created, err := service.Create(context.Background(), validInput())
	require.NoError(t, err)

	same := "siti"
	_, err = service.Update(context.Background(), created.ID, cashier.UpdateInput{Username: &same})

	require.NoError(t, err)
}

func TestUpdateOfAMissingCashierIsNotFound(t *testing.T) {
	service := newService(newFakeRepo(), &fakeUploader{})

	name := "Ghost"
	_, err := service.Update(context.Background(), uuid.New(), cashier.UpdateInput{Name: &name})

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestUpdateClampsAnOutOfRangeStatus(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})
	created, err := service.Create(context.Background(), validInput())
	require.NoError(t, err)

	weird := 42
	updated, err := service.Update(context.Background(), created.ID, cashier.UpdateInput{Status: &weird})

	require.NoError(t, err)
	assert.Equal(t, cashier.StatusInactive, updated.Status)
}

func TestGetReturnsTheStoredAccountAndNotFoundOtherwise(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo, &fakeUploader{url: "https://cdn.example/a.png"})
	created, err := service.Create(context.Background(), validInput())
	require.NoError(t, err)

	found, err := service.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "siti@example.com", found.Email)

	_, err = service.Get(context.Background(), uuid.New())
	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestDeleteOfAMissingCashierIsNotFound(t *testing.T) {
	service := newService(newFakeRepo(), &fakeUploader{})

	err := service.Delete(context.Background(), uuid.New())

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestRepositoryFailuresNeverLeakTheDriverMessage(t *testing.T) {
	repo := newFakeRepo()
	repo.failOn = errors.New("pq: column \"password\" does not exist")
	service := newService(repo, &fakeUploader{})

	_, _, err := service.List(context.Background(), cashier.ListQuery{Page: 1, PerPage: 20})

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusInternalServerError, appErr.Status)
	assert.NotContains(t, appErr.Message, "pq:")
}

func marshal(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
