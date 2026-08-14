// Package user owns everything about accounts and authentication: the DTOs, the
// persistence port, the business rules and the HTTP handlers.
//
// Grouping by feature rather than by layer means a change to "how login works"
// touches one directory. The previous service spread the same feature across
// internal/handlers, internal/domain/usecases, internal/domain/repositories,
// internal/data/repositories and internal/data/services, with the interface for
// a use case living in a package named `services` and its implementation in a
// package named `usecases`.
package user

import "time"

// RegisterRequest creates a back-office account.
type RegisterRequest struct {
	Name     string `json:"name" validate:"required,min=2,max=255"`
	Username string `json:"username" validate:"required,min=3,max=64"`
	Email    string `json:"email" validate:"required,email,max=255"`
	Password string `json:"password" validate:"required,min=8,max=72"`
	RoleID   string `json:"role_id" validate:"required,uuid"`
}

// LoginRequest authenticates by email and password.
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

// RefreshRequest exchanges a refresh token for a new token pair.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// ChangePasswordRequest rotates the caller's own password.
type ChangePasswordRequest struct {
	CurrentPassword      string `json:"current_password" validate:"required"`
	NewPassword          string `json:"new_password" validate:"required,min=8,max=72,nefield=CurrentPassword"`
	ConfirmationPassword string `json:"confirmation_password" validate:"required,eqfield=NewPassword"`
}

// SocialLoginRequest carries an ID token obtained by the mobile app from
// Google or Apple. The backend re-verifies it; the app's own result is never
// trusted.
type SocialLoginRequest struct {
	IDToken string `json:"id_token" validate:"required"`
	// Nonce is the value the app passed to the provider. Sending it lets the
	// backend reject a token captured from a different sign-in attempt.
	Nonce string `json:"nonce,omitempty"`
	// Name is only used on first sign-in with Apple, which reveals the name to
	// the client rather than in the token.
	Name string `json:"name,omitempty" validate:"omitempty,max=255"`
}

// PhoneStartRequest asks for a one-time code.
type PhoneStartRequest struct {
	Phone string `json:"phone" validate:"required,min=6,max=20"`
}

// PhoneVerifyRequest exchanges a one-time code for a session.
type PhoneVerifyRequest struct {
	Phone string `json:"phone" validate:"required,min=6,max=20"`
	Code  string `json:"code" validate:"required,min=4,max=10,number"`
	// Name is applied only when this verification creates the account.
	Name string `json:"name,omitempty" validate:"omitempty,max=255"`
}

// PhoneStartResponse tells the client when the code expires and when it may
// ask for another one.
type PhoneStartResponse struct {
	ExpiresInSeconds int `json:"expires_in_seconds"`
	ResendInSeconds  int `json:"resend_in_seconds"`
}

// SignInMethod describes one way an account can authenticate, so the client can
// show "you signed up with Google" and warn before removing the last method.
type SignInMethod struct {
	Provider string `json:"provider"`
	Email    string `json:"email,omitempty"`
}

// Profile is the read model returned to clients.
//
// It exists so a password hash can never be serialised by accident, which is
// the risk of returning the entity or the GORM model directly.
type Profile struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Username      string         `json:"username,omitempty"`
	Email         string         `json:"email,omitempty"`
	EmailVerified bool           `json:"email_verified"`
	Phone         string         `json:"phone,omitempty"`
	PhoneVerified bool           `json:"phone_verified"`
	Photo         string         `json:"photo,omitempty"`
	Role          string         `json:"role"`
	Status        int            `json:"status"`
	SignInMethods []SignInMethod `json:"sign_in_methods,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}
