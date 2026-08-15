package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// FCM's own endpoints and limits.
const (
	defaultBaseURL = "https://fcm.googleapis.com"
	// messagingScope is the only scope this adapter needs; asking for more
	// would hand the service account rights it never exercises.
	messagingScope = "https://www.googleapis.com/auth/firebase.messaging"
	// assertionTTL is the lifetime of the signed assertion. Google rejects
	// anything longer than an hour.
	assertionTTL = time.Hour
	// refreshMargin retires an access token before it actually expires, so a
	// send that starts just under the wire does not fail mid-batch.
	refreshMargin = time.Minute
	// maxBodyBytes bounds what is read from a response. Without it a
	// misbehaving proxy answering with a megabyte of HTML would be buffered in
	// full, once per device in the batch.
	maxBodyBytes = 64 << 10
)

// defaultConcurrency is how many devices are contacted at once.
//
// FCM's batch endpoint was retired in 2024: HTTP v1 accepts exactly one token
// per request, so a broadcast is N requests and the only lever left is how
// many are in flight. Eight keeps a thousand-device promo well under a minute
// without opening so many sockets that the rest of the API starves.
const defaultConcurrency = 8

// Config describes one FCM adapter.
type Config struct {
	Credentials Credentials
	// Timeout bounds a single request, not the whole batch; the batch is
	// bounded by the caller's context.
	Timeout time.Duration
	// Concurrency is how many sends run at once. Zero means defaultConcurrency.
	Concurrency int
	// AndroidChannelID must match the channel the Flutter app creates, or
	// Android 8+ drops the notification while still reporting success.
	AndroidChannelID string
	// BaseURL is overridden by tests. Empty means Google's own host.
	BaseURL string
	// HTTPClient is overridden by tests. Nil means one built from Timeout.
	HTTPClient *http.Client
}

// FCM sends through the Firebase Cloud Messaging HTTP v1 API.
//
// It is safe for concurrent use: the access token is guarded by a mutex, and
// nothing else is mutated after construction.
type FCM struct {
	creds       Credentials
	http        *http.Client
	baseURL     string
	concurrency int
	channelID   string

	// mu guards the cached access token. One mutex rather than a singleflight
	// group: refreshing is a single round trip, and the alternative -- every
	// worker in a broadcast minting its own token -- is exactly what this is
	// here to prevent.
	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

// NewFCM builds the adapter, or reports why it cannot be built.
func NewFCM(cfg Config) (*FCM, error) {
	if !cfg.Credentials.valid() {
		return nil, ErrNotConfigured
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return &FCM{
		creds:       cfg.Credentials,
		http:        client,
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		concurrency: concurrency,
		channelID:   cfg.AndroidChannelID,
	}, nil
}

// Enabled reports that this adapter can deliver.
func (f *FCM) Enabled() bool { return true }

// Send delivers every message, up to Concurrency at a time.
//
// One device failing never fails the batch: the counts and the dead
// registrations come back in the Report. An error is returned only when the
// batch could not be attempted at all -- credentials Google refuses, or a
// cancelled context -- because that is the difference between "retry later"
// and "fix the configuration".
func (f *FCM) Send(ctx context.Context, messages []Message) (Report, error) {
	if len(messages) == 0 {
		return Report{}, nil
	}

	// Minting the token once, before the workers start, keeps a broadcast to a
	// single OAuth round trip and surfaces a credential problem before any
	// device is contacted.
	if _, err := f.token(ctx); err != nil {
		return Report{}, err
	}

	var (
		mu     sync.Mutex
		report Report
		fatal  error
		sem    = make(chan struct{}, f.concurrency)
		wg     sync.WaitGroup
	)

	for _, message := range messages {
		select {
		case <-ctx.Done():
			// The caller gave up. Stop queueing rather than pushing the
			// remaining thousand requests at a context that will reject them.
			wg.Wait()
			return report, ctx.Err()
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(message Message) {
			defer wg.Done()
			defer func() { <-sem }()

			err := f.sendOne(ctx, message)

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				report.Success++
			case errors.Is(err, errTokenGone):
				report.Failure++
				report.InvalidTokens = append(report.InvalidTokens, message.Token)
			default:
				report.Failure++
				// Keep the first credential failure so the caller can log why
				// a broadcast delivered nothing, instead of only a count.
				if fatal == nil && isFatal(err) {
					fatal = err
				}
			}
		}(message)
	}

	wg.Wait()

	return report, fatal
}

// errTokenGone marks a registration FCM says no longer exists.
var errTokenGone = errors.New("registration token is no longer valid")

// errAuth marks a rejection of the credentials themselves.
var errAuth = errors.New("firebase rejected the credentials")

// isFatal reports whether an error means the whole batch is doomed, rather
// than this one device having a problem.
func isFatal(err error) bool { return errors.Is(err, errAuth) }

// sendOne delivers a single message.
func (f *FCM) sendOne(ctx context.Context, message Message) error {
	token, err := f.token(ctx)
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]any{"message": f.payload(message)})
	if err != nil {
		return fmt.Errorf("encode fcm message: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1/projects/%s/messages:send", f.baseURL, url.PathEscape(f.creds.ProjectID))

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build fcm request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := f.http.Do(request)
	if err != nil {
		return fmt.Errorf("call fcm: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	payload := readLimited(response.Body)

	if response.StatusCode == http.StatusOK {
		return nil
	}
	return classify(response.StatusCode, payload)
}

// payload builds the HTTP v1 message body.
//
// The notification block is what draws the tray entry; the data block is what
// the app reads on tap. Both are sent: a data-only message would not appear
// while the app is closed, and a notification-only one would leave the app
// with nothing to route on.
func (f *FCM) payload(message Message) map[string]any {
	androidNotification := map[string]any{"sound": "default"}
	if f.channelID != "" {
		androidNotification["channel_id"] = f.channelID
	}

	payload := map[string]any{
		"token": message.Token,
		"notification": map[string]any{
			"title": message.Title,
			"body":  message.Body,
		},
		"android": map[string]any{
			// HIGH is what wakes a dozing device. Promos would otherwise be
			// batched by Android and arrive hours late.
			"priority":     "HIGH",
			"notification": androidNotification,
		},
		"apns": map[string]any{
			"headers": map[string]any{"apns-priority": "10"},
			"payload": map[string]any{
				"aps": map[string]any{"sound": "default"},
			},
		},
	}

	if len(message.Data) > 0 {
		payload["data"] = message.Data
	}

	return payload
}

// fcmError is the error envelope the API returns.
type fcmError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type      string `json:"@type"`
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

// classify turns an FCM rejection into one of the three answers that change
// what the caller does: drop the registration, fix the credentials, or retry.
func classify(status int, body []byte) error {
	var parsed fcmError
	_ = json.Unmarshal(body, &parsed)

	detail := ""
	for _, item := range parsed.Error.Details {
		if item.ErrorCode != "" {
			detail = item.ErrorCode
			break
		}
	}

	switch {
	// UNREGISTERED is an uninstalled app; INVALID_ARGUMENT on a send means the
	// token itself is malformed. Both are dead registrations, and retrying
	// either forever is how a device_tokens table becomes mostly garbage.
	case detail == "UNREGISTERED", detail == "INVALID_ARGUMENT",
		status == http.StatusNotFound, status == http.StatusBadRequest:
		return fmt.Errorf("%w: %s", errTokenGone, describe(parsed, body))

	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Errorf("%w: %s", errAuth, describe(parsed, body))

	default:
		return fmt.Errorf("fcm responded %d: %s", status, describe(parsed, body))
	}
}

// describe prefers Google's own wording and falls back to the raw body, which
// is what a proxy in front of FCM tends to return.
func describe(parsed fcmError, body []byte) string {
	if parsed.Error.Message != "" {
		return parsed.Error.Message
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "no response body"
	}
	return text
}

func readLimited(r io.Reader) []byte {
	body, _ := io.ReadAll(io.LimitReader(r, maxBodyBytes))
	return body
}

// token returns a cached access token, minting a new one when the current one
// is close to expiring.
func (f *FCM) token(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.accessToken != "" && time.Now().Before(f.expiresAt.Add(-refreshMargin)) {
		return f.accessToken, nil
	}

	assertion, err := f.assertion()
	if err != nil {
		return "", err
	}

	access, ttl, err := f.exchange(ctx, assertion)
	if err != nil {
		return "", err
	}

	f.accessToken = access
	f.expiresAt = time.Now().Add(ttl)
	return access, nil
}

// assertion signs the JWT that proves this service account's identity.
func (f *FCM) assertion() (string, error) {
	now := time.Now()

	claims := jwt.MapClaims{
		"iss":   f.creds.ClientEmail,
		"scope": messagingScope,
		"aud":   f.creds.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(assertionTTL).Unix(),
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(f.creds.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign service account assertion: %w", err)
	}
	return signed, nil
}

// exchange trades the assertion for an access token.
func (f *FCM) exchange(ctx context.Context, assertion string) (string, time.Duration, error) {
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.creds.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("build token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := f.http.Do(request)
	if err != nil {
		return "", 0, fmt.Errorf("exchange service account assertion: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	body := readLimited(response.Body)

	if response.StatusCode != http.StatusOK {
		// Almost always clock skew, a revoked key, or the Cloud Messaging API
		// left disabled on the project -- all configuration, so the batch is
		// not worth retrying until somebody looks.
		return "", 0, fmt.Errorf("%w: token endpoint responded %d: %s",
			errAuth, response.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, fmt.Errorf("parse token response: %w", err)
	}
	if parsed.AccessToken == "" {
		return "", 0, fmt.Errorf("%w: token endpoint returned no access token", errAuth)
	}

	ttl := time.Duration(parsed.ExpiresIn) * time.Second
	if ttl <= 0 {
		// Google always sends expires_in; a missing one is treated as the
		// documented default rather than as "never expires", which would pin a
		// dead token for the life of the process.
		ttl = assertionTTL
	}

	return parsed.AccessToken, ttl, nil
}
