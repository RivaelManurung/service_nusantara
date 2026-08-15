package server

import (
	"context"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"service_nusantara/internal/config"
	"service_nusantara/internal/model"
	"service_nusantara/internal/modules/devicetoken"
	"service_nusantara/internal/modules/notification"
	"service_nusantara/internal/modules/user"
	"service_nusantara/internal/platform/oidc"
	"service_nusantara/internal/platform/otp"
	"service_nusantara/internal/platform/push"
	"service_nusantara/internal/platform/storage"
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

// pushSender builds the FCM adapter, falling back to one that refuses to send
// when no service account is configured.
//
// It returns the reason alongside, so startup can say why promos will not
// reach anyone's tray instead of leaving every broadcast quietly inbox-only.
func pushSender(cfg config.Push) (push.Sender, error) {
	if !cfg.Configured() {
		return push.Disabled{}, push.ErrNotConfigured
	}

	credentials, err := push.LoadServiceAccount(cfg.Credentials)
	if err != nil {
		return push.Disabled{}, err
	}

	sender, err := push.NewFCM(push.Config{
		Credentials:      credentials,
		Timeout:          cfg.Timeout,
		Concurrency:      cfg.Concurrency,
		AndroidChannelID: cfg.AndroidChannelID,
	})
	if err != nil {
		return push.Disabled{}, err
	}
	return sender, nil
}

// deviceRegistry lets the notification module read the device_tokens table
// without importing the module that owns it.
//
// No module in this tree imports another, so the port declared next to its
// consumer is satisfied here, in the wiring layer, exactly like every other
// dependency.
type deviceRegistry struct {
	repo *devicetoken.GormRepository
}

func (d deviceRegistry) TokensFor(ctx context.Context, userIDs []uuid.UUID) ([]notification.Device, error) {
	rows, err := d.repo.TokensFor(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	devices := make([]notification.Device, 0, len(rows))
	for _, row := range rows {
		devices = append(devices, notification.Device{UserID: row.UserID, Token: row.Token})
	}
	return devices, nil
}

func (d deviceRegistry) DeleteTokens(ctx context.Context, tokens []string) error {
	return d.repo.DeleteTokens(ctx, tokens)
}

// imageUploader builds the storage backend, falling back to one that refuses
// uploads when no provider is configured.
//
// It returns the reason alongside, so startup can say why uploads are off
// instead of leaving every image endpoint failing with an opaque 503.
func imageUploader(cfg config.Storage) (storage.Uploader, error) {
	if !cfg.Configured() {
		return storage.Disabled{}, storage.ErrNotConfigured
	}

	uploader, err := storage.NewCloudinary(storage.CloudinaryConfig{
		CloudName:  cfg.CloudinaryCloudName,
		APIKey:     cfg.CloudinaryAPIKey,
		APISecret:  cfg.CloudinaryAPISecret,
		RootFolder: cfg.RootFolder,
		Timeout:    cfg.Timeout,
	})
	if err != nil {
		return storage.Disabled{}, err
	}
	return uploader, nil
}
