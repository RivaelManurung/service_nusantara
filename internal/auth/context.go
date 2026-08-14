package auth

import "context"

// identityKey is an unexported struct type, so no other package can overwrite
// the authenticated identity by writing to a plain string key.
type identityKey struct{}

// Identity is the authenticated caller, as established by the auth middleware.
type Identity struct {
	UserID  string
	Role    string
	TokenID string
}

// WithIdentity attaches the caller identity to the request context.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the caller identity, if the request went through the
// auth middleware. Handlers check the boolean instead of type-asserting a JWT
// out of the context and risking a panic.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}
