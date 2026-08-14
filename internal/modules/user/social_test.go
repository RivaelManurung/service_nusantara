package user_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/modules/user"
	"service_nusantara/internal/platform/oidc"
)

func TestSignInWithGoogleCreatesAnAccountOnFirstUse(t *testing.T) {
	h := newHarness(t, 5)
	h.google.claims = oidc.Claims{
		Subject:       "google-sub-1",
		Email:         "budi@gmail.com",
		EmailVerified: true,
		Name:          "Budi",
	}

	pair, err := h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "any-token"})

	require.NoError(t, err)
	assert.NotEmpty(t, pair.AccessToken)

	created := h.repo.accounts["budi@gmail.com"]
	assert.Equal(t, "Budi", created.Name)
	assert.True(t, created.EmailVerified)
	// A social account has no password, so password login must not work for it.
	assert.False(t, created.CanSignInWithPassword())
}

func TestSignInWithGoogleReusesTheAccountOnSecondUse(t *testing.T) {
	h := newHarness(t, 5)
	h.google.claims = oidc.Claims{Subject: "google-sub-1", Email: "budi@gmail.com", EmailVerified: true}

	_, err := h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "any-token"})
	require.NoError(t, err)
	accountsAfterFirst := len(h.repo.accounts)

	_, err = h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "any-token"})
	require.NoError(t, err)

	assert.Len(t, h.repo.accounts, accountsAfterFirst)
	assert.Equal(t, 1, h.identities.linked, "the identity must be linked once, not on every sign-in")
}

func TestSignInWithGoogleLinksToAnExistingAccountWhenTheEmailIsVerified(t *testing.T) {
	// Someone who registered with a password and later taps "Continue with
	// Google" must land on the same account, not a duplicate.
	h := newHarness(t, 5)
	h.google.claims = oidc.Claims{
		Subject:       "google-sub-1",
		Email:         h.account.Email,
		EmailVerified: true,
	}

	_, err := h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "any-token"})

	require.NoError(t, err)
	assert.Len(t, h.repo.accounts, 1)

	linked, err := h.identities.FindBySubject(context.Background(), user.ProviderGoogle, "google-sub-1")
	require.NoError(t, err)
	assert.Equal(t, h.account.ID, linked.UserID)
}

func TestSignInWithGoogleRefusesToLinkAnUnverifiedEmail(t *testing.T) {
	// This is the account-takeover path: if an unverified address were enough,
	// anyone could register elsewhere with a victim's email and claim their
	// account here.
	h := newHarness(t, 5)
	h.google.claims = oidc.Claims{
		Subject:       "attacker-sub",
		Email:         h.account.Email,
		EmailVerified: false,
	}

	_, err := h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "any-token"})

	assert.Equal(t, http.StatusConflict, statusOf(t, err))
	assert.Equal(t, 0, h.identities.linked)
}

func TestSignInWithGoogleRejectsAnInvalidToken(t *testing.T) {
	h := newHarness(t, 5)
	h.google.err = oidc.ErrInvalidToken

	_, err := h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "forged"})

	assert.Equal(t, http.StatusUnauthorized, statusOf(t, err))
	assert.Empty(t, h.repo.accounts["budi@gmail.com"].ID)
}

func TestSignInWithGoogleReportsAProviderOutageAsRetryable(t *testing.T) {
	// A JWKS fetch failure is our problem; answering 401 would tell the user
	// their perfectly good credentials were rejected.
	h := newHarness(t, 5)
	h.google.err = errors.New("fetch jwks: connection refused")

	_, err := h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "any-token"})

	assert.Equal(t, http.StatusServiceUnavailable, statusOf(t, err))
}

func TestSignInWithADisabledProviderIsNotFound(t *testing.T) {
	// Apple is not in the harness's provider map.
	h := newHarness(t, 5)

	_, err := h.service.SignInWithProvider(context.Background(), user.ProviderApple,
		user.SocialLoginRequest{IDToken: "any-token"})

	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestSignInForwardsTheNonceToTheVerifier(t *testing.T) {
	h := newHarness(t, 5)
	h.google.claims = oidc.Claims{Subject: "google-sub-1", Email: "budi@gmail.com", EmailVerified: true}

	_, err := h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "any-token", Nonce: "nonce-from-the-app"})

	require.NoError(t, err)
	assert.Equal(t, "nonce-from-the-app", h.google.gotNonce)
}

func TestSignInWithGoogleFallsBackToTheClientSuppliedName(t *testing.T) {
	// Apple only reveals the name to the client, never in the token.
	h := newHarness(t, 5)
	h.google.claims = oidc.Claims{Subject: "sub-2", Email: "siti@gmail.com", EmailVerified: true}

	_, err := h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "any-token", Name: "Siti"})

	require.NoError(t, err)
	assert.Equal(t, "Siti", h.repo.accounts["siti@gmail.com"].Name)
}

func TestSignInMethodsListsEveryLinkedProvider(t *testing.T) {
	h := newHarness(t, 5)
	h.google.claims = oidc.Claims{Subject: "sub-1", Email: h.account.Email, EmailVerified: true}

	_, err := h.service.SignInWithProvider(context.Background(), user.ProviderGoogle,
		user.SocialLoginRequest{IDToken: "any-token"})
	require.NoError(t, err)

	methods, err := h.service.SignInMethods(context.Background(), h.account.ID.String())

	require.NoError(t, err)
	require.Len(t, methods, 1)
	assert.Equal(t, user.ProviderGoogle, methods[0].Provider)
}
