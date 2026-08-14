package role_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/role"
)

// --- fakes -------------------------------------------------------------

type fakeRepo struct {
	rows     map[uuid.UUID]role.Role
	inUse    map[uuid.UUID]bool
	failWith error
	deleted  int
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		rows:  map[uuid.UUID]role.Role{},
		inUse: map[uuid.UUID]bool{},
	}
}

func (f *fakeRepo) List(context.Context, role.ListQuery) ([]role.Role, int64, error) {
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	items := make([]role.Role, 0, len(f.rows))
	for _, row := range f.rows {
		items = append(items, row)
	}
	return items, int64(len(items)), nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (role.Role, error) {
	row, ok := f.rows[id]
	if !ok {
		return role.Role{}, role.ErrNotFound
	}
	return row, nil
}

func (f *fakeRepo) ExistsByName(_ context.Context, name string, excludeID uuid.UUID) (bool, error) {
	if f.failWith != nil {
		return false, f.failWith
	}
	for id, row := range f.rows {
		if id != excludeID && strings.EqualFold(row.Name, name) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) Create(_ context.Context, row role.Role) (role.Role, error) {
	f.rows[row.ID] = row
	return row, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, name string) (role.Role, error) {
	row, ok := f.rows[id]
	if !ok {
		return role.Role{}, role.ErrNotFound
	}
	row.Name = name
	f.rows[id] = row
	return row, nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.rows[id]; !ok {
		return role.ErrNotFound
	}
	delete(f.rows, id)
	f.deleted++
	return nil
}

func (f *fakeRepo) InUse(_ context.Context, id uuid.UUID) (bool, error) {
	if f.failWith != nil {
		return false, f.failWith
	}
	return f.inUse[id], nil
}

// --- helpers -----------------------------------------------------------

func newService(repo *fakeRepo) *role.Service {
	return role.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func seed(repo *fakeRepo, name string) role.Role {
	row := role.Role{ID: uuid.New(), Name: name}
	repo.rows[row.ID] = row
	return row
}

func status(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- tests -------------------------------------------------------------

func TestCreateStoresARole(t *testing.T) {
	// Arrange
	repo := newRepo()
	service := newService(repo)

	// Act
	created, err := service.Create(context.Background(), role.Input{Name: "superadmin"})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "superadmin", created.Name)
	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Len(t, repo.rows, 1)
}

func TestCreateTrimsTheName(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	created, err := service.Create(context.Background(), role.Input{Name: "  admin  "})

	require.NoError(t, err)
	assert.Equal(t, "admin", created.Name)
}

func TestCreateRejectsAnEmptyName(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	_, err := service.Create(context.Background(), role.Input{Name: "   "})

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
	assert.Empty(t, repo.rows)
}

func TestCreateRejectsADuplicateName(t *testing.T) {
	// Arrange: the name column is unique, so a duplicate must be answered with
	// a conflict rather than a driver error surfacing as a 500.
	repo := newRepo()
	seed(repo, "admin")
	service := newService(repo)

	// Act
	_, err := service.Create(context.Background(), role.Input{Name: "ADMIN"})

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, status(t, err))
	assert.Len(t, repo.rows, 1)
}

func TestGetReturnsNotFoundForAnUnknownID(t *testing.T) {
	service := newService(newRepo())

	_, err := service.Get(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestUpdateRenamesTheRole(t *testing.T) {
	repo := newRepo()
	existing := seed(repo, "admin")
	service := newService(repo)

	updated, err := service.Update(context.Background(), existing.ID, role.Input{Name: "manager"})

	require.NoError(t, err)
	assert.Equal(t, "manager", updated.Name)
	assert.Equal(t, existing.ID, updated.ID)
}

func TestUpdateKeepingItsOwnNameIsNotAConflict(t *testing.T) {
	repo := newRepo()
	existing := seed(repo, "admin")
	service := newService(repo)

	updated, err := service.Update(context.Background(), existing.ID, role.Input{Name: "admin"})

	require.NoError(t, err)
	assert.Equal(t, "admin", updated.Name)
}

func TestUpdateRejectsANameOwnedByAnotherRole(t *testing.T) {
	repo := newRepo()
	seed(repo, "admin")
	target := seed(repo, "cashier")
	service := newService(repo)

	_, err := service.Update(context.Background(), target.ID, role.Input{Name: "admin"})

	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, status(t, err))
	assert.Equal(t, "cashier", repo.rows[target.ID].Name)
}

func TestUpdateReturnsNotFoundForAnUnknownID(t *testing.T) {
	service := newService(newRepo())

	_, err := service.Update(context.Background(), uuid.New(), role.Input{Name: "admin"})

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestDeleteRemovesAnUnusedRole(t *testing.T) {
	repo := newRepo()
	existing := seed(repo, "admin")
	service := newService(repo)

	err := service.Delete(context.Background(), existing.ID)

	require.NoError(t, err)
	assert.Equal(t, 1, repo.deleted)
	assert.Empty(t, repo.rows)
}

func TestDeleteRefusesWhileUsersStillHoldTheRole(t *testing.T) {
	// Arrange: users.role_id references roles.id, so deleting would raise a
	// foreign key violation; the service answers 409 instead.
	repo := newRepo()
	existing := seed(repo, "admin")
	repo.inUse[existing.ID] = true
	service := newService(repo)

	// Act
	err := service.Delete(context.Background(), existing.ID)

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, status(t, err))
	assert.Zero(t, repo.deleted)
	assert.Len(t, repo.rows, 1)
}

func TestDeleteReturnsNotFoundForAnUnknownID(t *testing.T) {
	service := newService(newRepo())

	err := service.Delete(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestListReportsARepositoryFailureAsInternal(t *testing.T) {
	repo := newRepo()
	repo.failWith = errors.New("connection refused")
	service := newService(repo)

	_, _, err := service.List(context.Background(), role.ListQuery{Page: 1, PerPage: 20})

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, status(t, err))

	// The driver text must not reach the client.
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.NotContains(t, appErr.Message, "connection refused")
}
