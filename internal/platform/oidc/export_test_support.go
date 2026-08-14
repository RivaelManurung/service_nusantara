package oidc

// NewVerifierForTesting builds a verifier against an arbitrary JWKS endpoint
// and issuer list.
//
// It exists so the verification rules can be tested against a local key set
// instead of Google's live endpoint. It is not used by production code; the
// exported constructors pin the real provider URLs.
func NewVerifierForTesting(name, jwksURL string, issuers []string, cfg Config) *Verifier {
	return newVerifier(name, jwksURL, issuers, cfg)
}
