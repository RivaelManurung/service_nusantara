package report_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/report"
)

func newHandler(repo *fakeRepo) *report.Handler {
	return report.NewHandler(newService(repo))
}

// call runs a handler through httpx.Handler so returned errors are rendered the
// same way the router renders them.
func call(t *testing.T, handler func(http.ResponseWriter, *http.Request) error, query string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/report/transactions?"+query, nil)
	httpx.Handler(handler).ServeHTTP(recorder, request)
	return recorder
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) httpx.Envelope {
	t.Helper()
	var envelope httpx.Envelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope
}

func TestTransactionsRejectsAMissingRange(t *testing.T) {
	repo := &fakeRepo{}
	recorder := call(t, newHandler(repo).Transactions, "")

	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	// The repository must not have been reached: an unbounded report is exactly
	// what the 422 exists to prevent.
	assert.Zero(t, repo.lastTransactionQuery.PerPage)
}

func TestTransactionsRejectsAnInvertedRange(t *testing.T) {
	recorder := call(t, newHandler(&fakeRepo{}).Transactions, "from=2026-03-10&to=2026-03-01")
	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
}

func TestTransactionsReportsEveryBadFilterAtOnce(t *testing.T) {
	recorder := call(t, newHandler(&fakeRepo{}).Transactions,
		"from=2026-01-01&to=2026-01-31&status=NOPE&payment_method=BITCOIN&shop_id=not-a-uuid")

	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)

	envelope := decodeEnvelope(t, recorder)
	body, ok := envelope.Error.(map[string]any)
	require.True(t, ok)
	details, ok := body["details"].([]any)
	require.True(t, ok)
	assert.Len(t, details, 3, "status, payment_method and shop_id should all be reported")
}

func TestTransactionsAcceptsAValidRequest(t *testing.T) {
	shopID := uuid.New()
	repo := &fakeRepo{
		transactions: []report.Transaction{{Code: "INV-1", Status: "COMPLETED", IsRevenue: true}},
		total:        1,
	}

	recorder := call(t, newHandler(repo).Transactions,
		"from=2026-01-01&to=2026-01-31&status=COMPLETED&payment_method=qris&shop_id="+shopID.String()+"&page=2&per_page=25")

	require.Equal(t, http.StatusOK, recorder.Code)

	assert.Equal(t, "COMPLETED", repo.lastTransactionQuery.Status)
	assert.Equal(t, "QRIS", repo.lastTransactionQuery.PaymentMethod)
	assert.Equal(t, shopID, repo.lastTransactionQuery.ShopID)
	assert.Equal(t, 2, repo.lastTransactionQuery.Page)
	assert.Equal(t, 25, repo.lastTransactionQuery.PerPage)

	envelope := decodeEnvelope(t, recorder)
	require.NotNil(t, envelope.Pagination)
	assert.Equal(t, int64(1), envelope.Pagination.TotalData)
}

func TestSummaryIgnoresTheStatusFilter(t *testing.T) {
	repo := &fakeRepo{}
	recorder := call(t, newHandler(repo).Summary, "from=2026-01-01&to=2026-01-31&status=COMPLETED")

	// A status that would be rejected on the list endpoint is simply not read
	// here, because the summary is the breakdown across all of them.
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestFinancialRejectsAnUnknownGranularity(t *testing.T) {
	recorder := call(t, newHandler(&fakeRepo{}).Financial, "from=2026-01-01&to=2026-01-31&granularity=quarter")
	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
}

func TestTopProductsDefaultsItsLimit(t *testing.T) {
	repo := &fakeRepo{}
	recorder := call(t, newHandler(repo).TopProducts, "from=2026-01-01&to=2026-01-31")

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, report.DefaultTopProducts, repo.lastTopQuery.Limit)
}
