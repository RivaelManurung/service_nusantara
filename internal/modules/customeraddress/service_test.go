package customeraddress_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/customeraddress"
)

// --- fake repository ---------------------------------------------------
//
// An in-memory owner-scoped store. It enforces the same two invariants the SQL
// does -- every lookup is filtered by owner, and at most one row per owner
// carries the default flag -- so a service bug that relies on the database
// being lenient still fails here.

type fakeRepo struct {
	rows map[uuid.UUID]*customeraddress.Address
	// owner tracks who each address belongs to; the response type deliberately
	// does not carry it.
	owner  map[uuid.UUID]uuid.UUID
	seq    int
	shops  []customeraddress.NearbyShop
	lastOK struct {
		origin customeraddress.Point
		radius float64
		limit  int
	}
	failWith error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		rows:  map[uuid.UUID]*customeraddress.Address{},
		owner: map[uuid.UUID]uuid.UUID{},
	}
}

func (f *fakeRepo) mine(userID uuid.UUID) []customeraddress.Address {
	items := []customeraddress.Address{}
	for id, row := range f.rows {
		if f.owner[id] == userID {
			items = append(items, *row)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDefault != items[j].IsDefault {
			return items[i].IsDefault
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items
}

func (f *fakeRepo) ListByUser(_ context.Context, userID uuid.UUID) ([]customeraddress.Address, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.mine(userID), nil
}

func (f *fakeRepo) FindByID(_ context.Context, id, userID uuid.UUID) (customeraddress.Address, error) {
	row, ok := f.rows[id]
	if !ok || f.owner[id] != userID {
		return customeraddress.Address{}, customeraddress.ErrNotFound
	}
	return *row, nil
}

func (f *fakeRepo) FindDefault(_ context.Context, userID uuid.UUID) (customeraddress.Address, error) {
	for _, row := range f.mine(userID) {
		if row.IsDefault {
			return row, nil
		}
	}
	return customeraddress.Address{}, customeraddress.ErrNotFound
}

func (f *fakeRepo) Create(_ context.Context, userID uuid.UUID, input customeraddress.Input) (customeraddress.Address, error) {
	if f.failWith != nil {
		return customeraddress.Address{}, f.failWith
	}
	f.seq++
	created := customeraddress.Address{
		ID:          uuid.New(),
		Label:       input.Label,
		AddressText: input.AddressText,
		Lat:         input.Lat,
		Lng:         input.Lng,
		IsDefault:   len(f.mine(userID)) == 0,
		CreatedAt:   time.Unix(int64(f.seq), 0).UTC(),
	}
	f.rows[created.ID] = &created
	f.owner[created.ID] = userID
	return created, nil
}

func (f *fakeRepo) Update(_ context.Context, id, userID uuid.UUID, input customeraddress.Input) (customeraddress.Address, error) {
	row, ok := f.rows[id]
	if !ok || f.owner[id] != userID {
		return customeraddress.Address{}, customeraddress.ErrNotFound
	}
	row.Label = input.Label
	row.AddressText = input.AddressText
	row.Lat = input.Lat
	row.Lng = input.Lng
	return *row, nil
}

func (f *fakeRepo) Delete(_ context.Context, id, userID uuid.UUID) error {
	row, ok := f.rows[id]
	if !ok || f.owner[id] != userID {
		return customeraddress.ErrNotFound
	}
	wasDefault := row.IsDefault

	delete(f.rows, id)
	delete(f.owner, id)

	if !wasDefault {
		return nil
	}
	remaining := f.mine(userID)
	if len(remaining) == 0 {
		return nil
	}
	f.rows[remaining[0].ID].IsDefault = true
	return nil
}

func (f *fakeRepo) SetDefault(_ context.Context, id, userID uuid.UUID) error {
	if _, ok := f.rows[id]; !ok || f.owner[id] != userID {
		return customeraddress.ErrNotFound
	}
	for rowID, row := range f.rows {
		if f.owner[rowID] == userID {
			row.IsDefault = rowID == id
		}
	}
	return nil
}

func (f *fakeRepo) NearbyShops(_ context.Context, origin customeraddress.Point, radiusKM float64, limit int) ([]customeraddress.NearbyShop, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.lastOK.origin = origin
	f.lastOK.radius = radiusKM
	f.lastOK.limit = limit
	return f.shops, nil
}

// --- helpers -----------------------------------------------------------

func newService(repo customeraddress.Repository) *customeraddress.Service {
	return customeraddress.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.True(t, errors.As(err, &appErr), "expected an httpx.Error, got %v", err)
	return appErr.Status
}

func seed(t *testing.T, repo *fakeRepo, service *customeraddress.Service, userID uuid.UUID, label string) customeraddress.Address {
	t.Helper()
	created, err := service.Create(context.Background(), userID, customeraddress.Input{
		Label:       label,
		AddressText: label + " street 1",
		Lat:         -6.2,
		Lng:         106.8,
	})
	require.NoError(t, err)
	return created
}

func defaultCount(repo *fakeRepo, userID uuid.UUID) int {
	count := 0
	for _, row := range repo.mine(userID) {
		if row.IsDefault {
			count++
		}
	}
	return count
}

// --- ownership ---------------------------------------------------------

func TestGetRefusesAnotherCustomersAddress(t *testing.T) {
	// The whole module exists to keep this true: an id alone is not authority.
	repo := newFakeRepo()
	service := newService(repo)
	owner, intruder := uuid.New(), uuid.New()
	address := seed(t, repo, service, owner, "home")

	_, err := service.Get(context.Background(), intruder, address.ID)

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestUpdateRefusesAnotherCustomersAddress(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	owner, intruder := uuid.New(), uuid.New()
	address := seed(t, repo, service, owner, "home")
	hijacked := "hijacked"

	_, err := service.Update(context.Background(), intruder, address.ID, customeraddress.Patch{Label: &hijacked})

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
	stored, findErr := service.Get(context.Background(), owner, address.ID)
	require.NoError(t, findErr)
	assert.Equal(t, "home", stored.Label, "the owner's row must be untouched")
}

func TestDeleteRefusesAnotherCustomersAddress(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	owner, intruder := uuid.New(), uuid.New()
	address := seed(t, repo, service, owner, "home")

	err := service.Delete(context.Background(), intruder, address.ID)

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
	_, findErr := service.Get(context.Background(), owner, address.ID)
	assert.NoError(t, findErr, "the address must still exist")
}

func TestSetDefaultRefusesAnotherCustomersAddress(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	owner, intruder := uuid.New(), uuid.New()
	address := seed(t, repo, service, owner, "home")

	err := service.SetDefault(context.Background(), intruder, address.ID)

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestListOnlyReturnsTheCallersOwnAddresses(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	owner, other := uuid.New(), uuid.New()
	seed(t, repo, service, owner, "home")
	seed(t, repo, service, other, "somebody else")

	items, err := service.List(context.Background(), owner)

	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "home", items[0].Label)
}

// --- exactly one default ------------------------------------------------

func TestFirstAddressBecomesTheDefault(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	userID := uuid.New()

	created := seed(t, repo, service, userID, "home")

	assert.True(t, created.IsDefault)
}

func TestLaterAddressesDoNotBecomeTheDefault(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	userID := uuid.New()
	seed(t, repo, service, userID, "home")

	second := seed(t, repo, service, userID, "office")

	assert.False(t, second.IsDefault)
	assert.Equal(t, 1, defaultCount(repo, userID))
}

func TestSetDefaultDemotesThePreviousOne(t *testing.T) {
	// Two defaults would make checkout choose arbitrarily between them.
	repo := newFakeRepo()
	service := newService(repo)
	userID := uuid.New()
	first := seed(t, repo, service, userID, "home")
	second := seed(t, repo, service, userID, "office")

	require.NoError(t, service.SetDefault(context.Background(), userID, second.ID))

	assert.Equal(t, 1, defaultCount(repo, userID))
	current, err := service.GetDefault(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, second.ID, current.ID)
	assert.NotEqual(t, first.ID, current.ID)
}

func TestSetDefaultLeavesTheOwnersDefaultIntactWhenTheAddressIsNotTheirs(t *testing.T) {
	// The promotion and the demotion share a transaction, so a rejected
	// promotion must not have cleared the flag on the way through.
	repo := newFakeRepo()
	service := newService(repo)
	owner, intruder := uuid.New(), uuid.New()
	ownerAddress := seed(t, repo, service, owner, "home")
	intruderAddress := seed(t, repo, service, intruder, "elsewhere")

	err := service.SetDefault(context.Background(), owner, intruderAddress.ID)

	require.Error(t, err)
	current, findErr := service.GetDefault(context.Background(), owner)
	require.NoError(t, findErr)
	assert.Equal(t, ownerAddress.ID, current.ID)
}

// --- deleting the default ----------------------------------------------

func TestDeletingTheDefaultPromotesAnother(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	userID := uuid.New()
	first := seed(t, repo, service, userID, "home")
	second := seed(t, repo, service, userID, "office")

	require.NoError(t, service.Delete(context.Background(), userID, first.ID))

	current, err := service.GetDefault(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, second.ID, current.ID)
	assert.Equal(t, 1, defaultCount(repo, userID))
}

func TestDeletingANonDefaultLeavesTheDefaultAlone(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	userID := uuid.New()
	first := seed(t, repo, service, userID, "home")
	second := seed(t, repo, service, userID, "office")

	require.NoError(t, service.Delete(context.Background(), userID, second.ID))

	current, err := service.GetDefault(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, first.ID, current.ID)
}

func TestDeletingTheLastAddressLeavesNoDefault(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	userID := uuid.New()
	only := seed(t, repo, service, userID, "home")

	require.NoError(t, service.Delete(context.Background(), userID, only.ID))

	_, err := service.GetDefault(context.Background(), userID)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

// --- default lookup -----------------------------------------------------

func TestGetDefaultReports404WhenNoneIsSet(t *testing.T) {
	// The service being replaced returned (nil, nil) here and the handler then
	// dereferenced it, so an ordinary "new customer" state was a panic.
	repo := newFakeRepo()
	service := newService(repo)

	_, err := service.GetDefault(context.Background(), uuid.New())

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

// --- partial update -----------------------------------------------------

func TestUpdateKeepsFieldsTheCallerDidNotSend(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	userID := uuid.New()
	address := seed(t, repo, service, userID, "home")
	label := "home (new gate)"

	updated, err := service.Update(context.Background(), userID, address.ID, customeraddress.Patch{Label: &label})

	require.NoError(t, err)
	assert.Equal(t, "home (new gate)", updated.Label)
	assert.Equal(t, "home street 1", updated.AddressText)
	assert.InDelta(t, -6.2, updated.Lat, 0.0001)
	assert.InDelta(t, 106.8, updated.Lng, 0.0001)
}

func TestUpdateAppliesEveryFieldTheCallerDidSend(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	userID := uuid.New()
	address := seed(t, repo, service, userID, "home")
	label, text := "office", "office street 9"
	lat, lng := -7.1, 110.4

	updated, err := service.Update(context.Background(), userID, address.ID, customeraddress.Patch{
		Label: &label, AddressText: &text, Lat: &lat, Lng: &lng,
	})

	require.NoError(t, err)
	assert.Equal(t, customeraddress.Address{
		ID:          address.ID,
		Label:       "office",
		AddressText: "office street 9",
		Lat:         -7.1,
		Lng:         110.4,
		IsDefault:   true,
		CreatedAt:   address.CreatedAt,
	}, updated)
}

// --- nearby shops -------------------------------------------------------

func TestNearbyShopsAsksTheDatabaseForABoundedRankedSet(t *testing.T) {
	// Ordering and the row cap belong in SQL; loading every shop and sorting in
	// Go would transfer the whole table on every storefront open.
	repo := newFakeRepo()
	repo.shops = []customeraddress.NearbyShop{{Name: "closest", Distance: "0.40 Km"}}
	service := newService(repo)

	shops, err := service.NearbyShops(context.Background(), customeraddress.Point{Lat: -6.2, Lng: 106.8})

	require.NoError(t, err)
	require.Len(t, shops, 1)
	assert.Equal(t, customeraddress.MaxNearbyShops, repo.lastOK.limit)
	assert.InDelta(t, customeraddress.DefaultRadiusKM, repo.lastOK.radius, 0.0001)
	assert.InDelta(t, -6.2, repo.lastOK.origin.Lat, 0.0001)
}

func TestNearbyShopsRejectsAnImpossibleCoordinate(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)

	tests := []struct {
		name  string
		point customeraddress.Point
	}{
		{"latitude past the pole", customeraddress.Point{Lat: 91, Lng: 106.8}},
		{"latitude below the pole", customeraddress.Point{Lat: -91, Lng: 106.8}},
		{"longitude past the antimeridian", customeraddress.Point{Lat: -6.2, Lng: 181}},
		{"longitude below the antimeridian", customeraddress.Point{Lat: -6.2, Lng: -181}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.NearbyShops(context.Background(), tc.point)

			assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
		})
	}
}

func TestOriginForUserFallsBackToTheDefaultAddress(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	userID := uuid.New()
	seed(t, repo, service, userID, "home")

	origin, err := service.OriginForUser(context.Background(), userID)

	require.NoError(t, err)
	assert.InDelta(t, -6.2, origin.Lat, 0.0001)
	assert.InDelta(t, 106.8, origin.Lng, 0.0001)
}

func TestOriginForUserReports404WithoutADefaultAddress(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)

	_, err := service.OriginForUser(context.Background(), uuid.New())

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

// --- routing ------------------------------------------------------------

func TestRegisterKeepsLiteralPathsOutOfTheIdWildcard(t *testing.T) {
	// `default`, `nearby-shops` and `public-nearby-shops` sit where an address
	// id goes. Go 1.22's ServeMux resolves by specificity rather than
	// registration order, but that is load-bearing enough to pin down.
	passthrough := func(next http.Handler) http.Handler { return next }
	handler := customeraddress.NewHandler(newService(newFakeRepo()))

	mux := http.NewServeMux()
	customeraddress.Register(mux, "/api/v1", handler, passthrough, passthrough)

	tests := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodGet, "/api/v1/customer/addresses", "GET /api/v1/customer/addresses"},
		{http.MethodGet, "/api/v1/customer/addresses/default", "GET /api/v1/customer/addresses/default"},
		{http.MethodGet, "/api/v1/customer/addresses/nearby-shops", "GET /api/v1/customer/addresses/nearby-shops"},
		{http.MethodGet, "/api/v1/customer/addresses/public-nearby-shops", "GET /api/v1/customer/addresses/public-nearby-shops"},
		{http.MethodGet, "/api/v1/customer/addresses/" + uuid.New().String(), "GET /api/v1/customer/addresses/{id}"},
		{http.MethodPost, "/api/v1/customer/addresses/create", "POST /api/v1/customer/addresses/create"},
		{http.MethodPut, "/api/v1/customer/addresses/" + uuid.New().String() + "/edit", "PUT /api/v1/customer/addresses/{id}/edit"},
		{http.MethodPut, "/api/v1/customer/addresses/" + uuid.New().String() + "/set-default", "PUT /api/v1/customer/addresses/{id}/set-default"},
		{http.MethodDelete, "/api/v1/customer/addresses/" + uuid.New().String() + "/delete", "DELETE /api/v1/customer/addresses/{id}/delete"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			_, pattern := mux.Handler(httptest.NewRequest(tc.method, tc.path, nil))

			assert.Equal(t, tc.want, pattern)
		})
	}
}

// --- request decoding ---------------------------------------------------

// signedIn builds a JSON request carrying a verified identity, the way the auth
// middleware would have left it.
func signedIn(method, target, body string, userID uuid.UUID) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{
		UserID: userID.String(),
		Role:   customeraddress.RoleCustomer,
	}))
}

func TestCreateAcceptsEveryLongitudeSpellingTheClientsSend(t *testing.T) {
	// The shipped apps send lat/latitude and lang/lng/longitude together, and
	// httpx.DecodeJSON rejects unknown fields -- so every alias has to be
	// declared or a real request from the field is a 400.
	repo := newFakeRepo()
	handler := customeraddress.NewHandler(newService(repo))
	userID := uuid.New()
	body := `{"label":"home","address_text":"street 1",
	          "lat":-6.2,"latitude":-6.2,"lang":106.8,"lng":106.8,"longitude":106.8}`

	rec := httptest.NewRecorder()
	err := handler.Create(rec, signedIn(http.MethodPost, "/customer/addresses/create", body, userID))

	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), `"lang":106.8`, "longitude ships under the key the clients read")
}

func TestCreateRejectsABodyWithoutACoordinate(t *testing.T) {
	repo := newFakeRepo()
	handler := customeraddress.NewHandler(newService(repo))

	rec := httptest.NewRecorder()
	err := handler.Create(rec, signedIn(http.MethodPost, "/customer/addresses/create",
		`{"label":"home","address_text":"street 1"}`, uuid.New()))

	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
}

func TestCreateRefusesAnUnauthenticatedRequest(t *testing.T) {
	// Identity comes from the token; there is no body field that could supply it.
	handler := customeraddress.NewHandler(newService(newFakeRepo()))
	r := httptest.NewRequest(http.MethodPost, "/customer/addresses/create", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")

	err := handler.Create(httptest.NewRecorder(), r)

	assert.Equal(t, http.StatusUnauthorized, statusOf(t, err))
}

func TestPublicNearbyShopsRequiresBothCoordinates(t *testing.T) {
	handler := customeraddress.NewHandler(newService(newFakeRepo()))

	tests := []struct {
		name  string
		query string
	}{
		{"nothing at all", ""},
		{"latitude only", "?lat=-6.2"},
		{"longitude only", "?lng=106.8"},
		{"a non-numeric latitude", "?lat=jakarta&lng=106.8"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/customer/addresses/public-nearby-shops"+tc.query, nil)

			err := handler.PublicNearbyShops(httptest.NewRecorder(), r)

			assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
		})
	}
}

func TestPublicNearbyShopsAnswersACompleteCoordinate(t *testing.T) {
	repo := newFakeRepo()
	repo.shops = []customeraddress.NearbyShop{{Name: "warung", Distance: "0.40 Km", ShopImages: []string{}}}
	handler := customeraddress.NewHandler(newService(repo))
	r := httptest.NewRequest(http.MethodGet, "/customer/addresses/public-nearby-shops?lat=-6.2&lng=106.8", nil)

	rec := httptest.NewRecorder()
	require.NoError(t, handler.PublicNearbyShops(rec, r))

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "warung")
	// The lean projection is what keeps the anonymous variant from handing out
	// staff and owner details along with the shop card.
	assert.NotContains(t, body, "shop_cashier")
	assert.NotContains(t, body, "shop_product")
	assert.NotContains(t, body, "created_by")
}

func TestNearbyShopsPrefersTheQueryCoordinateOverTheDefaultAddress(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	handler := customeraddress.NewHandler(service)
	userID := uuid.New()
	seed(t, repo, service, userID, "home") // -6.2 / 106.8

	rec := httptest.NewRecorder()
	r := signedIn(http.MethodGet, "/customer/addresses/nearby-shops?lat=-7.25&lng=112.75", "", userID)
	require.NoError(t, handler.NearbyShops(rec, r))

	assert.InDelta(t, -7.25, repo.lastOK.origin.Lat, 0.0001)
	assert.InDelta(t, 112.75, repo.lastOK.origin.Lng, 0.0001)
}

func TestNearbyShopsFallsBackToTheDefaultAddress(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	handler := customeraddress.NewHandler(service)
	userID := uuid.New()
	seed(t, repo, service, userID, "home")

	rec := httptest.NewRecorder()
	r := signedIn(http.MethodGet, "/customer/addresses/nearby-shops", "", userID)
	require.NoError(t, handler.NearbyShops(rec, r))

	assert.InDelta(t, -6.2, repo.lastOK.origin.Lat, 0.0001)
	assert.InDelta(t, 106.8, repo.lastOK.origin.Lng, 0.0001)
}

func TestHandlersRenderTheEnvelopeTheClientsExpect(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	handler := customeraddress.NewHandler(service)
	userID := uuid.New()
	address := seed(t, repo, service, userID, "home")
	seed(t, repo, service, userID, "office")

	tests := []struct {
		name    string
		call    func(http.ResponseWriter, *http.Request) error
		method  string
		target  string
		wantMsg string
	}{
		{"list", handler.List, http.MethodGet, "/customer/addresses", "addresses retrieved successfully"},
		{"get", handler.Get, http.MethodGet, "/customer/addresses/" + address.ID.String(), "address retrieved successfully"},
		{"default", handler.GetDefault, http.MethodGet, "/customer/addresses/default", "default address retrieved successfully"},
		{"set-default", handler.SetDefault, http.MethodPut, "/customer/addresses/" + address.ID.String() + "/set-default", "default address updated successfully"},
		{"delete", handler.Delete, http.MethodDelete, "/customer/addresses/" + address.ID.String() + "/delete", "address deleted successfully"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r := signedIn(tc.method, tc.target, "", userID)
			r.SetPathValue("id", address.ID.String())

			require.NoError(t, tc.call(rec, r))

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.wantMsg)
		})
	}
}

func TestHandlersRejectAMalformedIdInTheURL(t *testing.T) {
	handler := customeraddress.NewHandler(newService(newFakeRepo()))
	userID := uuid.New()

	for name, call := range map[string]func(http.ResponseWriter, *http.Request) error{
		"get":         handler.Get,
		"update":      handler.Update,
		"delete":      handler.Delete,
		"set-default": handler.SetDefault,
	} {
		t.Run(name, func(t *testing.T) {
			r := signedIn(http.MethodGet, "/customer/addresses/not-a-uuid", `{}`, userID)
			r.SetPathValue("id", "not-a-uuid")

			err := call(httptest.NewRecorder(), r)

			assert.Equal(t, http.StatusBadRequest, statusOf(t, err))
		})
	}
}

func TestUpdateRejectsABlankedRequiredField(t *testing.T) {
	// An explicit empty string is an attempt to clear the field, not an
	// omission -- and neither the label nor the street line may end up empty.
	repo := newFakeRepo()
	service := newService(repo)
	handler := customeraddress.NewHandler(service)
	userID := uuid.New()
	address := seed(t, repo, service, userID, "home")

	for name, body := range map[string]string{
		"label":        `{"label":"   "}`,
		"address_text": `{"address_text":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := signedIn(http.MethodPut, "/customer/addresses/x/edit", body, userID)
			r.SetPathValue("id", address.ID.String())

			err := handler.Update(httptest.NewRecorder(), r)

			assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
		})
	}
}

func TestUpdateSendsOnlyTheFieldsTheBodyCarried(t *testing.T) {
	repo := newFakeRepo()
	service := newService(repo)
	handler := customeraddress.NewHandler(service)
	userID := uuid.New()
	address := seed(t, repo, service, userID, "home")

	rec := httptest.NewRecorder()
	r := signedIn(http.MethodPut, "/customer/addresses/"+address.ID.String()+"/edit", `{"label":"office"}`, userID)
	r.SetPathValue("id", address.ID.String())
	require.NoError(t, handler.Update(rec, r))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"address_text":"home street 1"`)
}

// --- error translation --------------------------------------------------

func TestRepositoryFailuresBecomeA500WithoutLeakingTheDriverMessage(t *testing.T) {
	repo := newFakeRepo()
	repo.failWith = errors.New(`pq: relation "customer_addresses" does not exist`)
	service := newService(repo)

	_, err := service.List(context.Background(), uuid.New())

	var appErr *httpx.Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusInternalServerError, appErr.Status)
	assert.NotContains(t, appErr.Message, "does not exist")
}

func TestCreateReportsAConflictOnceTheAddressLimitIsReached(t *testing.T) {
	repo := newFakeRepo()
	repo.failWith = customeraddress.ErrTooMany
	service := newService(repo)

	_, err := service.Create(context.Background(), uuid.New(), customeraddress.Input{Label: "home"})

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
}
