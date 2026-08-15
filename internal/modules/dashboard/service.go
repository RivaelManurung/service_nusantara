package dashboard

import (
	"context"
	"log/slog"
	"time"

	"service_nusantara/internal/httpx"
)

// Service holds the dashboard's rules: what "today" means, and how far back a
// trend may reach.
type Service struct {
	repo Repository
	log  *slog.Logger
	// now is injectable so a test can pin the clock. Nothing else overrides it.
	now func() time.Time
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log, now: time.Now}
}

// Summary returns today's headline figures.
func (s *Service) Summary(ctx context.Context) (Summary, error) {
	now := s.now()

	summary, err := s.repo.Summary(ctx, now, now.Add(-StalledAfter))
	if err != nil {
		return Summary{}, httpx.Internal("failed to load the dashboard summary").WithCause(err)
	}
	return summary, nil
}

// Trend returns a daily sales series, clamped to a sane window.
//
// A bad `days` is clamped rather than rejected: this is a chart on a dashboard,
// and refusing to draw it because a query string was odd helps nobody.
func (s *Service) Trend(ctx context.Context, days int) ([]TrendPoint, error) {
	switch {
	case days <= 0:
		days = DefaultTrendDays
	case days > MaxTrendDays:
		days = MaxTrendDays
	}

	points, err := s.repo.Trend(ctx, days)
	if err != nil {
		return nil, httpx.Internal("failed to load the sales trend").WithCause(err)
	}
	return points, nil
}

// Anomalies returns accounts worth a human look.
//
// The result is a queue, not a verdict. Nothing here blocks anybody: the rules
// are coarse by design, and coarse rules enforced automatically punish real
// customers. Blocking lives on the account screen, where it demands a reason
// and writes an audit row.
func (s *Service) Anomalies(ctx context.Context, limit int) ([]Anomaly, error) {
	switch {
	case limit <= 0:
		limit = DefaultAnomalies
	case limit > MaxAnomalies:
		limit = MaxAnomalies
	}

	findings, err := s.repo.Anomalies(ctx, limit)
	if err != nil {
		return nil, httpx.Internal("failed to run the anomaly checks").WithCause(err)
	}

	if len(findings) > 0 {
		s.log.Info("anomaly scan produced findings",
			slog.Int("count", len(findings)))
	}
	return findings, nil
}
