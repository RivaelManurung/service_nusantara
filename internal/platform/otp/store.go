package otp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Outcomes a caller must distinguish.
var (
	// ErrNoActiveCode means nothing was requested, or it expired.
	ErrNoActiveCode = errors.New("no active code for this number")
	// ErrCodeMismatch means the code was wrong and an attempt was consumed.
	ErrCodeMismatch = errors.New("code does not match")
	// ErrTooManyAttempts means the code was burned by repeated wrong guesses.
	ErrTooManyAttempts = errors.New("too many incorrect attempts")
	// ErrCooldownActive means a code was sent recently and is still valid.
	ErrCooldownActive = errors.New("a code was already sent recently")
)

// Config tunes the one-time code lifecycle.
type Config struct {
	Length      int
	TTL         time.Duration
	MaxAttempts int
	// ResendCooldown is how long a caller must wait before asking for another
	// code, which bounds both SMS spend and SMS-bombing a third party.
	ResendCooldown time.Duration
}

// Store keeps pending codes in Redis.
type Store struct {
	rdb *redis.Client
	cfg Config
}

func NewStore(rdb *redis.Client, cfg Config) *Store {
	return &Store{rdb: rdb, cfg: cfg}
}

func codeKey(phone string) string     { return "otp:code:" + phone }
func cooldownKey(phone string) string { return "otp:cooldown:" + phone }

const (
	fieldHash     = "hash"
	fieldAttempts = "attempts"
)

// Issue creates a code for phone, or reports how long the caller must wait.
// The plaintext is returned once, for delivery; only its hash is stored.
func (s *Store) Issue(ctx context.Context, phone string) (code string, expiresIn time.Duration, err error) {
	cooldown, err := s.rdb.TTL(ctx, cooldownKey(phone)).Result()
	if err != nil {
		return "", 0, fmt.Errorf("check otp cooldown: %w", err)
	}
	if cooldown > 0 {
		return "", cooldown, ErrCooldownActive
	}

	code, err = Generate(s.cfg.Length)
	if err != nil {
		return "", 0, err
	}

	pipe := s.rdb.TxPipeline()
	// A fresh request replaces any previous code rather than adding a second
	// valid one, so the number of guessable codes stays at one.
	pipe.Del(ctx, codeKey(phone))
	pipe.HSet(ctx, codeKey(phone), map[string]any{
		fieldHash:     hashCode(phone, code),
		fieldAttempts: 0,
	})
	pipe.Expire(ctx, codeKey(phone), s.cfg.TTL)
	pipe.Set(ctx, cooldownKey(phone), "1", s.cfg.ResendCooldown)

	if _, err := pipe.Exec(ctx); err != nil {
		return "", 0, fmt.Errorf("store otp: %w", err)
	}

	return code, s.cfg.TTL, nil
}

// Verify consumes an attempt and reports whether the code was correct. A
// correct code is deleted immediately, so it cannot be replayed.
func (s *Store) Verify(ctx context.Context, phone, code string) error {
	key := codeKey(phone)

	values, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("read otp: %w", err)
	}
	if len(values) == 0 {
		return ErrNoActiveCode
	}

	// Count the attempt before comparing, so a crash between the two cannot
	// hand an attacker a free guess.
	attempts, err := s.rdb.HIncrBy(ctx, key, fieldAttempts, 1).Result()
	if err != nil {
		return fmt.Errorf("count otp attempt: %w", err)
	}
	if attempts > int64(s.cfg.MaxAttempts) {
		// Burn the code: without this, an attacker gets unlimited guesses for
		// as long as the code lives.
		s.rdb.Del(ctx, key)
		return ErrTooManyAttempts
	}

	if subtle.ConstantTimeCompare([]byte(values[fieldHash]), []byte(hashCode(phone, code))) != 1 {
		return ErrCodeMismatch
	}

	if err := s.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("consume otp: %w", err)
	}
	return nil
}

// Clear drops any pending code, used after a number is verified by other means.
func (s *Store) Clear(ctx context.Context, phone string) error {
	if err := s.rdb.Del(ctx, codeKey(phone), cooldownKey(phone)).Err(); err != nil {
		return fmt.Errorf("clear otp: %w", err)
	}
	return nil
}
