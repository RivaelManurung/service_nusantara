package report

import (
	"context"
	"log/slog"
	"sort"

	"service_nusantara/internal/httpx"
)

// Service holds the reporting rules: which orders count, in what order the
// summary is presented, and how big a request may be.
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Transactions returns one page of orders for the period.
func (s *Service) Transactions(ctx context.Context, query TransactionQuery) ([]Transaction, int64, error) {
	rows, total, err := s.repo.Transactions(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load transaction report").WithCause(err)
	}
	return rows, total, nil
}

// statusOrder is the lifecycle order the summary is presented in, so the cards
// read as a funnel instead of in whatever order the GROUP BY happened to emit.
var statusOrder = func() map[string]int {
	order := make(map[string]int, len(allStatuses))
	for index, status := range allStatuses {
		order[string(status)] = index
	}
	return order
}()

// Summary returns per-status counts and totals for the period.
//
// Statuses with no orders are omitted rather than zero-filled: the screen wants
// to show what happened, and a wall of zero cards buries the rows that matter.
func (s *Service) Summary(ctx context.Context, filters Filters) (TransactionSummary, error) {
	rows, err := s.repo.StatusSummary(ctx, filters)
	if err != nil {
		return TransactionSummary{}, httpx.Internal("failed to summarise transactions").WithCause(err)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		return statusOrder[rows[i].Status] < statusOrder[rows[j].Status]
	})

	summary := TransactionSummary{
		From:     filters.Range.FromString(),
		To:       filters.Range.ToString(),
		Statuses: rows,
	}

	// These are per-status subtotals the database already rounded, and there is
	// one per status -- at most twelve additions, not one per order. Rolling
	// them up here avoids a second round trip without reintroducing the drift a
	// row-by-row sum would cause.
	for _, row := range rows {
		summary.OrderCount += row.OrderCount
		summary.Total += row.Total
		if row.IsRevenue {
			summary.RevenueOrderCount += row.OrderCount
			summary.RevenueTotal += row.Total
		}
	}

	return summary, nil
}

// Financial returns the revenue series and the period's totals.
func (s *Service) Financial(ctx context.Context, query FinancialQuery) (FinancialReport, error) {
	points, totals, err := s.repo.Revenue(ctx, query)
	if err != nil {
		return FinancialReport{}, httpx.Internal("failed to build financial report").WithCause(err)
	}

	statuses := make([]string, 0, len(RevenueStatuses))
	for _, status := range RevenueStatuses {
		statuses = append(statuses, string(status))
	}

	return FinancialReport{
		From:            query.Range.FromString(),
		To:              query.Range.ToString(),
		Granularity:     query.Granularity,
		Points:          points,
		Totals:          totals,
		RevenueStatuses: statuses,
	}, nil
}

// TopProducts returns the period's best sellers.
func (s *Service) TopProducts(ctx context.Context, query TopProductQuery) ([]TopProduct, error) {
	query.Limit = clampLimit(query.Limit)

	rows, err := s.repo.TopProducts(ctx, query)
	if err != nil {
		return nil, httpx.Internal("failed to load top products").WithCause(err)
	}
	return rows, nil
}

// clampLimit keeps an out-of-range `limit` from turning the top-seller list
// into an unbounded query.
func clampLimit(limit int) int {
	switch {
	case limit < 1:
		return DefaultTopProducts
	case limit > MaxTopProducts:
		return MaxTopProducts
	default:
		return limit
	}
}
