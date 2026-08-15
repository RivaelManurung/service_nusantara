package push_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/platform/push"
)

// --- helpers -----------------------------------------------------------

// serviceAccountJSON builds a credential document whose token endpoint points
// at the test server, so nothing in these tests reaches Google.
func serviceAccountJSON(t *testing.T, tokenURI string) []byte {
	t.Helper()

	// 1024 bits is far too short for production and perfectly adequate here:
	// these tests assert on signing, not on strength, and a 2048-bit key costs
	// tens of milliseconds per test.
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	document, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "menara-demo",
		"client_email": "fcm@menara-demo.iam.gserviceaccount.com",
		"private_key":  string(block),
		"token_uri":    tokenURI,
	})
	require.NoError(t, err)

	return document
}

// stub is an FCM stand-in: it mints access tokens and answers sends with
// whatever the test decides, per registration token.
type stub struct {
	server *httptest.Server
	// tokenCalls counts OAuth exchanges, so a test can prove the access token
	// is cached rather than minted per device.
	tokenCalls atomic.Int32
	sendCalls  atomic.Int32
	// respond decides the reply for one registration token.
	respond func(registration string) (int, string)
	// bodies records send payloads for assertions.
	bodies chan map[string]any
}

func newStub(t *testing.T, respond func(registration string) (int, string)) *stub {
	t.Helper()

	s := &stub{respond: respond, bodies: make(chan map[string]any, 64)}

	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		s.tokenCalls.Add(1)
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", r.PostForm.Get("grant_type"))
		assert.NotEmpty(t, r.PostForm.Get("assertion"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.test","expires_in":3600,"token_type":"Bearer"}`))
	})

	mux.HandleFunc("/v1/projects/menara-demo/messages:send", func(w http.ResponseWriter, r *http.Request) {
		s.sendCalls.Add(1)

		assert.Equal(t, "Bearer ya29.test", r.Header.Get("Authorization"))

		var envelope struct {
			Message map[string]any `json:"message"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&envelope))

		select {
		case s.bodies <- envelope.Message:
		default:
		}

		registration, _ := envelope.Message["token"].(string)
		status, body := s.respond(registration)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})

	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)

	return s
}

// newSender wires an adapter against the stub.
func newSender(t *testing.T, s *stub) *push.FCM {
	t.Helper()

	creds, err := push.ParseServiceAccount(serviceAccountJSON(t, s.server.URL+"/token"))
	require.NoError(t, err)

	sender, err := push.NewFCM(push.Config{
		Credentials:      creds,
		BaseURL:          s.server.URL,
		AndroidChannelID: "promo",
		HTTPClient:       s.server.Client(),
	})
	require.NoError(t, err)

	return sender
}

const okBody = `{"name":"projects/menara-demo/messages/1"}`

func unregistered() string {
	return `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND",` +
		`"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
}

// --- credentials -------------------------------------------------------

func TestParseServiceAccountRejectsADocumentMissingFields(t *testing.T) {
	_, err := push.ParseServiceAccount([]byte(`{"project_id":"menara-demo"}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client_email")
	assert.Contains(t, err.Error(), "private_key")
}

func TestParseServiceAccountAcceptsAKeyWithEscapedNewlines(t *testing.T) {
	// This is how the document survives a single-line environment variable,
	// and the form an operator is most likely to paste.
	document := serviceAccountJSON(t, "https://oauth2.googleapis.com/token")

	var fields map[string]string
	require.NoError(t, json.Unmarshal(document, &fields))
	fields["private_key"] = strings.ReplaceAll(fields["private_key"], "\n", `\n`)

	escaped, err := json.Marshal(fields)
	require.NoError(t, err)

	creds, err := push.ParseServiceAccount(escaped)

	require.NoError(t, err)
	assert.Equal(t, "menara-demo", creds.ProjectID)
}

func TestLoadServiceAccountReportsNotConfiguredForAnEmptyValue(t *testing.T) {
	_, err := push.LoadServiceAccount("   ")

	assert.ErrorIs(t, err, push.ErrNotConfigured)
}

// --- sending -----------------------------------------------------------

func TestSendDeliversEveryMessageAndCountsThem(t *testing.T) {
	stub := newStub(t, func(string) (int, string) { return http.StatusOK, okBody })
	sender := newSender(t, stub)

	report, err := sender.Send(context.Background(), []push.Message{
		{Token: "device-a", Title: "Promo", Body: "Diskon 50%"},
		{Token: "device-b", Title: "Promo", Body: "Diskon 50%"},
		{Token: "device-c", Title: "Promo", Body: "Diskon 50%"},
	})

	require.NoError(t, err)
	assert.Equal(t, 3, report.Success)
	assert.Zero(t, report.Failure)
	assert.Empty(t, report.InvalidTokens)
	assert.Equal(t, int32(3), stub.sendCalls.Load())
}

// A broadcast must not mint one access token per device: that would be three
// thousand OAuth round trips for a three thousand device promo.
func TestSendMintsOneAccessTokenForTheWholeBatch(t *testing.T) {
	stub := newStub(t, func(string) (int, string) { return http.StatusOK, okBody })
	sender := newSender(t, stub)

	messages := make([]push.Message, 0, 25)
	for i := range 25 {
		messages = append(messages, push.Message{Token: fmt.Sprintf("device-%d", i), Title: "Promo"})
	}

	_, err := sender.Send(context.Background(), messages)

	require.NoError(t, err)
	assert.Equal(t, int32(1), stub.tokenCalls.Load())
}

// The whole point of reporting invalid tokens: the caller deletes them, so the
// next broadcast is not slowed down by devices that no longer exist.
func TestSendReportsUnregisteredDevicesAsInvalidWithoutFailingTheBatch(t *testing.T) {
	stub := newStub(t, func(registration string) (int, string) {
		if registration == "device-gone" {
			return http.StatusNotFound, unregistered()
		}
		return http.StatusOK, okBody
	})
	sender := newSender(t, stub)

	report, err := sender.Send(context.Background(), []push.Message{
		{Token: "device-a", Title: "Promo"},
		{Token: "device-gone", Title: "Promo"},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, report.Success)
	assert.Equal(t, 1, report.Failure)
	assert.Equal(t, []string{"device-gone"}, report.InvalidTokens)
}

// A rejected credential is configuration, not a flaky device: the caller has
// to hear about it rather than read "0 delivered" and shrug.
func TestSendReportsRejectedCredentialsAsAnError(t *testing.T) {
	stub := newStub(t, func(string) (int, string) {
		return http.StatusUnauthorized,
			`{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`
	})
	sender := newSender(t, stub)

	report, err := sender.Send(context.Background(), []push.Message{{Token: "device-a", Title: "Promo"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid authentication credentials")
	assert.Equal(t, 1, report.Failure)
	assert.Empty(t, report.InvalidTokens, "a credential problem must not delete the customer's registration")
}

func TestSendBuildsAHighPriorityPayloadCarryingTheDeepLink(t *testing.T) {
	stub := newStub(t, func(string) (int, string) { return http.StatusOK, okBody })
	sender := newSender(t, stub)

	_, err := sender.Send(context.Background(), []push.Message{{
		Token: "device-a",
		Title: "Promo Merdeka",
		Body:  "Diskon 50% hari ini",
		Data:  map[string]string{"target_type": "VOUCHER", "target_route": "/rewards"},
	}})
	require.NoError(t, err)

	message := <-stub.bodies

	notification, ok := message["notification"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Promo Merdeka", notification["title"])
	assert.Equal(t, "Diskon 50% hari ini", notification["body"])

	data, ok := message["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "VOUCHER", data["target_type"])
	assert.Equal(t, "/rewards", data["target_route"])

	android, ok := message["android"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "HIGH", android["priority"], "a dozing device would otherwise batch the promo for hours")

	androidNotification, ok := android["notification"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "promo", androidNotification["channel_id"],
		"Android 8+ drops a notification whose channel does not exist")
}

func TestSendOnAnEmptyBatchTouchesNothing(t *testing.T) {
	stub := newStub(t, func(string) (int, string) { return http.StatusOK, okBody })
	sender := newSender(t, stub)

	report, err := sender.Send(context.Background(), nil)

	require.NoError(t, err)
	assert.Zero(t, report.Success)
	assert.Equal(t, int32(0), stub.tokenCalls.Load())
}

// --- disabled ----------------------------------------------------------

func TestDisabledSenderRefusesClearly(t *testing.T) {
	var sender push.Sender = push.Disabled{}

	assert.False(t, sender.Enabled())

	_, err := sender.Send(context.Background(), []push.Message{{Token: "device-a"}})
	assert.ErrorIs(t, err, push.ErrNotConfigured)
}
