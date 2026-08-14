package middleware

import (
	"context"
	"net/http"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// PermissionResolver turns a role name into the set of permission codes it
// holds. The interface lives here, next to its consumer, so this package does
// not import a module; internal/modules/role.GormPermissionRepository satisfies
// it.
type PermissionResolver interface {
	PermissionsFor(ctx context.Context, roleName string) (map[string]struct{}, error)
}

// permissionsKey is the context key for the per-request cache.
type permissionsKey struct{}

// RequirePermission authorises the caller against permission codes rather than
// against a hardcoded role list.
//
// It must be mounted after Authenticate: a missing identity is a 401, never an
// implicit allow, exactly as in RequireRole.
//
// Every listed code is required, not any of them. "All" is the safe reading of
// an ambiguous guard: a route asking for two codes wants both, and a caller who
// holds only one is refused rather than let through.
//
// The resolved set is cached on the request context, so two of these in one
// chain -- or a handler that inspects the set afterwards -- cost one query, not
// one per middleware. It is deliberately NOT cached beyond the request: an
// operator who revokes a permission expects the next request to see it, and a
// process-lifetime cache would keep the old answer until a redeploy.
func RequirePermission(resolver PermissionResolver, codes ...string) Middleware {
	return func(next http.Handler) http.Handler {
		return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
			identity, ok := auth.IdentityFrom(r.Context())
			if !ok {
				return httpx.Unauthorized("authentication required")
			}

			ctx, granted, err := permissionsFor(r.Context(), resolver, identity.Role)
			if err != nil {
				// Fail closed. An unreachable permission store must not be read
				// as "this caller is fine"; that is the failure mode that turns
				// a database blip into an authorisation bypass.
				return httpx.Unavailable("unable to verify permissions").WithCause(err)
			}

			for _, code := range codes {
				if _, has := granted[code]; !has {
					return httpx.Forbidden("your role does not have access to this resource")
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
			return nil
		})
	}
}

// PermissionsFrom exposes the cached set to a handler that needs to branch on
// it -- for instance to hide fields rather than refuse the whole request.
func PermissionsFrom(ctx context.Context) (map[string]struct{}, bool) {
	set, ok := ctx.Value(permissionsKey{}).(map[string]struct{})
	return set, ok
}

// permissionsFor returns the caller's set, resolving it at most once per
// request and returning the context that carries the cache.
func permissionsFor(
	ctx context.Context,
	resolver PermissionResolver,
	roleName string,
) (context.Context, map[string]struct{}, error) {
	if cached, ok := PermissionsFrom(ctx); ok {
		return ctx, cached, nil
	}

	granted, err := resolver.PermissionsFor(ctx, roleName)
	if err != nil {
		return ctx, nil, err
	}
	if granted == nil {
		// A role with no grants is a legitimate answer; store the empty set so
		// a second guard does not re-query looking for a cache that never
		// materialised.
		granted = map[string]struct{}{}
	}

	return context.WithValue(ctx, permissionsKey{}, granted), granted, nil
}
