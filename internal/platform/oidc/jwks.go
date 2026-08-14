// Package oidc verifies the ID tokens issued by Google and Apple.
//
// It talks to the providers' JWKS endpoints directly instead of pulling in an
// OIDC library, so the trust decisions -- which algorithms are accepted, which
// audiences, how long keys are cached -- are visible in this package rather
// than buried in a dependency.
package oidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// keySet caches a provider's public keys and refetches them when a token
// arrives signed by a key it has not seen.
type keySet struct {
	url    string
	client *http.Client
	ttl    time.Duration

	mu sync.RWMutex
	// keys is nil until the first successful fetch.
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	// refresh serialises concurrent misses so a burst of requests for an
	// unknown kid triggers one fetch, not one per request.
	refresh sync.Mutex
}

// minRefreshInterval throttles refetches. Without it, tokens carrying a random
// `kid` would let a client drive unbounded outbound requests to the provider.
const minRefreshInterval = time.Minute

func newKeySet(url string, client *http.Client, ttl time.Duration) *keySet {
	return &keySet{url: url, client: client, ttl: ttl}
}

// key returns the public key for kid, fetching the key set if needed.
func (k *keySet) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if key, ok := k.cached(kid); ok {
		return key, nil
	}

	k.refresh.Lock()
	defer k.refresh.Unlock()

	// Another goroutine may have refreshed while this one waited.
	if key, ok := k.cached(kid); ok {
		return key, nil
	}

	k.mu.RLock()
	lastFetch := k.fetchedAt
	k.mu.RUnlock()
	if !lastFetch.IsZero() && time.Since(lastFetch) < minRefreshInterval {
		return nil, fmt.Errorf("no key %q in the cached key set", kid)
	}

	if err := k.fetch(ctx); err != nil {
		return nil, err
	}

	if key, ok := k.cached(kid); ok {
		return key, nil
	}
	return nil, fmt.Errorf("no key %q published by %s", kid, k.url)
}

func (k *keySet) cached(kid string) (*rsa.PublicKey, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	if k.keys == nil || time.Since(k.fetchedAt) > k.ttl {
		return nil, false
	}
	key, ok := k.keys[kid]
	return key, ok
}

func (k *keySet) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return fmt.Errorf("build jwks request: %w", err)
	}

	resp, err := k.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks from %s: %w", k.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks endpoint %s returned %d", k.url, resp.StatusCode)
	}

	var document struct {
		Keys []jwk `json:"keys"`
	}
	// The response is small and provider-controlled, but a bounded read still
	// keeps a misbehaving endpoint from exhausting memory.
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&document); err != nil {
		return fmt.Errorf("decode jwks from %s: %w", k.url, err)
	}

	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, key := range document.Keys {
		// Only RSA signing keys are used by Google and Apple today; skipping
		// anything else is safer than guessing at an unfamiliar key type.
		if key.Kty != "RSA" || (key.Use != "" && key.Use != "sig") {
			continue
		}
		parsed, err := key.rsaPublicKey()
		if err != nil {
			continue
		}
		keys[key.Kid] = parsed
	}

	if len(keys) == 0 {
		return fmt.Errorf("jwks endpoint %s published no usable RSA keys", k.url)
	}

	k.mu.Lock()
	k.keys = keys
	k.fetchedAt = time.Now()
	k.mu.Unlock()

	return nil
}

// jwk is one entry of a JSON Web Key Set.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (j jwk) rsaPublicKey() (*rsa.PublicKey, error) {
	modulus, err := base64.RawURLEncoding.DecodeString(j.N)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	exponent, err := base64.RawURLEncoding.DecodeString(j.E)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}
	if len(exponent) == 0 || len(exponent) > 8 {
		return nil, fmt.Errorf("exponent length %d is out of range", len(exponent))
	}

	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(modulus),
		E: int(new(big.Int).SetBytes(exponent).Int64()),
	}, nil
}
