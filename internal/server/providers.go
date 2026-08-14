package server

import (
	"github.com/redis/go-redis/v9"

	"service_nusantara/internal/config"
	"service_nusantara/internal/model"
	"service_nusantara/internal/modules/user"
	"service_nusantara/internal/platform/oidc"
	"service_nusantara/internal/platform/otp"
)

// socialVerifiers builds one ID token verifier per configured provider.
//
// A provider with no audiences is left out of the map entirely, so its route
// answers "not enabled" rather than accepting tokens minted for any client.
func socialVerifiers(cfg config.Social) map[string]user.IDTokenVerifier {
	verifiers := map[string]user.IDTokenVerifier{}

	shared := oidc.Config{HTTPTimeout: cfg.HTTPTimeout, CacheTTL: cfg.JWKSCacheTTL}

	if cfg.GoogleEnabled() {
		google := shared
		google.Audiences = cfg.GoogleAudiences
		verifiers[model.ProviderGoogle] = oidc.NewGoogle(google)
	}
	if cfg.AppleEnabled() {
		apple := shared
		apple.Audiences = cfg.AppleAudiences
		verifiers[model.ProviderApple] = oidc.NewApple(apple)
	}

	return verifiers
}

// phoneSignIn builds the OTP store and delivery adapter, or reports why phone
// sign-in cannot be enabled.
func phoneSignIn(cfg config.Config, rdb *redis.Client) (*otp.Store, otp.Sender, error) {
	if !cfg.OTP.Enabled {
		return nil, nil, nil
	}

	sender, err := otp.NewLogSender(cfg.App.IsProduction())
	if err != nil {
		return nil, nil, err
	}

	store := otp.NewStore(rdb, otp.Config{
		Length:         cfg.OTP.Length,
		TTL:            cfg.OTP.TTL,
		MaxAttempts:    cfg.OTP.MaxAttempts,
		ResendCooldown: cfg.OTP.ResendCooldown,
	})

	return store, sender, nil
}
