package otp_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/platform/otp"
)

func TestGenerateReturnsTheRequestedNumberOfDigits(t *testing.T) {
	code, err := otp.Generate(6)

	require.NoError(t, err)
	assert.Len(t, code, 6)
	_, err = strconv.Atoi(code)
	assert.NoError(t, err, "code must be numeric")
}

func TestGenerateCanProduceCodesWithLeadingZeros(t *testing.T) {
	// The previous implementation derived a range from 10^(n-1), so it could
	// never emit a leading zero -- silently removing a tenth of the keyspace.
	// This asserts the range is at least reachable rather than waiting for a
	// specific draw.
	seen := map[string]bool{}
	for range 500 {
		code, err := otp.Generate(4)
		require.NoError(t, err)
		require.Len(t, code, 4)
		seen[code] = true
	}

	assert.Greater(t, len(seen), 100, "codes must not repeat in a narrow band")
}

func TestGenerateRejectsAnUnreasonableLength(t *testing.T) {
	_, err := otp.Generate(2)

	assert.Error(t, err)
}
