package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// slogAdapter bridges GORM's logger interface onto slog so SQL diagnostics land
// in the same structured stream as the rest of the service.
type slogAdapter struct {
	log           *slog.Logger
	slowThreshold time.Duration
}

func newGormLogger(log *slog.Logger, slowThreshold time.Duration) gormlogger.Interface {
	return &slogAdapter{log: log.With(slog.String("component", "gorm")), slowThreshold: slowThreshold}
}

// LogMode is part of the interface; levels are controlled by the slog handler.
func (a *slogAdapter) LogMode(gormlogger.LogLevel) gormlogger.Interface { return a }

// GORM passes a printf-style format string plus its arguments. Rendering them
// here does two things the previous slog.Any("args", args) did not: it produces
// the message GORM actually meant, and it cannot fail.
//
// slog.Any handed the raw arguments to the JSON handler, and those arguments
// include gorm.Config -- which carries a NowFunc field of type func() time.Time.
// encoding/json cannot marshal a func, so every GORM diagnostic came out as
// `"args":"!ERROR:json: unsupported type: func() time.Time"` with the real
// message discarded. A logger that destroys the error it was handed is worse
// than no logger at all.
func (a *slogAdapter) render(msg string, args ...any) string {
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

func (a *slogAdapter) Info(ctx context.Context, msg string, args ...any) {
	a.log.InfoContext(ctx, a.render(msg, args...))
}

func (a *slogAdapter) Warn(ctx context.Context, msg string, args ...any) {
	a.log.WarnContext(ctx, a.render(msg, args...))
}

func (a *slogAdapter) Error(ctx context.Context, msg string, args ...any) {
	a.log.ErrorContext(ctx, a.render(msg, args...))
}

// Trace records one statement. Only failures and slow queries are reported at
// warn/error; everything else stays at debug so production logs stay readable.
func (a *slogAdapter) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()

	attrs := []any{
		slog.String("sql", sql),
		slog.Int64("rows", rows),
		slog.Duration("elapsed", elapsed),
	}

	switch {
	// A missing row is a normal outcome the caller handles, not a failure.
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		a.log.ErrorContext(ctx, "query failed", append(attrs, slog.String("error", err.Error()))...)
	case a.slowThreshold > 0 && elapsed > a.slowThreshold:
		a.log.WarnContext(ctx, "slow query", attrs...)
	default:
		a.log.DebugContext(ctx, "query", attrs...)
	}
}
