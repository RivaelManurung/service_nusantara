package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
	"service_nusantara/internal/platform/otp"
)

// otpStore issues and verifies one-time codes.
type otpStore interface {
	Issue(ctx context.Context, phone string) (code string, expiresIn time.Duration, err error)
	Verify(ctx context.Context, phone, code string) error
}

// otpSender delivers a code to a phone number.
type otpSender interface {
	Send(ctx context.Context, phone, code string) error
}

// StartPhoneSignIn sends a one-time code to a phone number.
//
// The response is deliberately identical whether or not the number already has
// an account: the endpoint is unauthenticated, so telling the caller would let
// anyone test which numbers are registered.
func (s *Service) StartPhoneSignIn(ctx context.Context, req PhoneStartRequest) (PhoneStartResponse, error) {
	if s.otp == nil || s.sender == nil {
		return PhoneStartResponse{}, httpx.NotFound("phone sign-in is not enabled")
	}

	phone, err := normalizePhone(req.Phone, s.defaultCountryCode)
	if err != nil {
		return PhoneStartResponse{}, err
	}

	code, expiresIn, err := s.otp.Issue(ctx, phone)
	if err != nil {
		if errors.Is(err, otp.ErrCooldownActive) {
			return PhoneStartResponse{}, httpx.RateLimited("a code was already sent; please wait before asking for another").
				WithDetails(map[string]any{"retry_after_seconds": int(expiresIn.Seconds())})
		}
		return PhoneStartResponse{}, httpx.Internal("failed to issue verification code").WithCause(err)
	}

	if err := s.sender.Send(ctx, phone, code); err != nil {
		return PhoneStartResponse{}, httpx.Unavailable("failed to send verification code").WithCause(err)
	}

	return PhoneStartResponse{
		ExpiresInSeconds: int(expiresIn.Seconds()),
		ResendInSeconds:  int(s.otpResendCooldown.Seconds()),
	}, nil
}

// VerifyPhoneSignIn exchanges a valid code for a session, creating the account
// on first use.
func (s *Service) VerifyPhoneSignIn(ctx context.Context, req PhoneVerifyRequest) (auth.TokenPair, error) {
	if s.otp == nil {
		return auth.TokenPair{}, httpx.NotFound("phone sign-in is not enabled")
	}

	phone, err := normalizePhone(req.Phone, s.defaultCountryCode)
	if err != nil {
		return auth.TokenPair{}, err
	}

	if err := s.otp.Verify(ctx, phone, req.Code); err != nil {
		switch {
		case errors.Is(err, otp.ErrNoActiveCode):
			return auth.TokenPair{}, httpx.Unauthorized("this code has expired, please request a new one")
		case errors.Is(err, otp.ErrTooManyAttempts):
			return auth.TokenPair{}, httpx.RateLimited("too many incorrect attempts, please request a new code")
		case errors.Is(err, otp.ErrCodeMismatch):
			return auth.TokenPair{}, httpx.Unauthorized("the code is incorrect")
		default:
			return auth.TokenPair{}, httpx.Internal("failed to verify code").WithCause(err)
		}
	}

	account, err := s.resolvePhoneAccount(ctx, phone, req.Name)
	if err != nil {
		return auth.TokenPair{}, err
	}

	if account.Status != StatusActive {
		return auth.TokenPair{}, httpx.Forbidden("this account is not active")
	}

	return s.issueSession(ctx, account.ID.String(), account.RoleName)
}

// resolvePhoneAccount returns the account for a freshly verified number,
// creating it on first sign-in.
func (s *Service) resolvePhoneAccount(ctx context.Context, phone, name string) (Account, error) {
	identity, err := s.identities.FindBySubject(ctx, ProviderPhone, phone)
	switch {
	case err == nil:
		account, err := s.repo.FindByID(ctx, identity.UserID)
		if err != nil {
			return Account{}, httpx.Internal("failed to load account").WithCause(err)
		}
		if err := s.identities.TouchLogin(ctx, identity.ID, time.Now().UTC()); err != nil {
			s.log.Warn("could not record identity login", "error", err)
		}
		return account, nil
	case !errors.Is(err, ErrNotFound):
		return Account{}, httpx.Internal("failed to look up identity").WithCause(err)
	}

	// An account may already hold this number from a back-office registration
	// that predates the identity table; adopt it rather than failing on the
	// unique index.
	existing, err := s.repo.FindByPhone(ctx, phone)
	switch {
	case err == nil:
		if _, err := s.identities.Link(ctx, Identity{
			UserID: existing.ID, Provider: ProviderPhone, Subject: phone,
		}); err != nil {
			return Account{}, httpx.Internal("failed to link sign-in method").WithCause(err)
		}
		return existing, nil
	case !errors.Is(err, ErrNotFound):
		return Account{}, httpx.Internal("failed to look up account").WithCause(err)
	}

	roleID, err := s.defaultRoleID(ctx)
	if err != nil {
		return Account{}, err
	}

	created, err := s.repo.Create(ctx, Account{
		ID:            uuid.New(),
		Name:          firstNonEmpty(name, "Pengguna Nusantara"),
		Phone:         phone,
		PhoneVerified: true,
		RoleID:        roleID,
		Status:        StatusActive,
	})
	if err != nil {
		return Account{}, httpx.Internal("failed to create account").WithCause(err)
	}

	if _, err := s.identities.Link(ctx, Identity{
		UserID: created.ID, Provider: ProviderPhone, Subject: phone,
	}); err != nil {
		return Account{}, httpx.Internal("failed to link sign-in method").WithCause(err)
	}

	return created, nil
}
