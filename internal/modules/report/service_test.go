package report_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/model"
	"service_nusantara/internal/modules/report"
)

// --- fakes -------------------------------------------------------------

type fakeRepo struct {
	transactions []report.Transaction
	total        int64
	statuses     []report.StatusSummary
	points       []report.RevenuePoint
	totals       report.FinancialTotals
	top          []report.TopProduct
	failWith     error

	// Captured arguments, so a test can assert what the service asked for.
	lastTransactionQuery report.TransactionQuery
	lastFinancialQuery   report.FinancialQuery
	lastTopQuery         report.TopProductQuery
}

func (f *fakeRepo) Transactions(_ context.Context, query report.TransactionQuery) ([]report.Transaction, int64, error) {
	f.lastTransactionQuery = query
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	return f.transactions, f.total, nil
}

func (f *fakeRepo) StatusSummary(context.Context, report.Filters) ([]report.StatusSummary, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.statuses, nil
}

func (f *fakeRepo) Revenue(_ context.Context, query report.FinancialQuery) ([]report.RevenuePoint, report.FinancialTotals, error) {
	f.lastFinancialQuery = query
	if f.failWith != nil {
		return nil, report.FinancialTotals{}, f.failWith
	}
	return f.points, f.totals, nil
}

func (f *fakeRepo) TopProducts(_ context.Context, query report.TopProductQuery) ([]report.TopProduct, error) {
	f.lastTopQuery = query
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.top, nil
}

func newService(repo *fakeRepo) *report.Service {
	return report.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func day(value string) time.Time {
	parsed, err := time.ParseInLocation("2006-01-02", value, time.UTC)
	if err != nil {
		panic(err)
	}
	return parsed
}

func testRange(t *testing.T, from, to string) report.Range {
	t.Helper()
	period, err := report.ParseRange(from, to)
	require.NoError(t, err)
	return period
}

// --- range parsing -----------------------------------------------------

func TestParseRangeRejectsMissingDates(t *testing.T) {
	_, err := report.ParseRange("", "")

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusUnprocessableEntity, appErr.Status)

	fields, ok := appErr.Details.([]httpx.FieldError)
	require.True(t, ok)
	assert.Len(t, fields, 2, "both bounds should be reported at once")
}

func TestParseRangeRejectsInvertedRange(t *testing.T) {
	_, err := report.ParseRange("2026-03-01", "2026-02-01")

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusUnprocessableEntity, appErr.Status)
}

func TestParseRangeRejectsUnparseableDate(t *testing.T) {
	_, err := report.ParseRange("01/03/2026", "2026-03-05")

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusUnprocessableEntity, appErr.Status)
}

func TestParseRangeCapsTheWindow(t *testing.T) {
	from := day("2024-01-01")
	tooLong := from.AddDate(0, 0, report.MaxRangeDays) // inclusive length = Max+1

	_, err := report.ParseRange("2024-01-01", tooLong.Format("2006-01-02"))

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusUnprocessableEntity, appErr.Status)

	// One day shorter is the largest range that is still accepted.
	atLimit := from.AddDate(0, 0, report.MaxRangeDays-1)
	period, err := report.ParseRange("2024-01-01", atLimit.Format("2006-01-02"))
	require.NoError(t, err)
	assert.Equal(t, report.MaxRangeDays, period.Days())
}

func TestRangeEndIsExclusiveDayAfter(t *testing.T) {
	period := testRange(t, "2026-01-10", "2026-01-12")

	// An order placed at 23:59 on the final day must fall inside the window.
	assert.True(t, period.End().After(day("2026-01-12").Add(23*time.Hour+59*time.Minute)))
	assert.Equal(t, day("2026-01-13"), period.End())
	assert.Equal(t, 3, period.Days())
}

func TestParseGranularity(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want report.Granularity
	}{
		{"", report.GranularityDay},
		{"day", report.GranularityDay},
		{"WEEK", report.GranularityWeek},
		{" month ", report.GranularityMonth},
	} {
		got, err := report.ParseGranularity(tc.raw)
		require.NoError(t, err, tc.raw)
		assert.Equal(t, tc.want, got)
	}

	_, err := report.ParseGranularity("quarter")
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusUnprocessableEntity, appErr.Status)
}

func TestParseStatusRejectsUnknownValue(t *testing.T) {
	status, err := report.ParseStatus(string(model.OrderCompleted))
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", status)

	empty, err := report.ParseStatus("")
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = report.ParseStatus("DEFINITELY_NOT_A_STATUS")
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusUnprocessableEntity, appErr.Status)
}

func TestParsePaymentMethodIsCaseInsensitive(t *testing.T) {
	method, err := report.ParsePaymentMethod("qris")
	require.NoError(t, err)
	assert.Equal(t, "QRIS", method)

	_, err = report.ParsePaymentMethod("BITCOIN")
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusUnprocessableEntity, appErr.Status)
}

// --- revenue statuses --------------------------------------------------

func TestRevenueExcludesCancelledAndUnpaidOrders(t *testing.T) {
	counted := []model.OrderStatus{
		model.OrderPaid,
		model.OrderWaitingStore,
		model.OrderStoreAccepted,
		model.OrderSearchingDriver,
		model.OrderDriverAssigned,
		model.OrderOnTheWay,
		model.OrderDelivered,
		model.OrderCompleted,
	}
	for _, status := range counted {
		assert.True(t, report.IsRevenueStatus(string(status)), "%s should count as revenue", status)
	}

	excluded := []model.OrderStatus{
		model.OrderDraft,
		model.OrderWaitingPayment,
		model.OrderStoreRejected,
		model.OrderCanceled,
	}
	for _, status := range excluded {
		assert.False(t, report.IsRevenueStatus(string(status)), "%s must not inflate revenue", status)
	}

	assert.Len(t, report.RevenueStatuses, len(counted))
}

// --- summary -----------------------------------------------------------

func TestSummaryRollsUpRevenueSeparatelyFromEverything(t *testing.T) {
	repo := &fakeRepo{statuses: []report.StatusSummary{
		{Status: string(model.OrderCanceled), OrderCount: 2, Total: 500_000, IsRevenue: false},
		{Status: string(model.OrderCompleted), OrderCount: 3, Total: 1_200_000, IsRevenue: true},
		{Status: string(model.OrderDraft), OrderCount: 1, Total: 90_000, IsRevenue: false},
	}}

	summary, err := newService(repo).Summary(context.Background(), report.Filters{
		Range: testRange(t, "2026-01-01", "2026-01-31"),
	})
	require.NoError(t, err)

	assert.Equal(t, int64(6), summary.OrderCount)
	assert.InDelta(t, 1_790_000, summary.Total, 0.001)

	// The cancelled and draft money must not appear in the revenue figures.
	assert.Equal(t, int64(3), summary.RevenueOrderCount)
	assert.InDelta(t, 1_200_000, summary.RevenueTotal, 0.001)

	assert.Equal(t, "2026-01-01", summary.From)
	assert.Equal(t, "2026-01-31", summary.To)
}

func TestSummaryOrdersStatusesByLifecycle(t *testing.T) {
	repo := &fakeRepo{statuses: []report.StatusSummary{
		{Status: string(model.OrderCompleted)},
		{Status: string(model.OrderDraft)},
		{Status: string(model.OrderPaid)},
	}}

	summary, err := newService(repo).Summary(context.Background(), report.Filters{
		Range: testRange(t, "2026-01-01", "2026-01-31"),
	})
	require.NoError(t, err)

	assert.Equal(t, []string{
		string(model.OrderDraft),
		string(model.OrderPaid),
		string(model.OrderCompleted),
	}, []string{summary.Statuses[0].Status, summary.Statuses[1].Status, summary.Statuses[2].Status})
}

func TestSummarySurfacesRepositoryFailureAsInternal(t *testing.T) {
	repo := &fakeRepo{failWith: errors.New("connection reset")}

	_, err := newService(repo).Summary(context.Background(), report.Filters{
		Range: testRange(t, "2026-01-01", "2026-01-31"),
	})

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusInternalServerError, appErr.Status)
	// The driver's wording must not reach the client.
	assert.NotContains(t, appErr.Message, "connection reset")
}

// --- financial ---------------------------------------------------------

func TestFinancialEchoesTheRangeAndCountedStatuses(t *testing.T) {
	repo := &fakeRepo{
		points: []report.RevenuePoint{{Bucket: "2026-02-01", OrderCount: 4, Gross: 900_000, Net: 850_000}},
		totals: report.FinancialTotals{OrderCount: 4, Gross: 900_000, Net: 850_000},
	}

	result, err := newService(repo).Financial(context.Background(), report.FinancialQuery{
		Filters:     report.Filters{Range: testRange(t, "2026-02-01", "2026-02-28")},
		Granularity: report.GranularityWeek,
	})
	require.NoError(t, err)

	assert.Equal(t, report.GranularityWeek, result.Granularity)
	assert.Equal(t, "2026-02-01", result.From)
	assert.Equal(t, "2026-02-28", result.To)
	assert.Len(t, result.Points, 1)
	assert.InDelta(t, 850_000, result.Totals.Net, 0.001)
	assert.Contains(t, result.RevenueStatuses, string(model.OrderCompleted))
	assert.NotContains(t, result.RevenueStatuses, string(model.OrderCanceled))
}

func TestFinancialPassesTheGranularityThrough(t *testing.T) {
	repo := &fakeRepo{}

	_, err := newService(repo).Financial(context.Background(), report.FinancialQuery{
		Filters:     report.Filters{Range: testRange(t, "2026-02-01", "2026-02-28")},
		Granularity: report.GranularityMonth,
	})
	require.NoError(t, err)

	assert.Equal(t, report.GranularityMonth, repo.lastFinancialQuery.Granularity)
}

// --- top products ------------------------------------------------------

func TestTopProductsClampsTheLimit(t *testing.T) {
	for _, tc := range []struct{ given, want int }{
		{0, report.DefaultTopProducts},
		{-5, report.DefaultTopProducts},
		{7, 7},
		{5_000, report.MaxTopProducts},
	} {
		repo := &fakeRepo{}
		_, err := newService(repo).TopProducts(context.Background(), report.TopProductQuery{
			Filters: report.Filters{Range: testRange(t, "2026-01-01", "2026-01-31")},
			Limit:   tc.given,
		})
		require.NoError(t, err)
		assert.Equal(t, tc.want, repo.lastTopQuery.Limit, "limit %d", tc.given)
	}
}

func TestTransactionsPassesPagingThrough(t *testing.T) {
	repo := &fakeRepo{total: 42}

	_, total, err := newService(repo).Transactions(context.Background(), report.TransactionQuery{
		Filters: report.Filters{Range: testRange(t, "2026-01-01", "2026-01-31")},
		Page:    3,
		PerPage: 20,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(42), total)
	assert.Equal(t, 3, repo.lastTransactionQuery.Page)
	assert.Equal(t, 20, repo.lastTransactionQuery.PerPage)
}
