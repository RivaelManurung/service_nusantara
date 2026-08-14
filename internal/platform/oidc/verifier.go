package oidc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken is returned for every verification failure. The specific
// reason is wrapped for the logs but deliberately not distinguished for
// callers, so a handler cannot accidentally turn it into a probing oracle.
var ErrInvalidToken = errors.New("id token is invalid")

// Well-known provider endpoints and issuers.
const (
	GoogleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
	AppleJWKSURL  = "https://appleid.apple.com/auth/keys"

	appleIssuer = "https://appleid.apple.com"
)

// googleIssuers are both accepted; Google has issued tokens under each form.
var googleIssuers = []string{"https://accounts.google.com", "accounts.google.com"}

// Claims is the subset of an ID token this service acts on.
type Claims struct {
	// Subject is the provider's stable identifier for the person. It is the
	// only field safe to key an account on: email addresses change hands.
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
	// Nonce echoes what the client sent, binding the token to this sign-in
	// attempt.
	Nonce string
	// IsPrivateEmail is Apple's flag for a relay address.
	IsPrivateEmail bool
}

// Verifier validates ID tokens for one provider.
type Verifier struct {
	name      string
	keys      *keySet
	issuers   []string
	audiences []string
	leeway    time.Duration
}

// Config describes one provider.
type Config struct {
	// Audiences are the client IDs this backend accepts. A token minted for
	// another app must not be usable here, which is the whole point of the
	// check.
	Audiences []string
	// HTTPTimeout bounds the JWKS fetch.
	HTTPTimeout time.Duration
	// CacheTTL is how long a fetched key set is reused.
	CacheTTL time.Duration
}

// NewGoogle builds a verifier for Google Sign-In.
func NewGoogle(cfg Config) *Verifier {
	return newVerifier("google", GoogleJWKSURL, googleIssuers, cfg)
}

// NewApple builds a verifier for Sign in with Apple.
func NewApple(cfg Config) *Verifier {
	return newVerifier("apple", AppleJWKSURL, []string{appleIssuer}, cfg)
}

func newVerifier(name, jwksURL string, issuers []string, cfg Config) *Verifier {
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = time.Hour
	}

	return &Verifier{
		name:      name,
		keys:      newKeySet(jwksURL, &http.Client{Timeout: cfg.HTTPTimeout}, cfg.CacheTTL),
		issuers:   issuers,
		audiences: cfg.Audiences,
		// A minute absorbs ordinary clock drift between a phone and this
		// server without meaningfully extending a token's life.
		leeway: time.Minute,
	}
}

// Name identifies the provider, for logs and identity rows.
func (v *Verifier) Name() string { return v.name }

// Verify checks the signature, issuer, audience and expiry of an ID token and
// returns the claims this service uses.
//
// expectedNonce may be empty. When the client supplies one it must match the
// token, which is what stops a token captured from another sign-in from being
// replayed here.
func (v *Verifier) Verify(ctx context.Context, rawToken, expectedNonce string) (Claims, error) {
	if len(v.audiences) == 0 {
		return Claims{}, fmt.Errorf("%s sign-in is not configured", v.name)
	}

	var raw rawClaims
	parsed, err := jwt.ParseWithClaims(rawToken, &raw,
		func(t *jwt.Token) (any, error) {
			kid, _ := t.Header["kid"].(string)
			if kid == "" {
				return nil, errors.New("token header has no kid")
			}
			return v.keys.key(ctx, kid)
		},
		// Providers sign with RS256. Accepting whatever the token declares is
		// how alg-confusion attacks succeed.
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(v.leeway),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !parsed.Valid {
		return Claims{}, ErrInvalidToken
	}

	issuer, err := raw.GetIssuer()
	if err != nil || !slices.Contains(v.issuers, issuer) {
		return Claims{}, fmt.Errorf("%w: unexpected issuer %q", ErrInvalidToken, issuer)
	}

	// jwt.WithAudience only requires one match; checking here keeps the
	// accepted list explicit and the error message useful in logs.
	if !v.audienceAllowed(raw.Audience) {
		return Claims{}, fmt.Errorf("%w: token audience %v is not accepted", ErrInvalidToken, []string(raw.Audience))
	}

	if raw.Subject == "" {
		return Claims{}, fmt.Errorf("%w: token has no subject", ErrInvalidToken)
	}

	if expectedNonce != "" {
		// Constant time, because a mismatch reveals nothing but a timing
		// difference would.
		if subtle.ConstantTimeCompare([]byte(expectedNonce), []byte(raw.Nonce)) != 1 {
			return Claims{}, fmt.Errorf("%w: nonce does not match", ErrInvalidToken)
		}
	}

	return Claims{
		Subject:        raw.Subject,
		Email:          strings.ToLower(strings.TrimSpace(raw.Email)),
		EmailVerified:  bool(raw.EmailVerified),
		Name:           raw.Name,
		Picture:        raw.Picture,
		Nonce:          raw.Nonce,
		IsPrivateEmail: bool(raw.IsPrivateEmail),
	}, nil
}

func (v *Verifier) audienceAllowed(audience jwt.ClaimStrings) bool {
	for _, aud := range audience {
		if slices.Contains(v.audiences, aud) {
			return true
		}
	}
	return false
}

// rawClaims mirrors the wire format of a Google or Apple ID token.
type rawClaims struct {
	Email          string   `json:"email"`
	EmailVerified  flexBool `json:"email_verified"`
	Name           string   `json:"name"`
	Picture        string   `json:"picture"`
	Nonce          string   `json:"nonce"`
	IsPrivateEmail flexBool `json:"is_private_email"`
	jwt.RegisteredClaims
}

// flexBool decodes a claim that providers encode inconsistently: Google sends
// email_verified as a JSON boolean, Apple sends it as the string "true".
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var asBool bool
	if err := json.Unmarshal(data, &asBool); err == nil {
		*b = flexBool(asBool)
		return nil
	}

	var asString string
	if err := json.Unmarshal(data, &asString); err != nil {
		return fmt.Errorf("value is neither a boolean nor a string: %s", data)
	}
	*b = flexBool(strings.EqualFold(asString, "true"))
	return nil
}
