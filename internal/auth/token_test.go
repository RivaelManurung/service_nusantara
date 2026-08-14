package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/config"
)

func newManager(t *testing.T, accessTTL time.Duration) *auth.Manager {
	t.Helper()
	return auth.NewManager(config.Auth{
		JWTSecret:       "test-secret-that-is-at-least-32-bytes-long",
		Issuer:          "nusantara-test",
		AccessTokenTTL:  accessTTL,
		RefreshTokenTTL: time.Hour,
	})
}

func TestVerifyReturnsClaimsForFreshlyIssuedToken(t *testing.T) {
	// Arrange
	manager := newManager(t, time.Minute)

	// Act
	pair, tokenID, err := manager.Issue("user-1", "superadmin")
	require.NoError(t, err)
	claims, err := manager.Verify(pair.AccessToken)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.UserID)
	assert.Equal(t, "superadmin", claims.Role)
	assert.Equal(t, tokenID, claims.TokenID())
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, "Bearer", pair.TokenType)
}

func TestIssueGivesEachSessionADistinctTokenID(t *testing.T) {
	// Two devices must be revocable independently.
	manager := newManager(t, time.Minute)

	_, firstID, err := manager.Issue("user-1", "admin")
	require.NoError(t, err)
	_, secondID, err := manager.Issue("user-1", "admin")
	require.NoError(t, err)

	assert.NotEqual(t, firstID, secondID)
}

func TestIssueGivesEachSessionADistinctRefreshToken(t *testing.T) {
	manager := newManager(t, time.Minute)

	first, _, err := manager.Issue("user-1", "admin")
	require.NoError(t, err)
	second, _, err := manager.Issue("user-1", "admin")
	require.NoError(t, err)

	assert.NotEqual(t, first.RefreshToken, second.RefreshToken)
}

func TestVerifyReportsExpiredTokensDistinctly(t *testing.T) {
	// A negative TTL yields a token that was already expired when signed.
	manager := newManager(t, -time.Minute)

	pair, _, err := manager.Issue("user-1", "admin")
	require.NoError(t, err)

	_, err = manager.Verify(pair.AccessToken)

	// Clients refresh on ErrTokenExpired but re-authenticate on ErrTokenInvalid.
	assert.ErrorIs(t, err, auth.ErrTokenExpired)
}

func TestVerifyRejectsTokenSignedWithAnotherSecret(t *testing.T) {
	issuer := newManager(t, time.Minute)
	pair, _, err := issuer.Issue("user-1", "admin")
	require.NoError(t, err)

	other := auth.NewManager(config.Auth{
		JWTSecret:       "a-completely-different-secret-value-32b",
		Issuer:          "nusantara-test",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
	})

	_, err = other.Verify(pair.AccessToken)

	assert.ErrorIs(t, err, auth.ErrTokenInvalid)
}

func TestVerifyRejectsUnsignedToken(t *testing.T) {
	// The "none" algorithm is the classic JWT bypass: a token with no signature
	// must never be accepted, however well-formed its claims are.
	manager := newManager(t, time.Minute)

	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, auth.Claims{
		UserID: "attacker",
		Role:   "superadmin",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "forged",
			Issuer:    "nusantara-test",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = manager.Verify(unsigned)

	assert.ErrorIs(t, err, auth.ErrTokenInvalid)
}

func TestVerifyRejectsTokenFromAnotherIssuer(t *testing.T) {
	foreign := auth.NewManager(config.Auth{
		JWTSecret:       "test-secret-that-is-at-least-32-bytes-long",
		Issuer:          "some-other-service",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
	})
	pair, _, err := foreign.Issue("user-1", "admin")
	require.NoError(t, err)

	_, err = newManager(t, time.Minute).Verify(pair.AccessToken)

	assert.ErrorIs(t, err, auth.ErrTokenInvalid)
}

func TestVerifyRejectsMalformedToken(t *testing.T) {
	_, err := newManager(t, time.Minute).Verify("not-a-jwt")

	assert.ErrorIs(t, err, auth.ErrTokenInvalid)
}
