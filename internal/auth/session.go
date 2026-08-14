package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrSessionNotFound means the refresh token is unknown, already used or expired.
var ErrSessionNotFound = errors.New("session not found")

// SessionStore tracks live sessions and revoked access tokens in Redis.
//
// The previous service kept one key per user ("superadmin_token:<id>"), which
// meant logging in on a phone silently invalidated the tablet. Keys here are
// scoped per session, so devices are independent, and a per-user index makes it
// possible to revoke all of them at once.
type SessionStore struct {
	rdb *redis.Client
}

func NewSessionStore(rdb *redis.Client) *SessionStore {
	return &SessionStore{rdb: rdb}
}

// tokenIDField is the hash field holding the access-token jti of a session.
const tokenIDField = "token_id"

func sessionKey(refreshToken string) string {
	// Storing a hash means a Redis dump cannot be replayed as valid refresh
	// tokens.
	sum := sha256.Sum256([]byte(refreshToken))
	return "session:refresh:" + hex.EncodeToString(sum[:])
}

func revokedKey(tokenID string) string { return "session:revoked:" + tokenID }

func userSessionsKey(userID string) string { return "session:user:" + userID }

// Session is the server-side record backing a refresh token.
type Session struct {
	UserID  string `json:"user_id"`
	Role    string `json:"role"`
	TokenID string `json:"token_id"`
}

// Create records a new session for the lifetime of its refresh token.
func (s *SessionStore) Create(ctx context.Context, refreshToken string, sess Session, ttl time.Duration) error {
	key := sessionKey(refreshToken)

	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, map[string]any{
		"user_id":    sess.UserID,
		"role":       sess.Role,
		tokenIDField: sess.TokenID,
	})
	pipe.Expire(ctx, key, ttl)
	// The index stores session keys rather than jtis, because RevokeAllForUser
	// needs to delete the refresh records themselves, not only blacklist the
	// access tokens they mint.
	pipe.SAdd(ctx, userSessionsKey(sess.UserID), key)
	pipe.Expire(ctx, userSessionsKey(sess.UserID), ttl)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Get resolves a refresh token to its session record.
func (s *SessionStore) Get(ctx context.Context, refreshToken string) (Session, error) {
	values, err := s.rdb.HGetAll(ctx, sessionKey(refreshToken)).Result()
	if err != nil {
		return Session{}, fmt.Errorf("read session: %w", err)
	}
	if len(values) == 0 {
		return Session{}, ErrSessionNotFound
	}

	return Session{
		UserID:  values["user_id"],
		Role:    values["role"],
		TokenID: values[tokenIDField],
	}, nil
}

// Delete removes a refresh token and drops it from the user's index.
func (s *SessionStore) Delete(ctx context.Context, refreshToken string, sess Session) error {
	key := sessionKey(refreshToken)

	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, key)
	pipe.SRem(ctx, userSessionsKey(sess.UserID), key)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// Revoke blacklists an access token by its jti until it would have expired
// anyway, which bounds how large the blacklist can grow.
func (s *SessionStore) Revoke(ctx context.Context, tokenID string, ttl time.Duration) error {
	if ttl <= 0 {
		// Already expired: the signature check alone will reject it.
		return nil
	}
	if err := s.rdb.Set(ctx, revokedKey(tokenID), "1", ttl).Err(); err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return nil
}

// RevokeAllForUser ends every session the account holds: each refresh record is
// deleted and each access token it issued is blacklisted.
//
// Without this, changing a password only invalidated the one access token that
// made the request. A refresh token stolen from another device kept working for
// its full lifetime, so the password change did not actually lock the attacker
// out.
func (s *SessionStore) RevokeAllForUser(ctx context.Context, userID string, accessTTL time.Duration) error {
	indexKey := userSessionsKey(userID)

	keys, err := s.rdb.SMembers(ctx, indexKey).Result()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	if len(keys) == 0 {
		return nil
	}

	// Read every session's jti first; the blacklist is keyed by jti, which only
	// the session record knows.
	reads := s.rdb.Pipeline()
	tokenCmds := make([]*redis.StringCmd, len(keys))
	for i, key := range keys {
		tokenCmds[i] = reads.HGet(ctx, key, tokenIDField)
	}
	// redis.Nil here just means one session expired between SMEMBERS and now.
	if _, err := reads.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("read sessions: %w", err)
	}

	writes := s.rdb.TxPipeline()
	for i, key := range keys {
		if tokenID, err := tokenCmds[i].Result(); err == nil && tokenID != "" && accessTTL > 0 {
			writes.Set(ctx, revokedKey(tokenID), "1", accessTTL)
		}
		writes.Del(ctx, key)
	}
	writes.Del(ctx, indexKey)

	if _, err := writes.Exec(ctx); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return nil
}

// IsRevoked reports whether an access token has been logged out.
//
// A Redis outage returns an error rather than false: the middleware must fail
// closed, because treating "unknown" as "still valid" is exactly the bug that
// let revoked tokens through in the previous service.
func (s *SessionStore) IsRevoked(ctx context.Context, tokenID string) (bool, error) {
	err := s.rdb.Get(ctx, revokedKey(tokenID)).Err()
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, redis.Nil):
		return false, nil
	default:
		return false, fmt.Errorf("check revocation: %w", err)
	}
}
