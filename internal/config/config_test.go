package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/config"
)

// setBaseEnv provides the minimum valid environment; individual tests override
// one variable to prove the corresponding rule fires.
func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/nusantara")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("JWT_SECRET", "a-secret-that-is-at-least-32-characters")
	t.Setenv("APP_ENV", config.EnvDevelopment)
}

func TestLoadAppliesDefaults(t *testing.T) {
	setBaseEnv(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Equal(t, "8080", cfg.HTTP.Port)
	assert.Equal(t, 12, cfg.Auth.BcryptCost)
	assert.False(t, cfg.Postgres.AutoMigrate)
}

func TestLoadRejectsAShortJWTSecret(t *testing.T) {
	// A short HMAC key is brute-forceable, so this must fail at startup rather
	// than at the first forged token.
	setBaseEnv(t)
	t.Setenv("JWT_SECRET", "tooshort")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET")
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	t.Setenv("APP_ENV", config.EnvDevelopment)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("JWT_SECRET", "")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL")
	assert.Contains(t, err.Error(), "REDIS_ADDR")
	assert.Contains(t, err.Error(), "JWT_SECRET")
}

func TestLoadRejectsWildcardCORSInProduction(t *testing.T) {
	setProductionEnv(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", "*")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "CORS_ALLOWED_ORIGINS")
}

func TestLoadRejectsAutoMigrateInProduction(t *testing.T) {
	setProductionEnv(t)
	t.Setenv("DB_AUTO_MIGRATE", "true")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_AUTO_MIGRATE")
}

func TestLoadRejectsAnAccessTTLLongerThanTheRefreshTTL(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("JWT_ACCESS_TTL", "48h")
	t.Setenv("JWT_REFRESH_TTL", "24h")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_ACCESS_TTL")
}

func TestLoadRejectsAMalformedDuration(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("JWT_ACCESS_TTL", "fifteen minutes")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_ACCESS_TTL")
}

func TestProductionAcceptsAnExplicitOriginList(t *testing.T) {
	setProductionEnv(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://admin.nusantara.id, https://app.nusantara.id")

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Equal(t, []string{"https://admin.nusantara.id", "https://app.nusantara.id"}, cfg.HTTP.CORSOrigins)
}

// setProductionEnv is setBaseEnv plus the settings production insists on.
func setProductionEnv(t *testing.T) {
	t.Helper()
	setBaseEnv(t)
	t.Setenv("APP_ENV", config.EnvProduction)
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://admin.nusantara.id")
	// The log sender prints codes in plaintext, so production refuses it.
	t.Setenv("OTP_ENABLED", "false")
}

func TestLoadRejectsTheLogOTPSenderInProduction(t *testing.T) {
	// A one-time code written to the log is a one-time code in every log sink.
	setProductionEnv(t)
	t.Setenv("OTP_ENABLED", "true")
	t.Setenv("OTP_SENDER", "log")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "OTP_SENDER")
}

func TestLoadRejectsAnOutOfRangeOTPLength(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("OTP_LENGTH", "2")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "OTP_LENGTH")
}

func TestSocialProvidersAreDisabledUntilAudiencesAreConfigured(t *testing.T) {
	// An empty audience list must disable the provider, never accept every
	// client id.
	setBaseEnv(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.False(t, cfg.Social.GoogleEnabled())
	assert.False(t, cfg.Social.AppleEnabled())
}

func TestSocialProvidersReadTheirAudienceLists(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("GOOGLE_CLIENT_IDS", "web.apps.googleusercontent.com, ios.apps.googleusercontent.com")
	t.Setenv("APPLE_CLIENT_IDS", "id.nusantara.app")

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.True(t, cfg.Social.GoogleEnabled())
	assert.Len(t, cfg.Social.GoogleAudiences, 2)
	assert.Equal(t, []string{"id.nusantara.app"}, cfg.Social.AppleAudiences)
}
