// Package auth issues and verifies credentials.
//
// Two deliberate changes from the previous service:
//
//  1. Access tokens are short lived and carry a unique jti. Revocation stores
//     the jti, not the whole token string, so the blacklist is small and a
//     token cannot be "un-revoked" by re-login on another device.
//  2. The signing algorithm is pinned. Accepting whatever `alg` the token
//     declares is how HS256/none confusion attacks succeed.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"service_nusantara/internal/config"
)

// Errors callers distinguish when verifying a token.
var (
	ErrTokenInvalid = errors.New("token is invalid")
	ErrTokenExpired = errors.New("token has expired")
)

// Claims is the access token payload.
type Claims struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// TokenID returns the jti used for revocation lookups.
func (c Claims) TokenID() string { return c.ID }

// TokenPair is what a successful login or refresh returns.
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Manager mints and verifies tokens. It holds no request state and is safe for
// concurrent use.
type Manager struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

// NewManager builds a Manager from validated configuration.
func NewManager(cfg config.Auth) *Manager {
	return &Manager{
		secret:     []byte(cfg.JWTSecret),
		issuer:     cfg.Issuer,
		accessTTL:  cfg.AccessTokenTTL,
		refreshTTL: cfg.RefreshTokenTTL,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// AccessTTL exposes the configured lifetime for session bookkeeping.
func (m *Manager) AccessTTL() time.Duration { return m.accessTTL }

// RefreshTTL exposes the configured refresh lifetime.
func (m *Manager) RefreshTTL() time.Duration { return m.refreshTTL }

// Issue mints an access/refresh pair for a user. The returned jti identifies
// this specific session so a single device can be logged out on its own.
func (m *Manager) Issue(userID, role string) (TokenPair, string, error) {
	issuedAt := m.now()
	expiresAt := issuedAt.Add(m.accessTTL)
	tokenID := uuid.NewString()

	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        tokenID,
			Subject:   userID,
			Issuer:    m.issuer,
			IssuedAt:  jwt.NewNumericDate(issuedAt),
			NotBefore: jwt.NewNumericDate(issuedAt),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	access, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return TokenPair{}, "", fmt.Errorf("sign access token: %w", err)
	}

	// The refresh token is opaque random data, not a JWT: it carries no claims
	// a client could read and is only meaningful against the session store.
	refresh, err := randomToken()
	if err != nil {
		return TokenPair{}, "", fmt.Errorf("generate refresh token: %w", err)
	}

	return TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresIn:    int64(m.accessTTL.Seconds()),
		ExpiresAt:    expiresAt,
	}, tokenID, nil
}

// Verify parses and checks an access token, pinning the signing algorithm and
// the issuer.
func (m *Manager) Verify(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{},
		func(t *jwt.Token) (any, error) {
			// Without this check any token signed with "none" or an asymmetric
			// public key would be accepted.
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("%w: unexpected signing method %v", ErrTokenInvalid, t.Header["alg"])
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}

	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, ErrTokenInvalid
	}
	// A token without these is structurally valid but useless downstream, and
	// letting it through is what forced the old handlers into unchecked type
	// assertions on the claims map.
	if claims.UserID == "" || claims.ID == "" {
		return nil, fmt.Errorf("%w: missing user_id or jti", ErrTokenInvalid)
	}

	return claims, nil
}

// randomToken returns 256 bits of URL-safe entropy.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
