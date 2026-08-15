// Package push delivers notifications to mobile devices through Firebase
// Cloud Messaging.
//
// It follows the shape of internal/platform/storage: one small interface the
// modules depend on, one real adapter, and one disabled adapter that refuses
// clearly instead of pretending to work. A deployment without FCM credentials
// therefore still serves every other route, and the notification module still
// writes the customer's inbox -- only the tray notification is missing, and it
// says so in the response rather than in a stack trace.
package push

import (
	"context"
	"errors"
)

// ErrNotConfigured is returned by the disabled sender. Callers compare against
// it to tell "push is switched off in this environment" apart from "Firebase
// rejected the request", which are different operational problems.
var ErrNotConfigured = errors.New("push notifications are not configured")

// Message is one notification for one device.
type Message struct {
	// Token is the FCM registration of the target installation.
	Token string
	Title string
	Body  string
	// Data is delivered alongside the notification and is what the app reads
	// when the customer taps it. FCM only carries strings here, which is why
	// the deep-link target is flattened into fields rather than nested.
	Data map[string]string
}

// Report is the outcome of one Send.
//
// A partial failure is normal -- of a thousand registrations a handful are
// always stale -- so Send reports counts instead of failing the whole call.
type Report struct {
	Success int
	Failure int
	// InvalidTokens are registrations FCM rejected as gone (the app was
	// uninstalled, or the token was replaced). The caller deletes them: kept
	// around, they are retried on every broadcast forever.
	InvalidTokens []string
}

// Sender delivers a batch of messages.
//
// Implementations must not fail the batch because one device failed; that is
// what Report exists for. An error is reserved for a problem with the batch
// itself, such as credentials the provider refuses.
type Sender interface {
	Send(ctx context.Context, messages []Message) (Report, error)
	// Enabled reports whether messages can actually be delivered, so a caller
	// can say "saved to the inbox, push is off" instead of attempting a send
	// it knows will fail.
	Enabled() bool
}

// Disabled is the no-op sender used when no credentials are configured.
type Disabled struct{}

// Send never delivers anything and says so.
func (Disabled) Send(context.Context, []Message) (Report, error) {
	return Report{}, ErrNotConfigured
}

// Enabled is always false, which is the whole point of this type.
func (Disabled) Enabled() bool { return false }
