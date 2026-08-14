package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
	"service_nusantara/internal/model"
	"service_nusantara/internal/platform/oidc"
)

// IDTokenVerifier checks a provider's ID token. One instance per provider.
//
// It is exported because internal/server builds the provider map; every other
// port in this package stays unexported.
type IDTokenVerifier interface {
	Verify(ctx context.Context, rawToken, expectedNonce string) (oidc.Claims, error)
	Name() string
}

// SignInWithProvider verifies a Google or Apple ID token and returns a session,
// creating or linking the account as needed.
func (s *Service) SignInWithProvider(ctx context.Context, provider string, req SocialLoginRequest) (auth.TokenPair, error) {
	verifier, ok := s.social[provider]
	if !ok {
		return auth.TokenPair{}, httpx.NotFound(fmt.Sprintf("%s sign-in is not enabled", provider))
	}

	claims, err := verifier.Verify(ctx, req.IDToken, req.Nonce)
	if err != nil {
		if errors.Is(err, oidc.ErrInvalidToken) {
			return auth.TokenPair{}, httpx.Unauthorized("sign-in token is not valid").WithCause(err)
		}
		// A JWKS fetch failure is our problem, not the client's, and retrying
		// may well succeed.
		return auth.TokenPair{}, httpx.Unavailable("unable to verify sign-in right now").WithCause(err)
	}

	account, err := s.resolveSocialAccount(ctx, SocialProfile{
		Provider:      provider,
		Subject:       claims.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          firstNonEmpty(claims.Name, req.Name),
		Picture:       claims.Picture,
	})
	if err != nil {
		return auth.TokenPair{}, err
	}

	if account.Status != StatusActive {
		return auth.TokenPair{}, httpx.Forbidden("this account is not active")
	}

	return s.issueSession(ctx, account.ID.String(), account.RoleName)
}

// resolveSocialAccount finds, links or creates the account behind a verified
// provider profile.
func (s *Service) resolveSocialAccount(ctx context.Context, profile SocialProfile) (Account, error) {
	// 1. Known identity: this person has signed in with this provider before.
	identity, err := s.identities.FindBySubject(ctx, profile.Provider, profile.Subject)
	switch {
	case err == nil:
		account, err := s.repo.FindByID(ctx, identity.UserID)
		if err != nil {
			return Account{}, httpx.Internal("failed to load account").WithCause(err)
		}
		// Best effort: a failed timestamp update must not fail the sign-in.
		if err := s.identities.TouchLogin(ctx, identity.ID, time.Now().UTC()); err != nil {
			s.log.Warn("could not record identity login", "error", err)
		}
		return account, nil
	case !errors.Is(err, ErrNotFound):
		return Account{}, httpx.Internal("failed to look up identity").WithCause(err)
	}

	// 2. Unknown identity, but the provider vouches for an email we already
	//    hold. Linking is only safe when the provider says the address is
	//    verified; otherwise anyone could claim someone else's account by
	//    signing up elsewhere with their address.
	if profile.Email != "" {
		existing, err := s.repo.FindByEmail(ctx, profile.Email)
		switch {
		case err == nil && !profile.EmailVerified:
			return Account{}, httpx.Conflict(
				"this email is already registered; sign in with your existing method first, then link this one")
		case err == nil:
			return s.linkAndReturn(ctx, existing, profile)
		case !errors.Is(err, ErrNotFound):
			return Account{}, httpx.Internal("failed to look up account").WithCause(err)
		}
	}

	// 3. Nobody here yet: create the account.
	return s.createSocialAccount(ctx, profile)
}

func (s *Service) linkAndReturn(ctx context.Context, account Account, profile SocialProfile) (Account, error) {
	if _, err := s.identities.Link(ctx, Identity{
		UserID:   account.ID,
		Provider: profile.Provider,
		Subject:  profile.Subject,
		Email:    profile.Email,
	}); err != nil {
		return Account{}, httpx.Internal("failed to link sign-in method").WithCause(err)
	}

	// The provider has now vouched for the address, so an account created by
	// password with an unverified email becomes verified.
	if !account.EmailVerified && profile.EmailVerified {
		if err := s.repo.MarkEmailVerified(ctx, account.ID); err != nil {
			s.log.Warn("could not mark email verified", "error", err)
		}
		account.EmailVerified = true
	}

	return account, nil
}

func (s *Service) createSocialAccount(ctx context.Context, profile SocialProfile) (Account, error) {
	roleID, err := s.defaultRoleID(ctx)
	if err != nil {
		return Account{}, err
	}

	created, err := s.repo.Create(ctx, Account{
		ID: uuid.New(),
		// Apple only reveals a name on the very first authorization, and only
		// to the client, so a placeholder is better than an empty screen.
		Name:          firstNonEmpty(profile.Name, "Pengguna Nusantara"),
		Email:         profile.Email,
		EmailVerified: profile.EmailVerified,
		Photo:         profile.Picture,
		RoleID:        roleID,
		Status:        StatusActive,
	})
	if err != nil {
		return Account{}, httpx.Internal("failed to create account").WithCause(err)
	}

	if _, err := s.identities.Link(ctx, Identity{
		UserID:   created.ID,
		Provider: profile.Provider,
		Subject:  profile.Subject,
		Email:    profile.Email,
	}); err != nil {
		return Account{}, httpx.Internal("failed to link sign-in method").WithCause(err)
	}

	return created, nil
}

// defaultRoleID resolves the role granted to self-service sign-ups.
func (s *Service) defaultRoleID(ctx context.Context) (uuid.UUID, error) {
	roleID, err := s.repo.FindRoleIDByName(ctx, s.defaultRoleName)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// A misconfigured deployment, not a client mistake: the role must
			// be seeded before self-service sign-up can work.
			return uuid.Nil, httpx.Internal(
				fmt.Sprintf("default sign-up role %q does not exist", s.defaultRoleName)).WithCause(err)
		}
		return uuid.Nil, httpx.Internal("failed to resolve default role").WithCause(err)
	}
	return roleID, nil
}

// SignInMethods lists how an account can authenticate.
func (s *Service) SignInMethods(ctx context.Context, userID string) ([]SignInMethod, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return nil, httpx.Unauthorized("token subject is not a valid user id").WithCause(err)
	}

	identities, err := s.identities.ListForUser(ctx, id)
	if err != nil {
		return nil, httpx.Internal("failed to list sign-in methods").WithCause(err)
	}

	methods := make([]SignInMethod, 0, len(identities))
	for _, identity := range identities {
		methods = append(methods, SignInMethod{Provider: identity.Provider, Email: identity.Email})
	}
	return methods, nil
}

// Provider names re-exported so handlers and routes do not import the model
// package just to name a route.
const (
	ProviderGoogle   = model.ProviderGoogle
	ProviderApple    = model.ProviderApple
	ProviderPassword = model.ProviderPassword
	ProviderPhone    = model.ProviderPhone
)

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
