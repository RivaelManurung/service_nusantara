package user_test

import (
	"time"

	"service_nusantara/internal/config"
)

// testAuthConfig returns auth settings valid for the token manager under test.
func testAuthConfig() config.Auth {
	return config.Auth{
		JWTSecret:       "test-secret-that-is-at-least-32-bytes-long",
		Issuer:          "nusantara-test",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: time.Hour,
	}
}
