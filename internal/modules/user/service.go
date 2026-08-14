package user

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// Ports the service depends on. Each is small enough to fake in a test, which
// is what lets the login lockout rules below be covered without Redis.
type (
	tokenIssuer interface {
		Issue(userID, role string) (auth.TokenPair, string, error)
		AccessTTL() time.Duration
		RefreshTTL() time.Duration
	}

	sessionStore interface {
		Create(ctx context.Context, refreshToken string, sess auth.Session, ttl time.Duration) error
		Get(ctx context.Context, refreshToken string) (auth.Session, error)
		Delete(ctx context.Context, refreshToken string, sess auth.Session) error
		Revoke(ctx context.Context, tokenID string, ttl time.Duration) error
		RevokeAllForUser(ctx context.Context, userID string, accessTTL time.Duration) error
	}

	passwordHasher interface {
		Hash(password string) (string, error)
		Compare(hash, password string) error
	}

	// attemptLimiter throttles credential checks. Allow reports whether another
	// attempt may be made and how long the caller must wait otherwise.
	attemptLimiter interface {
		Allow(ctx context.Context, key string) (bool, time.Duration, error)
		Reset(ctx context.Context, key string) error
	}

	profileReader interface {
		FindProfile(ctx context.Context, id uuid.UUID) (Profile, error)
	}
)

// Service holds the account business rules.
type Service struct {
	repo       Repository
	profiles   profileReader
	identities IdentityRepository
	tokens     tokenIssuer
	sessions   sessionStore
	hasher     passwordHasher
	logins     attemptLimiter
	log        *slog.Logger

	// social holds one verifier per enabled provider; an absent key means that
	// provider is switched off rather than misconfigured.
	social map[string]IDTokenVerifier

	// otp and sender are nil when phone sign-in is disabled.
	otp               otpStore
	sender            otpSender
	otpResendCooldown time.Duration

	defaultRoleName    string
	defaultCountryCode string
}

// Deps is the Service's dependency set. It is a struct rather than a long
// parameter list because the constructor now takes eleven collaborators, and a
// positional call of that length is impossible to read or to get right.
type Deps struct {
	Repo       Repository
	Profiles   profileReader
	Identities IdentityRepository
	Tokens     tokenIssuer
	Sessions   sessionStore
	Hasher     passwordHasher
	Logins     attemptLimiter
	Logger     *slog.Logger

	// Social is keyed by provider name; leave a provider out to disable it.
	Social map[string]IDTokenVerifier

	// OTP and Sender must be supplied together, or both left nil.
	OTP               otpStore
	Sender            otpSender
	OTPResendCooldown time.Duration

	DefaultRoleName    string
	DefaultCountryCode string
}

func NewService(deps Deps) *Service {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Social == nil {
		deps.Social = map[string]IDTokenVerifier{}
	}

	return &Service{
		repo:               deps.Repo,
		profiles:           deps.Profiles,
		identities:         deps.Identities,
		tokens:             deps.Tokens,
		sessions:           deps.Sessions,
		hasher:             deps.Hasher,
		logins:             deps.Logins,
		log:                deps.Logger,
		social:             deps.Social,
		otp:                deps.OTP,
		sender:             deps.Sender,
		otpResendCooldown:  deps.OTPResendCooldown,
		defaultRoleName:    deps.DefaultRoleName,
		defaultCountryCode: deps.DefaultCountryCode,
	}
}

// Register creates a back-office account.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (Profile, error) {
	email := normalizeEmail(req.Email)
	username := strings.TrimSpace(req.Username)

	// uuid.MustParse panics on malformed input. The previous service called it
	// on a request field, so any client could crash the handler goroutine.
	roleID, err := uuid.Parse(req.RoleID)
	if err != nil {
		return Profile{}, httpx.BadRequest("role_id must be a valid UUID").WithCause(err)
	}

	roleExists, err := s.repo.RoleExists(ctx, roleID)
	if err != nil {
		return Profile{}, httpx.Internal("failed to verify role").WithCause(err)
	}
	if !roleExists {
		return Profile{}, httpx.BadRequest("role does not exist")
	}

	taken, err := s.repo.ExistsByUsernameOrEmail(ctx, username, email)
	if err != nil {
		return Profile{}, httpx.Internal("failed to verify account availability").WithCause(err)
	}
	if taken {
		return Profile{}, httpx.Conflict("username or email is already registered")
	}

	hash, err := s.hasher.Hash(req.Password)
	if err != nil {
		return Profile{}, httpx.Internal("failed to secure password").WithCause(err)
	}

	created, err := s.repo.Create(ctx, Account{
		ID:           uuid.New(),
		Name:         strings.TrimSpace(req.Name),
		Username:     username,
		Email:        email,
		PasswordHash: hash,
		RoleID:       roleID,
		Status:       StatusActive,
	})
	if err != nil {
		return Profile{}, httpx.Internal("failed to create account").WithCause(err)
	}

	// Record the password method alongside any social ones, so /auth/methods
	// can tell the client every way this account can sign in.
	if _, err := s.identities.Link(ctx, Identity{
		UserID:   created.ID,
		Provider: ProviderPassword,
		Subject:  email,
		Email:    email,
	}); err != nil {
		return Profile{}, httpx.Internal("failed to record sign-in method").WithCause(err)
	}

	return s.profiles.FindProfile(ctx, created.ID)
}

// StatusActive marks an account that may sign in.
const StatusActive = 1

// Login verifies credentials and issues a token pair.
func (s *Service) Login(ctx context.Context, req LoginRequest) (auth.TokenPair, error) {
	email := normalizeEmail(req.Email)
	limiterKey := "login:" + email

	allowed, retryAfter, err := s.logins.Allow(ctx, limiterKey)
	if err != nil {
		return auth.TokenPair{}, httpx.Internal("failed to check login attempts").WithCause(err)
	}
	if !allowed {
		return auth.TokenPair{}, httpx.RateLimited("too many failed login attempts, please try again later").
			WithDetails(map[string]any{"retry_after_seconds": int(retryAfter.Seconds())})
	}

	account, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// One message for both "no such email" and "wrong password", so the
			// endpoint cannot be used to enumerate registered addresses. The
			// previous service answered "email incorrect" vs "password
			// incorrect".
			return auth.TokenPair{}, httpx.Unauthorized("email or password is incorrect")
		}
		return auth.TokenPair{}, httpx.Internal("failed to load account").WithCause(err)
	}

	// A Google- or phone-only account has no password. Falling through to
	// bcrypt with an empty hash would still fail, but rejecting explicitly
	// keeps the reason clear in the logs.
	if !account.CanSignInWithPassword() {
		return auth.TokenPair{}, httpx.Unauthorized("email or password is incorrect")
	}

	if err := s.hasher.Compare(account.PasswordHash, req.Password); err != nil {
		return auth.TokenPair{}, httpx.Unauthorized("email or password is incorrect")
	}

	if account.Status != StatusActive {
		return auth.TokenPair{}, httpx.Forbidden("this account is not active")
	}

	// Only a successful login clears the counter.
	if err := s.logins.Reset(ctx, limiterKey); err != nil {
		return auth.TokenPair{}, httpx.Internal("failed to reset login attempts").WithCause(err)
	}

	return s.issueSession(ctx, account.ID.String(), account.RoleName)
}

// Refresh rotates a refresh token into a fresh pair.
func (s *Service) Refresh(ctx context.Context, req RefreshRequest) (auth.TokenPair, error) {
	session, err := s.sessions.Get(ctx, req.RefreshToken)
	if err != nil {
		if errors.Is(err, auth.ErrSessionNotFound) {
			return auth.TokenPair{}, httpx.Unauthorized("refresh token is invalid or has expired")
		}
		return auth.TokenPair{}, httpx.Internal("failed to read session").WithCause(err)
	}

	// Rotate: the presented refresh token is single use, so a stolen copy is
	// only useful until the legitimate client refreshes.
	if err := s.sessions.Delete(ctx, req.RefreshToken, session); err != nil {
		return auth.TokenPair{}, httpx.Internal("failed to rotate session").WithCause(err)
	}
	if err := s.sessions.Revoke(ctx, session.TokenID, s.tokens.AccessTTL()); err != nil {
		return auth.TokenPair{}, httpx.Internal("failed to revoke previous token").WithCause(err)
	}

	return s.issueSession(ctx, session.UserID, session.Role)
}

// Logout revokes the caller's access token and drops the refresh session.
func (s *Service) Logout(ctx context.Context, identity auth.Identity, refreshToken string) error {
	if err := s.sessions.Revoke(ctx, identity.TokenID, s.tokens.AccessTTL()); err != nil {
		return httpx.Internal("failed to revoke access token").WithCause(err)
	}

	// The refresh token is optional: a client that only holds an access token
	// can still log out, it simply cannot invalidate a refresh token it did not
	// send.
	if refreshToken == "" {
		return nil
	}

	session, err := s.sessions.Get(ctx, refreshToken)
	if err != nil {
		if errors.Is(err, auth.ErrSessionNotFound) {
			return nil
		}
		return httpx.Internal("failed to read session").WithCause(err)
	}
	if session.UserID != identity.UserID {
		// Presenting someone else's refresh token must not delete their session.
		return httpx.Forbidden("refresh token does not belong to this account")
	}
	if err := s.sessions.Delete(ctx, refreshToken, session); err != nil {
		return httpx.Internal("failed to delete session").WithCause(err)
	}
	return nil
}

// Profile returns the caller's own account.
func (s *Service) Profile(ctx context.Context, userID string) (Profile, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return Profile{}, httpx.Unauthorized("token subject is not a valid user id").WithCause(err)
	}

	profile, err := s.profiles.FindProfile(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Profile{}, httpx.NotFound("account no longer exists")
		}
		return Profile{}, httpx.Internal("failed to load profile").WithCause(err)
	}
	return profile, nil
}

// ChangePassword rotates the caller's password and revokes the current token so
// the client must sign in again with the new credentials.
func (s *Service) ChangePassword(ctx context.Context, identity auth.Identity, req ChangePasswordRequest) error {
	id, err := uuid.Parse(identity.UserID)
	if err != nil {
		return httpx.Unauthorized("token subject is not a valid user id").WithCause(err)
	}

	account, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return httpx.NotFound("account no longer exists")
		}
		return httpx.Internal("failed to load account").WithCause(err)
	}

	if err := s.hasher.Compare(account.PasswordHash, req.CurrentPassword); err != nil {
		return httpx.BadRequest("current password is incorrect")
	}

	hash, err := s.hasher.Hash(req.NewPassword)
	if err != nil {
		return httpx.Internal("failed to secure password").WithCause(err)
	}

	if err := s.repo.UpdatePassword(ctx, id, hash); err != nil {
		return httpx.Internal("failed to update password").WithCause(err)
	}

	// Every session must end, not just the one that made this request. A
	// refresh token stolen from another device would otherwise keep minting
	// access tokens for its full lifetime, so the password change would not
	// actually lock the attacker out.
	if err := s.sessions.RevokeAllForUser(ctx, identity.UserID, s.tokens.AccessTTL()); err != nil {
		return httpx.Internal("failed to end existing sessions").WithCause(err)
	}
	if err := s.sessions.Revoke(ctx, identity.TokenID, s.tokens.AccessTTL()); err != nil {
		return httpx.Internal("failed to revoke current token").WithCause(err)
	}
	return nil
}

// issueSession mints a token pair and records the refresh side of it.
func (s *Service) issueSession(ctx context.Context, userID, role string) (auth.TokenPair, error) {
	pair, tokenID, err := s.tokens.Issue(userID, role)
	if err != nil {
		return auth.TokenPair{}, httpx.Internal("failed to issue token").WithCause(err)
	}

	err = s.sessions.Create(ctx, pair.RefreshToken, auth.Session{
		UserID:  userID,
		Role:    role,
		TokenID: tokenID,
	}, s.tokens.RefreshTTL())
	if err != nil {
		return auth.TokenPair{}, httpx.Internal("failed to persist session").WithCause(err)
	}

	return pair, nil
}

// normalizeEmail makes lookups and uniqueness checks case insensitive, so
// "Admin@x.com" and "admin@x.com" cannot become two accounts.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
