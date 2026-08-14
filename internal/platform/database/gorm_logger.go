package database

import (
	"context"
	"errors"
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

func (a *slogAdapter) Info(ctx context.Context, msg string, args ...any) {
	a.log.InfoContext(ctx, msg, slog.Any("args", args))
}

func (a *slogAdapter) Warn(ctx context.Context, msg string, args ...any) {
	a.log.WarnContext(ctx, msg, slog.Any("args", args))
}

func (a *slogAdapter) Error(ctx context.Context, msg string, args ...any) {
	a.log.ErrorContext(ctx, msg, slog.Any("args", args))
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
