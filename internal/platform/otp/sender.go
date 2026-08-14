package otp

import (
	"context"
	"fmt"
	"log/slog"

	"service_nusantara/internal/platform/logging"
)

// Sender delivers a code to a phone number.
//
// It is an interface so the SMS provider is a deployment decision. The previous
// service called Twilio directly from the use case, which made every code path
// that touched OTP untestable without network access.
type Sender interface {
	Send(ctx context.Context, phone, code string) error
	Name() string
}

// LogSender writes codes to the log instead of sending them.
//
// It exists so the phone flow is fully exercisable in development without an
// SMS account. NewLogSender refuses to build in production, because a code in
// the log is a code in every log aggregator.
type LogSender struct{}

func NewLogSender(isProduction bool) (*LogSender, error) {
	if isProduction {
		return nil, fmt.Errorf("the log OTP sender must not be used in production; configure a real SMS provider")
	}
	return &LogSender{}, nil
}

func (l *LogSender) Name() string { return "log" }

func (l *LogSender) Send(ctx context.Context, phone, code string) error {
	logging.FromContext(ctx).Warn("otp delivered to the log, not by sms",
		slog.String("phone", phone),
		slog.String("code", code))
	return nil
}
