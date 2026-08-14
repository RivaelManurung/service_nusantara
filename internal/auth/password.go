package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// ErrPasswordMismatch is returned when a plaintext password does not match a hash.
var ErrPasswordMismatch = errors.New("password does not match")

// Hasher wraps bcrypt with a configured cost. The previous service used
// bcrypt.DefaultCost (10) hardcoded; making it configuration lets the cost rise
// with hardware without a code change.
type Hasher struct {
	cost int
}

func NewHasher(cost int) *Hasher { return &Hasher{cost: cost} }

// Hash derives a storable hash from a plaintext password.
func (h *Hasher) Hash(password string) (string, error) {
	// bcrypt silently truncates beyond 72 bytes, so a long passphrase would be
	// weaker than the user believes; reject it instead.
	if len(password) > 72 {
		return "", errors.New("password must not exceed 72 bytes")
	}
	digest, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(digest), nil
}

// Compare checks a plaintext password against a stored hash.
func (h *Hasher) Compare(hash, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrPasswordMismatch
	}
	return nil
}
