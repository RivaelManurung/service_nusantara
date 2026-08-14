package user_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/user"
	"service_nusantara/internal/platform/oidc"
	"service_nusantara/internal/platform/otp"
)

// --- fakes -------------------------------------------------------------
//
// The ports are small enough that in-memory fakes cover the business rules
// without PostgreSQL or Redis, which is what makes these tests fast enough to
// run on every save.

type fakeRepo struct {
	accounts    map[string]user.Account
	roles       map[uuid.UUID]bool
	roleNames   map[string]uuid.UUID
	createdAt   time.Time
	failOnQuery error
}

func (f *fakeRepo) FindByPhone(_ context.Context, phone string) (user.Account, error) {
	for _, a := range f.accounts {
		if a.Phone != "" && a.Phone == phone {
			return a, nil
		}
	}
	return user.Account{}, user.ErrNotFound
}

func (f *fakeRepo) FindRoleIDByName(_ context.Context, name string) (uuid.UUID, error) {
	id, ok := f.roleNames[name]
	if !ok {
		return uuid.Nil, user.ErrNotFound
	}
	return id, nil
}

func (f *fakeRepo) MarkEmailVerified(_ context.Context, id uuid.UUID) error {
	for email, a := range f.accounts {
		if a.ID == id {
			a.EmailVerified = true
			f.accounts[email] = a
			return nil
		}
	}
	return user.ErrNotFound
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		accounts:  map[string]user.Account{},
		roles:     map[uuid.UUID]bool{},
		roleNames: map[string]uuid.UUID{},
		createdAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func (f *fakeRepo) add(account user.Account) {
	f.accounts[account.Email] = account
}

func (f *fakeRepo) Create(_ context.Context, account user.Account) (user.Account, error) {
	// Phone-only accounts have no email, so fall back to the id as the key.
	f.accounts[accountKey(account)] = account
	return account, nil
}

// accountKey mirrors the unique constraints: email when present, id otherwise.
func accountKey(a user.Account) string {
	if a.Email != "" {
		return a.Email
	}
	return a.ID.String()
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (user.Account, error) {
	for _, a := range f.accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return user.Account{}, user.ErrNotFound
}

func (f *fakeRepo) FindByEmail(_ context.Context, email string) (user.Account, error) {
	if f.failOnQuery != nil {
		return user.Account{}, f.failOnQuery
	}
	a, ok := f.accounts[email]
	if !ok {
		return user.Account{}, user.ErrNotFound
	}
	return a, nil
}

func (f *fakeRepo) ExistsByUsernameOrEmail(_ context.Context, username, email string) (bool, error) {
	for _, a := range f.accounts {
		if a.Username == username || a.Email == email {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) RoleExists(_ context.Context, roleID uuid.UUID) (bool, error) {
	return f.roles[roleID], nil
}

func (f *fakeRepo) UpdatePassword(_ context.Context, id uuid.UUID, hash string) error {
	for email, a := range f.accounts {
		if a.ID == id {
			a.PasswordHash = hash
			f.accounts[email] = a
			return nil
		}
	}
	return user.ErrNotFound
}

func (f *fakeRepo) FindProfile(_ context.Context, id uuid.UUID) (user.Profile, error) {
	for _, a := range f.accounts {
		if a.ID == id {
			return user.Profile{
				ID: a.ID.String(), Name: a.Name, Username: a.Username,
				Email: a.Email, EmailVerified: a.EmailVerified,
				Phone: a.Phone, PhoneVerified: a.PhoneVerified,
				Role: a.RoleName, Status: a.Status,
				CreatedAt: f.createdAt,
			}, nil
		}
	}
	return user.Profile{}, user.ErrNotFound
}

// fakeIdentities is an in-memory IdentityRepository.
type fakeIdentities struct {
	rows   []user.Identity
	linked int
}

func (f *fakeIdentities) FindBySubject(_ context.Context, provider, subject string) (user.Identity, error) {
	for _, row := range f.rows {
		if row.Provider == provider && row.Subject == subject {
			return row, nil
		}
	}
	return user.Identity{}, user.ErrNotFound
}

func (f *fakeIdentities) Link(_ context.Context, identity user.Identity) (user.Identity, error) {
	identity.ID = uuid.New()
	f.rows = append(f.rows, identity)
	f.linked++
	return identity, nil
}

func (f *fakeIdentities) TouchLogin(_ context.Context, _ uuid.UUID, _ time.Time) error { return nil }

func (f *fakeIdentities) ListForUser(_ context.Context, userID uuid.UUID) ([]user.Identity, error) {
	var out []user.Identity
	for _, row := range f.rows {
		if row.UserID == userID {
			out = append(out, row)
		}
	}
	return out, nil
}

// stubVerifier returns a fixed result, standing in for Google or Apple.
type stubVerifier struct {
	name   string
	claims oidc.Claims
	err    error
	// gotNonce records what the service forwarded, proving the nonce reaches
	// the verifier rather than being dropped.
	gotNonce string
}

func (v *stubVerifier) Name() string { return v.name }

func (v *stubVerifier) Verify(_ context.Context, _ string, nonce string) (oidc.Claims, error) {
	v.gotNonce = nonce
	if v.err != nil {
		return oidc.Claims{}, v.err
	}
	return v.claims, nil
}

// fakeOTP is an in-memory one-time code store.
type fakeOTP struct {
	issued   map[string]string
	failWith error
	lastCode string
}

func newFakeOTP() *fakeOTP { return &fakeOTP{issued: map[string]string{}} }

func (f *fakeOTP) Issue(_ context.Context, phone string) (string, time.Duration, error) {
	if f.failWith != nil {
		return "", time.Minute, f.failWith
	}
	code := "123456"
	f.issued[phone] = code
	f.lastCode = code
	return code, 5 * time.Minute, nil
}

func (f *fakeOTP) Verify(_ context.Context, phone, code string) error {
	if f.failWith != nil {
		return f.failWith
	}
	want, ok := f.issued[phone]
	if !ok {
		return otp.ErrNoActiveCode
	}
	if want != code {
		return otp.ErrCodeMismatch
	}
	delete(f.issued, phone)
	return nil
}

// recordingSender captures what would have been sent.
type recordingSender struct {
	sentTo   []string
	sentCode string
}

func (s *recordingSender) Send(_ context.Context, phone, code string) error {
	s.sentTo = append(s.sentTo, phone)
	s.sentCode = code
	return nil
}

type fakeSessions struct {
	byRefresh map[string]auth.Session
	revoked   map[string]bool
}

// RevokeAllForUser mirrors the real store: every session the user holds is
// deleted and each access token it issued is blacklisted.
func (f *fakeSessions) RevokeAllForUser(_ context.Context, userID string, _ time.Duration) error {
	for refresh, sess := range f.byRefresh {
		if sess.UserID == userID {
			f.revoked[sess.TokenID] = true
			delete(f.byRefresh, refresh)
		}
	}
	return nil
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{byRefresh: map[string]auth.Session{}, revoked: map[string]bool{}}
}

func (f *fakeSessions) Create(_ context.Context, refresh string, sess auth.Session, _ time.Duration) error {
	f.byRefresh[refresh] = sess
	return nil
}

func (f *fakeSessions) Get(_ context.Context, refresh string) (auth.Session, error) {
	sess, ok := f.byRefresh[refresh]
	if !ok {
		return auth.Session{}, auth.ErrSessionNotFound
	}
	return sess, nil
}

func (f *fakeSessions) Delete(_ context.Context, refresh string, _ auth.Session) error {
	delete(f.byRefresh, refresh)
	return nil
}

func (f *fakeSessions) Revoke(_ context.Context, tokenID string, _ time.Duration) error {
	f.revoked[tokenID] = true
	return nil
}

// fakeLimiter counts attempts per key and blocks past the configured maximum.
type fakeLimiter struct {
	attempts map[string]int
	max      int
}

func newFakeLimiter(max int) *fakeLimiter {
	return &fakeLimiter{attempts: map[string]int{}, max: max}
}

func (f *fakeLimiter) Allow(_ context.Context, key string) (bool, time.Duration, error) {
	f.attempts[key]++
	if f.attempts[key] > f.max {
		return false, time.Minute, nil
	}
	return true, 0, nil
}

func (f *fakeLimiter) Reset(_ context.Context, key string) error {
	delete(f.attempts, key)
	return nil
}

// --- helpers -----------------------------------------------------------

const testPassword = "supersecret123"

type harness struct {
	service    *user.Service
	repo       *fakeRepo
	sessions   *fakeSessions
	limiter    *fakeLimiter
	identities *fakeIdentities
	google     *stubVerifier
	otp        *fakeOTP
	sender     *recordingSender
	account    user.Account
}

const defaultRole = "customer"

func newHarness(t *testing.T, maxAttempts int) *harness {
	t.Helper()

	repo := newFakeRepo()
	sessions := newFakeSessions()
	limiter := newFakeLimiter(maxAttempts)
	identities := &fakeIdentities{}
	google := &stubVerifier{name: user.ProviderGoogle}
	codes := newFakeOTP()
	sender := &recordingSender{}
	hasher := auth.NewHasher(10)

	hash, err := hasher.Hash(testPassword)
	require.NoError(t, err)

	account := user.Account{
		ID:           uuid.New(),
		Name:         "Admin",
		Username:     "admin",
		Email:        "admin@nusantara.id",
		PasswordHash: hash,
		RoleID:       uuid.New(),
		RoleName:     user.RoleSuperAdmin,
		Status:       user.StatusActive,
	}
	repo.add(account)
	repo.roles[account.RoleID] = true

	// The default sign-up role must exist for social and phone sign-up.
	customerRoleID := uuid.New()
	repo.roles[customerRoleID] = true
	repo.roleNames[defaultRole] = customerRoleID

	tokens := auth.NewManager(testAuthConfig())

	service := user.NewService(user.Deps{
		Repo:               repo,
		Profiles:           repo,
		Identities:         identities,
		Tokens:             tokens,
		Sessions:           sessions,
		Hasher:             hasher,
		Logins:             limiter,
		Social:             map[string]user.IDTokenVerifier{user.ProviderGoogle: google},
		OTP:                codes,
		Sender:             sender,
		OTPResendCooldown:  time.Minute,
		DefaultRoleName:    defaultRole,
		DefaultCountryCode: "62",
	})

	return &harness{
		service:    service,
		repo:       repo,
		sessions:   sessions,
		limiter:    limiter,
		identities: identities,
		google:     google,
		otp:        codes,
		sender:     sender,
		account:    account,
	}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.True(t, errors.As(err, &appErr), "expected an *httpx.Error, got %v", err)
	return appErr.Status
}

// --- tests -------------------------------------------------------------

func TestLoginIssuesATokenPairForValidCredentials(t *testing.T) {
	h := newHarness(t, 5)

	pair, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Len(t, h.sessions.byRefresh, 1)
}

func TestLoginTreatsEmailCaseInsensitively(t *testing.T) {
	h := newHarness(t, 5)

	_, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: "  ADMIN@Nusantara.ID ", Password: testPassword,
	})

	assert.NoError(t, err)
}

func TestLoginGivesTheSameAnswerForUnknownEmailAndWrongPassword(t *testing.T) {
	// Distinct messages ("email incorrect" vs "password incorrect") let an
	// attacker enumerate which addresses are registered.
	h := newHarness(t, 5)

	_, unknownErr := h.service.Login(context.Background(), user.LoginRequest{
		Email: "nobody@nusantara.id", Password: testPassword,
	})
	_, wrongPassErr := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: "wrong-password",
	})

	require.Error(t, unknownErr)
	require.Error(t, wrongPassErr)
	assert.Equal(t, http.StatusUnauthorized, statusOf(t, unknownErr))
	assert.Equal(t, unknownErr.Error(), wrongPassErr.Error())
}

func TestLoginLocksOutAfterTooManyFailedAttempts(t *testing.T) {
	h := newHarness(t, 3)

	for range 3 {
		_, err := h.service.Login(context.Background(), user.LoginRequest{
			Email: h.account.Email, Password: "wrong-password",
		})
		require.Error(t, err)
	}

	// The fourth attempt is refused before the password is even checked.
	_, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})

	assert.Equal(t, http.StatusTooManyRequests, statusOf(t, err))
}

func TestLoginClearsTheAttemptCounterOnSuccess(t *testing.T) {
	h := newHarness(t, 3)

	_, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: "wrong-password",
	})
	require.Error(t, err)

	_, err = h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})
	require.NoError(t, err)

	assert.Empty(t, h.limiter.attempts)
}

func TestLoginRefusesAnInactiveAccount(t *testing.T) {
	h := newHarness(t, 5)
	inactive := h.account
	inactive.Status = 0
	h.repo.add(inactive)

	_, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})

	assert.Equal(t, http.StatusForbidden, statusOf(t, err))
}

func TestRefreshRotatesTheRefreshToken(t *testing.T) {
	// A refresh token is single use, so a stolen copy stops working as soon as
	// the legitimate client refreshes.
	h := newHarness(t, 5)
	first, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})
	require.NoError(t, err)

	second, err := h.service.Refresh(context.Background(), user.RefreshRequest{
		RefreshToken: first.RefreshToken,
	})
	require.NoError(t, err)

	assert.NotEqual(t, first.RefreshToken, second.RefreshToken)

	_, err = h.service.Refresh(context.Background(), user.RefreshRequest{
		RefreshToken: first.RefreshToken,
	})
	assert.Equal(t, http.StatusUnauthorized, statusOf(t, err))
}

func TestRefreshRevokesThePreviousAccessToken(t *testing.T) {
	h := newHarness(t, 5)
	first, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})
	require.NoError(t, err)
	previousSession := h.sessions.byRefresh[first.RefreshToken]

	_, err = h.service.Refresh(context.Background(), user.RefreshRequest{
		RefreshToken: first.RefreshToken,
	})
	require.NoError(t, err)

	assert.True(t, h.sessions.revoked[previousSession.TokenID])
}

func TestRefreshRejectsAnUnknownToken(t *testing.T) {
	h := newHarness(t, 5)

	_, err := h.service.Refresh(context.Background(), user.RefreshRequest{
		RefreshToken: "never-issued",
	})

	assert.Equal(t, http.StatusUnauthorized, statusOf(t, err))
}

func TestLogoutRevokesTheAccessTokenAndDropsTheSession(t *testing.T) {
	h := newHarness(t, 5)
	pair, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})
	require.NoError(t, err)
	session := h.sessions.byRefresh[pair.RefreshToken]

	err = h.service.Logout(context.Background(), auth.Identity{
		UserID: h.account.ID.String(), TokenID: session.TokenID,
	}, pair.RefreshToken)

	require.NoError(t, err)
	assert.True(t, h.sessions.revoked[session.TokenID])
	assert.Empty(t, h.sessions.byRefresh)
}

func TestLogoutRefusesToDropAnotherUsersSession(t *testing.T) {
	h := newHarness(t, 5)
	pair, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})
	require.NoError(t, err)

	err = h.service.Logout(context.Background(), auth.Identity{
		UserID: uuid.NewString(), TokenID: "someone-elses-token",
	}, pair.RefreshToken)

	assert.Equal(t, http.StatusForbidden, statusOf(t, err))
	assert.Len(t, h.sessions.byRefresh, 1)
}

func TestRegisterRejectsADuplicateEmail(t *testing.T) {
	h := newHarness(t, 5)

	_, err := h.service.Register(context.Background(), user.RegisterRequest{
		Name: "Another", Username: "another", Email: h.account.Email,
		Password: testPassword, RoleID: h.account.RoleID.String(),
	})

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
}

func TestRegisterRejectsAnUnknownRole(t *testing.T) {
	h := newHarness(t, 5)

	_, err := h.service.Register(context.Background(), user.RegisterRequest{
		Name: "New", Username: "new", Email: "new@nusantara.id",
		Password: testPassword, RoleID: uuid.NewString(),
	})

	assert.Equal(t, http.StatusBadRequest, statusOf(t, err))
}

func TestRegisterRejectsAMalformedRoleIDWithoutPanicking(t *testing.T) {
	// The previous implementation called uuid.MustParse on this request field,
	// so a malformed value panicked the handler goroutine.
	h := newHarness(t, 5)

	_, err := h.service.Register(context.Background(), user.RegisterRequest{
		Name: "New", Username: "new", Email: "new@nusantara.id",
		Password: testPassword, RoleID: "not-a-uuid",
	})

	assert.Equal(t, http.StatusBadRequest, statusOf(t, err))
}

func TestRegisterStoresAHashNotThePlaintextPassword(t *testing.T) {
	h := newHarness(t, 5)
	roleID := uuid.New()
	h.repo.roles[roleID] = true

	profile, err := h.service.Register(context.Background(), user.RegisterRequest{
		Name: "New", Username: "new", Email: "New@Nusantara.ID",
		Password: testPassword, RoleID: roleID.String(),
	})

	require.NoError(t, err)
	assert.Equal(t, "new@nusantara.id", profile.Email)
	stored := h.repo.accounts["new@nusantara.id"]
	assert.NotEqual(t, testPassword, stored.PasswordHash)
	assert.NotEmpty(t, stored.PasswordHash)
}

func TestChangePasswordRejectsAWrongCurrentPassword(t *testing.T) {
	h := newHarness(t, 5)

	err := h.service.ChangePassword(context.Background(),
		auth.Identity{UserID: h.account.ID.String(), TokenID: "tok"},
		user.ChangePasswordRequest{
			CurrentPassword:      "not-the-current-one",
			NewPassword:          "brand-new-password",
			ConfirmationPassword: "brand-new-password",
		})

	assert.Equal(t, http.StatusBadRequest, statusOf(t, err))
}

func TestChangePasswordRevokesTheCurrentToken(t *testing.T) {
	// Rotating a password must not leave the old session usable.
	h := newHarness(t, 5)

	err := h.service.ChangePassword(context.Background(),
		auth.Identity{UserID: h.account.ID.String(), TokenID: "current-token"},
		user.ChangePasswordRequest{
			CurrentPassword:      testPassword,
			NewPassword:          "brand-new-password",
			ConfirmationPassword: "brand-new-password",
		})

	require.NoError(t, err)
	assert.True(t, h.sessions.revoked["current-token"])
}

func TestProfileRejectsANonUUIDSubject(t *testing.T) {
	h := newHarness(t, 5)

	_, err := h.service.Profile(context.Background(), "not-a-uuid")

	assert.Equal(t, http.StatusUnauthorized, statusOf(t, err))
}

func TestChangePasswordEndsEverySessionTheAccountHolds(t *testing.T) {
	// Regression test: revoking only the calling token left a refresh token
	// stolen from another device working for its full 30-day lifetime, so the
	// password change did not lock the attacker out.
	h := newHarness(t, 5)

	phone, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})
	require.NoError(t, err)
	tablet, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})
	require.NoError(t, err)
	require.Len(t, h.sessions.byRefresh, 2)

	phoneSession := h.sessions.byRefresh[phone.RefreshToken]

	err = h.service.ChangePassword(context.Background(),
		auth.Identity{UserID: h.account.ID.String(), TokenID: phoneSession.TokenID},
		user.ChangePasswordRequest{
			CurrentPassword:      testPassword,
			NewPassword:          "brand-new-password",
			ConfirmationPassword: "brand-new-password",
		})
	require.NoError(t, err)

	// The other device's refresh token must no longer mint access tokens.
	assert.Empty(t, h.sessions.byRefresh)
	_, err = h.service.Refresh(context.Background(), user.RefreshRequest{
		RefreshToken: tablet.RefreshToken,
	})
	assert.Equal(t, http.StatusUnauthorized, statusOf(t, err))
}

func TestChangePasswordLeavesOtherAccountsAlone(t *testing.T) {
	h := newHarness(t, 5)
	victim, err := h.service.Login(context.Background(), user.LoginRequest{
		Email: h.account.Email, Password: testPassword,
	})
	require.NoError(t, err)

	// A session belonging to a different user must survive.
	other := auth.Session{UserID: uuid.NewString(), Role: user.RoleAdmin, TokenID: "other-token"}
	require.NoError(t, h.sessions.Create(context.Background(), "other-refresh", other, time.Hour))

	err = h.service.ChangePassword(context.Background(),
		auth.Identity{UserID: h.account.ID.String(), TokenID: h.sessions.byRefresh[victim.RefreshToken].TokenID},
		user.ChangePasswordRequest{
			CurrentPassword:      testPassword,
			NewPassword:          "brand-new-password",
			ConfirmationPassword: "brand-new-password",
		})
	require.NoError(t, err)

	assert.Contains(t, h.sessions.byRefresh, "other-refresh")
	assert.False(t, h.sessions.revoked["other-token"])
}
