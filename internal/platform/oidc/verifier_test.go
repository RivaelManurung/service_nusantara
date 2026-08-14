package oidc_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/platform/oidc"
)

const (
	testKid      = "test-key-1"
	testAudience = "nusantara.apps.googleusercontent.com"
	googleIssuer = "https://accounts.google.com"
)

// jwksServer serves a key set for a generated RSA key and counts how often it
// was fetched, so caching and refresh throttling can be asserted.
type jwksServer struct {
	*httptest.Server
	key     *rsa.PrivateKey
	fetches atomic.Int32
}

func newJWKSServer(t *testing.T) *jwksServer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	server := &jwksServer{key: key}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": testKid,
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	}))
	t.Cleanup(server.Close)

	return server
}

// sign mints an ID token, letting each test bend one field at a time.
func (s *jwksServer) sign(t *testing.T, claims jwt.MapClaims, kid string) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid

	signed, err := token.SignedString(s.key)
	require.NoError(t, err)
	return signed
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss":            googleIssuer,
		"aud":            testAudience,
		"sub":            "google-sub-1",
		"email":          "Budi@Gmail.com",
		"email_verified": true,
		"name":           "Budi",
		"exp":            time.Now().Add(time.Hour).Unix(),
		"iat":            time.Now().Unix(),
	}
}

// newVerifier points a Google verifier at the test JWKS server.
func newVerifier(t *testing.T, server *jwksServer, audiences ...string) *oidc.Verifier {
	t.Helper()
	if len(audiences) == 0 {
		audiences = []string{testAudience}
	}
	return oidc.NewVerifierForTesting("google", server.URL, []string{googleIssuer}, oidc.Config{
		Audiences:   audiences,
		HTTPTimeout: 5 * time.Second,
		CacheTTL:    time.Hour,
	})
}

func TestVerifyAcceptsAWellFormedToken(t *testing.T) {
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	claims, err := verifier.Verify(context.Background(), server.sign(t, validClaims(), testKid), "")

	require.NoError(t, err)
	assert.Equal(t, "google-sub-1", claims.Subject)
	assert.True(t, claims.EmailVerified)
	// The address is lowercased so it matches however it was stored.
	assert.Equal(t, "budi@gmail.com", claims.Email)
}

func TestVerifyRejectsATokenMintedForAnotherApp(t *testing.T) {
	// Without the audience check, an ID token obtained by any other Google
	// client would be accepted here as proof of identity.
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	claims := validClaims()
	claims["aud"] = "someone-elses-app.apps.googleusercontent.com"

	_, err := verifier.Verify(context.Background(), server.sign(t, claims, testKid), "")

	assert.ErrorIs(t, err, oidc.ErrInvalidToken)
}

func TestVerifyRejectsAnUnexpectedIssuer(t *testing.T) {
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	claims := validClaims()
	claims["iss"] = "https://evil.example.com"

	_, err := verifier.Verify(context.Background(), server.sign(t, claims, testKid), "")

	assert.ErrorIs(t, err, oidc.ErrInvalidToken)
}

func TestVerifyRejectsAnExpiredToken(t *testing.T) {
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	claims := validClaims()
	claims["exp"] = time.Now().Add(-time.Hour).Unix()

	_, err := verifier.Verify(context.Background(), server.sign(t, claims, testKid), "")

	assert.ErrorIs(t, err, oidc.ErrInvalidToken)
}

func TestVerifyRejectsATokenSignedByAnUnknownKey(t *testing.T) {
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	// A different key entirely, presented under the published kid.
	attacker, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims())
	token.Header["kid"] = testKid
	forged, err := token.SignedString(attacker)
	require.NoError(t, err)

	_, err = verifier.Verify(context.Background(), forged, "")

	assert.ErrorIs(t, err, oidc.ErrInvalidToken)
}

func TestVerifyRejectsAnUnsignedToken(t *testing.T) {
	// The "none" algorithm is the classic bypass.
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	token := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims())
	token.Header["kid"] = testKid
	unsigned, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = verifier.Verify(context.Background(), unsigned, "")

	assert.ErrorIs(t, err, oidc.ErrInvalidToken)
}

func TestVerifyRejectsAMismatchedNonce(t *testing.T) {
	// The nonce binds the token to this sign-in attempt, so a token captured
	// from another one cannot be replayed.
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	claims := validClaims()
	claims["nonce"] = "nonce-from-another-attempt"

	_, err := verifier.Verify(context.Background(), server.sign(t, claims, testKid), "expected-nonce")

	assert.ErrorIs(t, err, oidc.ErrInvalidToken)
}

func TestVerifyAcceptsAMatchingNonce(t *testing.T) {
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	claims := validClaims()
	claims["nonce"] = "expected-nonce"

	_, err := verifier.Verify(context.Background(), server.sign(t, claims, testKid), "expected-nonce")

	assert.NoError(t, err)
}

func TestVerifyReadsAppleStyleStringBooleans(t *testing.T) {
	// Google sends email_verified as a boolean, Apple as the string "true".
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	claims := validClaims()
	claims["email_verified"] = "true"
	claims["is_private_email"] = "true"

	result, err := verifier.Verify(context.Background(), server.sign(t, claims, testKid), "")

	require.NoError(t, err)
	assert.True(t, result.EmailVerified)
	assert.True(t, result.IsPrivateEmail)
}

func TestVerifyRefusesWhenNoAudienceIsConfigured(t *testing.T) {
	// An empty audience list must disable the provider, not accept everything.
	server := newJWKSServer(t)
	verifier := oidc.NewVerifierForTesting("google", server.URL, []string{googleIssuer}, oidc.Config{})

	_, err := verifier.Verify(context.Background(), server.sign(t, validClaims(), testKid), "")

	assert.Error(t, err)
	assert.Zero(t, server.fetches.Load(), "a disabled provider must not call out to the network")
}

func TestVerifyCachesTheKeySetAcrossCalls(t *testing.T) {
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	for range 5 {
		_, err := verifier.Verify(context.Background(), server.sign(t, validClaims(), testKid), "")
		require.NoError(t, err)
	}

	assert.Equal(t, int32(1), server.fetches.Load())
}

func TestVerifyDoesNotRefetchForEveryUnknownKid(t *testing.T) {
	// Otherwise a client could drive one outbound request per forged token.
	server := newJWKSServer(t)
	verifier := newVerifier(t, server)

	for i := range 10 {
		_, err := verifier.Verify(context.Background(),
			server.sign(t, validClaims(), "unknown-kid-"+string(rune('a'+i))), "")
		require.Error(t, err)
	}

	assert.LessOrEqual(t, server.fetches.Load(), int32(2),
		"unknown kids must not translate into unbounded jwks fetches")
}
