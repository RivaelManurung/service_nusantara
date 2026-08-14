package auth_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/auth"
)

// bcrypt cost 10 keeps the suite fast; production uses the configured cost.
const testCost = 10

func TestCompareAcceptsTheOriginalPassword(t *testing.T) {
	hasher := auth.NewHasher(testCost)

	hash, err := hasher.Hash("correct horse battery")
	require.NoError(t, err)

	assert.NoError(t, hasher.Compare(hash, "correct horse battery"))
}

func TestCompareRejectsAWrongPassword(t *testing.T) {
	hasher := auth.NewHasher(testCost)

	hash, err := hasher.Hash("correct horse battery")
	require.NoError(t, err)

	assert.ErrorIs(t, hasher.Compare(hash, "wrong password"), auth.ErrPasswordMismatch)
}

func TestHashProducesADifferentDigestEachTime(t *testing.T) {
	// A per-hash salt means identical passwords are not identifiable in a dump.
	hasher := auth.NewHasher(testCost)

	first, err := hasher.Hash("same password")
	require.NoError(t, err)
	second, err := hasher.Hash("same password")
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
}

func TestHashRejectsPasswordsBeyondTheBcryptLimit(t *testing.T) {
	// bcrypt ignores bytes past 72, so accepting a longer one would silently
	// weaken the credential.
	_, err := auth.NewHasher(testCost).Hash(strings.Repeat("a", 73))

	assert.Error(t, err)
}
