package push

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// defaultTokenURI is where a service account exchanges a signed assertion for
// an access token. Real credential files carry it; it is defaulted so a
// hand-trimmed one still works.
const defaultTokenURI = "https://oauth2.googleapis.com/token"

// serviceAccount is the subset of a Firebase service account JSON this adapter
// needs. The file carries more (client_id, certificate URLs); nothing here
// depends on those, so they are not described.
type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// Credentials is a parsed, validated service account, ready to sign
// assertions. The private key is parsed once at startup rather than per
// request, so a malformed key is a boot failure instead of a 500 during the
// first broadcast.
type Credentials struct {
	ProjectID   string
	ClientEmail string
	TokenURI    string

	privateKey *rsa.PrivateKey
}

// LoadServiceAccount accepts either the JSON itself or a path to it.
//
// Both forms exist because both deployments exist: Railway and friends inject
// the whole document into one environment variable, while a docker-compose
// setup mounts the file. Guessing by the first character is enough -- a path
// never starts with '{', and the document always does.
func LoadServiceAccount(value string) (Credentials, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Credentials{}, ErrNotConfigured
	}

	if strings.HasPrefix(value, "{") {
		return ParseServiceAccount([]byte(value))
	}

	raw, err := os.ReadFile(value)
	if err != nil {
		return Credentials{}, fmt.Errorf("read service account file: %w", err)
	}
	return ParseServiceAccount(raw)
}

// ParseServiceAccount validates the document and parses the signing key.
func ParseServiceAccount(raw []byte) (Credentials, error) {
	var account serviceAccount
	if err := json.Unmarshal(raw, &account); err != nil {
		return Credentials{}, fmt.Errorf("parse service account: %w", err)
	}

	// Every field is required: a credential missing one of them cannot mint a
	// token, and finding that out at startup beats finding it out when the
	// first promo is sent.
	var missing []string
	if account.ProjectID == "" {
		missing = append(missing, "project_id")
	}
	if account.ClientEmail == "" {
		missing = append(missing, "client_email")
	}
	if account.PrivateKey == "" {
		missing = append(missing, "private_key")
	}
	if len(missing) > 0 {
		return Credentials{}, fmt.Errorf("service account is missing %s", strings.Join(missing, ", "))
	}

	// An environment variable cannot hold real newlines on every platform, so
	// the key is commonly stored with them escaped. Undoing that here means an
	// operator who pasted the value verbatim gets a working service rather
	// than "failed to parse PEM".
	pem := strings.ReplaceAll(account.PrivateKey, `\n`, "\n")

	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pem))
	if err != nil {
		return Credentials{}, fmt.Errorf("parse service account private key: %w", err)
	}

	tokenURI := account.TokenURI
	if tokenURI == "" {
		tokenURI = defaultTokenURI
	}

	return Credentials{
		ProjectID:   account.ProjectID,
		ClientEmail: account.ClientEmail,
		TokenURI:    tokenURI,
		privateKey:  key,
	}, nil
}

// valid reports whether these credentials can sign.
func (c Credentials) valid() bool {
	return c.ProjectID != "" && c.ClientEmail != "" && c.privateKey != nil
}
