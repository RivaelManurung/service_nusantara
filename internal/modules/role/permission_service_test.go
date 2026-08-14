package role_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/role"
)

// --- fake ---------------------------------------------------------------

type fakePermissionRepo struct {
	grants   map[uuid.UUID][]string
	failWith error
	replaced int
}

func newPermissionRepo() *fakePermissionRepo {
	return &fakePermissionRepo{grants: map[uuid.UUID][]string{}}
}

func (f *fakePermissionRepo) ListPermissions(context.Context) ([]role.Permission, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	items := make([]role.Permission, 0)
	for _, def := range role.Catalog() {
		items = append(items, role.Permission{
			ID: uuid.New(), Code: def.Code, Label: def.Label, Group: def.Group,
		})
	}
	return items, nil
}

func (f *fakePermissionRepo) CodesForRoleID(_ context.Context, roleID uuid.UUID) ([]string, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.grants[roleID], nil
}

func (f *fakePermissionRepo) ReplaceForRole(_ context.Context, roleID uuid.UUID, codes []string) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.grants[roleID] = codes
	f.replaced++
	return nil
}

func (f *fakePermissionRepo) PermissionsFor(context.Context, string) (map[string]struct{}, error) {
	return nil, nil
}

// --- helpers ------------------------------------------------------------

func newPermissionService(roles *fakeRepo, perms *fakePermissionRepo) *role.PermissionService {
	return role.NewPermissionService(roles, perms, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// --- tests --------------------------------------------------------------

func TestCatalogReturnsEveryKnownPermission(t *testing.T) {
	// Arrange
	service := newPermissionService(newRepo(), newPermissionRepo())

	// Act
	items, err := service.Catalog(context.Background())

	// Assert
	require.NoError(t, err)
	assert.Len(t, items, len(role.AllCodes()))
	assert.NotEmpty(t, items[0].Group, "the UI groups the matrix by this field")
}

func TestCatalogReportsAStoreFailureAsInternal(t *testing.T) {
	perms := newPermissionRepo()
	perms.failWith = errors.New("connection refused")
	service := newPermissionService(newRepo(), perms)

	_, err := service.Catalog(context.Background())

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, status(t, err))

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.NotContains(t, appErr.Message, "connection refused")
}

func TestForRoleReturnsTheGrantedCodes(t *testing.T) {
	roles := newRepo()
	target := seed(roles, "admin")
	perms := newPermissionRepo()
	perms.grants[target.ID] = []string{role.PermProductRead}
	service := newPermissionService(roles, perms)

	got, err := service.ForRole(context.Background(), target.ID)

	require.NoError(t, err)
	assert.Equal(t, target.ID, got.RoleID)
	assert.Equal(t, "admin", got.RoleName)
	assert.Equal(t, []string{role.PermProductRead}, got.Codes)
}

func TestForRoleReturnsNotFoundForAnUnknownRole(t *testing.T) {
	service := newPermissionService(newRepo(), newPermissionRepo())

	_, err := service.ForRole(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestReplaceStoresTheSubmittedSet(t *testing.T) {
	roles := newRepo()
	target := seed(roles, "admin")
	perms := newPermissionRepo()
	service := newPermissionService(roles, perms)

	got, err := service.Replace(context.Background(), target.ID,
		[]string{role.PermProductWrite, role.PermProductRead})

	require.NoError(t, err)
	assert.Equal(t, 1, perms.replaced)
	// Catalogue order, not submission order, so the response reads the same way
	// on every save.
	assert.Equal(t, []string{role.PermProductRead, role.PermProductWrite}, got.Codes)
}

func TestReplaceDeduplicatesAndTrims(t *testing.T) {
	roles := newRepo()
	target := seed(roles, "admin")
	service := newPermissionService(roles, newPermissionRepo())

	got, err := service.Replace(context.Background(), target.ID,
		[]string{" product.read ", "product.read", ""})

	require.NoError(t, err)
	assert.Equal(t, []string{role.PermProductRead}, got.Codes)
}

func TestReplaceRejectsAnUnknownCode(t *testing.T) {
	// A typo must be a 422 naming the code, not a grant that silently does
	// nothing because no permission row matches it.
	roles := newRepo()
	target := seed(roles, "admin")
	perms := newPermissionRepo()
	service := newPermissionService(roles, perms)

	_, err := service.Replace(context.Background(), target.ID,
		[]string{role.PermProductRead, "product.destroy"})

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
	assert.Zero(t, perms.replaced)

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	details, ok := appErr.Details.([]httpx.FieldError)
	require.True(t, ok)
	assert.Contains(t, details[0].Message, "product.destroy")
}

func TestReplaceAcceptsAnEmptySetForAnOrdinaryRole(t *testing.T) {
	// "This role can do nothing" has to be reachable; only superadmin is
	// special.
	roles := newRepo()
	target := seed(roles, "cashier")
	perms := newPermissionRepo()
	service := newPermissionService(roles, perms)

	got, err := service.Replace(context.Background(), target.ID, nil)

	require.NoError(t, err)
	assert.Empty(t, got.Codes)
	assert.Equal(t, 1, perms.replaced)
}

func TestReplaceRefusesToStripTheSuperAdminRole(t *testing.T) {
	// Arrange: the permission endpoints themselves require superadmin, so a
	// superadmin left short of the catalogue could never restore what it gave
	// away.
	roles := newRepo()
	target := seed(roles, "superadmin")
	perms := newPermissionRepo()
	service := newPermissionService(roles, perms)

	// Act
	_, err := service.Replace(context.Background(), target.ID, nil)

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, status(t, err))
	assert.Zero(t, perms.replaced)
}

func TestReplaceRefusesToRemoveASinglePermissionFromSuperAdmin(t *testing.T) {
	roles := newRepo()
	target := seed(roles, "SuperAdmin") // the guard is case-insensitive
	perms := newPermissionRepo()
	service := newPermissionService(roles, perms)

	all := role.AllCodes()
	_, err := service.Replace(context.Background(), target.ID, all[:len(all)-1])

	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, status(t, err))
	assert.Zero(t, perms.replaced)
}

func TestReplaceAllowsSuperAdminToKeepTheFullCatalogue(t *testing.T) {
	roles := newRepo()
	target := seed(roles, "superadmin")
	perms := newPermissionRepo()
	service := newPermissionService(roles, perms)

	got, err := service.Replace(context.Background(), target.ID, role.AllCodes())

	require.NoError(t, err)
	assert.Len(t, got.Codes, len(role.AllCodes()))
}

func TestReplaceReturnsNotFoundForAnUnknownRole(t *testing.T) {
	service := newPermissionService(newRepo(), newPermissionRepo())

	_, err := service.Replace(context.Background(), uuid.New(), nil)

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestCatalogueCodesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, code := range role.AllCodes() {
		assert.False(t, seen[code], "duplicate permission code: %s", code)
		seen[code] = true
	}
}

func TestCatalogueIsSelfConsistent(t *testing.T) {
	known := role.KnownCodes()
	assert.Len(t, known, len(role.AllCodes()))

	for _, def := range role.Catalog() {
		assert.NotEmpty(t, def.Label, "%s has no label", def.Code)
		assert.NotEmpty(t, def.Group, "%s has no group", def.Code)
		assert.True(t, slices.Contains(role.AllCodes(), def.Code))
	}
}
