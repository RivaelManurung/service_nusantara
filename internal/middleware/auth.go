package middleware

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// TokenVerifier is the slice of auth.Manager this package depends on.
type TokenVerifier interface {
	Verify(token string) (*auth.Claims, error)
}

// RevocationChecker reports whether an access token has been logged out.
type RevocationChecker interface {
	IsRevoked(ctx context.Context, tokenID string) (bool, error)
}

// Authenticate validates the bearer token and stores the caller identity.
//
// This replaces the previous echo-jwt configuration whose Skipper returned true
// for blacklisted tokens. Returning true from a Skipper means "do not run this
// middleware", so a revoked token did not merely stay valid, it bypassed
// authentication entirely and reached the handler unauthenticated.
func Authenticate(verifier TokenVerifier, revocations RevocationChecker) Middleware {
	return func(next http.Handler) http.Handler {
		return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
			token, err := bearerToken(r)
			if err != nil {
				return err
			}

			claims, err := verifier.Verify(token)
			if err != nil {
				if errors.Is(err, auth.ErrTokenExpired) {
					return httpx.Unauthorized("access token has expired").WithCause(err)
				}
				return httpx.Unauthorized("access token is invalid").WithCause(err)
			}

			revoked, err := revocations.IsRevoked(r.Context(), claims.TokenID())
			if err != nil {
				// Fail closed: an unreachable revocation store must not be
				// treated as "this token is fine".
				return httpx.Unavailable("unable to verify session").WithCause(err)
			}
			if revoked {
				return httpx.Unauthorized("session has been logged out")
			}

			ctx := auth.WithIdentity(r.Context(), auth.Identity{
				UserID:  claims.UserID,
				Role:    claims.Role,
				TokenID: claims.TokenID(),
			})

			next.ServeHTTP(w, r.WithContext(ctx))
			return nil
		})
	}
}

// RequireRole authorises the caller against a set of role names. It must be
// mounted after Authenticate; a missing identity is treated as a 401, never as
// an implicit allow.
func RequireRole(roles ...string) Middleware {
	return func(next http.Handler) http.Handler {
		return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
			identity, ok := auth.IdentityFrom(r.Context())
			if !ok {
				return httpx.Unauthorized("authentication required")
			}
			if !slices.Contains(roles, identity.Role) {
				return httpx.Forbidden("your role does not have access to this resource")
			}

			next.ServeHTTP(w, r)
			return nil
		})
	}
}

func bearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", httpx.Unauthorized("authorization header is required")
	}

	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", httpx.Unauthorized("authorization header must use the Bearer scheme")
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return "", httpx.Unauthorized("bearer token is empty")
	}

	return token, nil
}
