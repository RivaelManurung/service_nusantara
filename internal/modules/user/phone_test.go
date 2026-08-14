package user_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/modules/user"
	"service_nusantara/internal/platform/otp"
)

func TestStartPhoneSignInSendsACodeToTheNormalizedNumber(t *testing.T) {
	h := newHarness(t, 5)

	result, err := h.service.StartPhoneSignIn(context.Background(),
		user.PhoneStartRequest{Phone: "0812-3456-7890"})

	require.NoError(t, err)
	assert.Positive(t, result.ExpiresInSeconds)
	assert.Equal(t, []string{"+6281234567890"}, h.sender.sentTo)
}

func TestPhoneNumberFormsAllResolveToTheSameAccount(t *testing.T) {
	// A person who typed 0812… on signup and +62812… on a new phone must not
	// end up with two accounts and two order histories.
	tests := []struct {
		name  string
		input string
	}{
		{"national with leading zero", "081234567890"},
		{"international with plus", "+6281234567890"},
		{"international without plus", "6281234567890"},
		{"with separators", "0812 3456-7890"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, 5)

			_, err := h.service.StartPhoneSignIn(context.Background(),
				user.PhoneStartRequest{Phone: tc.input})

			require.NoError(t, err)
			assert.Equal(t, []string{"+6281234567890"}, h.sender.sentTo)
		})
	}
}

func TestStartPhoneSignInRejectsANumberThatIsNotDigits(t *testing.T) {
	// The previous helper turned "0abc" into the stored number "+62abc".
	h := newHarness(t, 5)

	_, err := h.service.StartPhoneSignIn(context.Background(),
		user.PhoneStartRequest{Phone: "0abcdefgh"})

	assert.Equal(t, http.StatusBadRequest, statusOf(t, err))
	assert.Empty(t, h.sender.sentTo)
}

func TestStartPhoneSignInSurfacesTheResendCooldown(t *testing.T) {
	h := newHarness(t, 5)
	h.otp.failWith = otp.ErrCooldownActive

	_, err := h.service.StartPhoneSignIn(context.Background(),
		user.PhoneStartRequest{Phone: "081234567890"})

	assert.Equal(t, http.StatusTooManyRequests, statusOf(t, err))
}

func TestVerifyPhoneSignInCreatesAnAccountOnFirstUse(t *testing.T) {
	h := newHarness(t, 5)
	_, err := h.service.StartPhoneSignIn(context.Background(),
		user.PhoneStartRequest{Phone: "081234567890"})
	require.NoError(t, err)

	pair, err := h.service.VerifyPhoneSignIn(context.Background(),
		user.PhoneVerifyRequest{Phone: "081234567890", Code: h.otp.lastCode, Name: "Dewi"})

	require.NoError(t, err)
	assert.NotEmpty(t, pair.AccessToken)

	require.Len(t, h.repo.accounts, 2) // the seeded admin plus the new customer
	var created user.Account
	for _, a := range h.repo.accounts {
		if a.Phone == "+6281234567890" {
			created = a
		}
	}
	assert.Equal(t, "Dewi", created.Name)
	assert.True(t, created.PhoneVerified)
	// A phone-only account has neither email nor password.
	assert.Empty(t, created.Email)
	assert.False(t, created.CanSignInWithPassword())
}

func TestVerifyPhoneSignInReusesTheAccountOnSecondUse(t *testing.T) {
	h := newHarness(t, 5)

	for range 2 {
		_, err := h.service.StartPhoneSignIn(context.Background(),
			user.PhoneStartRequest{Phone: "081234567890"})
		require.NoError(t, err)
		_, err = h.service.VerifyPhoneSignIn(context.Background(),
			user.PhoneVerifyRequest{Phone: "081234567890", Code: h.otp.lastCode})
		require.NoError(t, err)
	}

	assert.Len(t, h.repo.accounts, 2)
	assert.Equal(t, 1, h.identities.linked)
}

func TestVerifyPhoneSignInRejectsAWrongCode(t *testing.T) {
	h := newHarness(t, 5)
	_, err := h.service.StartPhoneSignIn(context.Background(),
		user.PhoneStartRequest{Phone: "081234567890"})
	require.NoError(t, err)

	_, err = h.service.VerifyPhoneSignIn(context.Background(),
		user.PhoneVerifyRequest{Phone: "081234567890", Code: "000000"})

	assert.Equal(t, http.StatusUnauthorized, statusOf(t, err))
	assert.Len(t, h.repo.accounts, 1, "a failed code must not create an account")
}

func TestVerifyPhoneSignInRejectsACodeThatWasNeverRequested(t *testing.T) {
	h := newHarness(t, 5)

	_, err := h.service.VerifyPhoneSignIn(context.Background(),
		user.PhoneVerifyRequest{Phone: "081234567890", Code: "123456"})

	assert.Equal(t, http.StatusUnauthorized, statusOf(t, err))
}

func TestVerifyPhoneSignInReportsExhaustedAttempts(t *testing.T) {
	h := newHarness(t, 5)
	h.otp.failWith = otp.ErrTooManyAttempts

	_, err := h.service.VerifyPhoneSignIn(context.Background(),
		user.PhoneVerifyRequest{Phone: "081234567890", Code: "123456"})

	assert.Equal(t, http.StatusTooManyRequests, statusOf(t, err))
}

func TestVerifyPhoneSignInAdoptsAnAccountThatAlreadyHoldsTheNumber(t *testing.T) {
	// A back-office account created before the identity table existed must be
	// adopted, not duplicated onto a second row that collides on the unique
	// phone index.
	h := newHarness(t, 5)
	existing := h.account
	existing.Phone = "+6281234567890"
	h.repo.add(existing)

	_, err := h.service.StartPhoneSignIn(context.Background(),
		user.PhoneStartRequest{Phone: "081234567890"})
	require.NoError(t, err)
	_, err = h.service.VerifyPhoneSignIn(context.Background(),
		user.PhoneVerifyRequest{Phone: "081234567890", Code: h.otp.lastCode})

	require.NoError(t, err)
	assert.Len(t, h.repo.accounts, 1)

	linked, err := h.identities.FindBySubject(context.Background(), user.ProviderPhone, "+6281234567890")
	require.NoError(t, err)
	assert.Equal(t, h.account.ID, linked.UserID)
}
