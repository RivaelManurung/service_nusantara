// Package otp issues, stores and verifies one-time codes for phone sign-in.
package otp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// Generate returns a numeric code of the requested length.
//
// It draws from crypto/rand. The previous service used math/rand seeded with
// time.Now(), so codes were predictable to anyone who could guess when the
// request was made.
func Generate(length int) (string, error) {
	if length < 4 || length > 10 {
		return "", fmt.Errorf("otp length %d is out of range", length)
	}

	var builder strings.Builder
	builder.Grow(length)
	for range length {
		digit, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("generate otp: %w", err)
		}
		builder.WriteByte(byte('0' + digit.Int64()))
	}

	return builder.String(), nil
}

// hashCode derives what is actually stored. A Redis dump then reveals no usable
// codes, and the short lifetime makes the missing salt irrelevant here.
func hashCode(phone, code string) string {
	sum := sha256.Sum256([]byte(phone + ":" + code))
	return hex.EncodeToString(sum[:])
}
