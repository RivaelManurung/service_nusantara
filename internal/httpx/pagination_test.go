package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"service_nusantara/internal/httpx"
)

func parse(query string) httpx.Pagination {
	return httpx.ParsePagination(httptest.NewRequest(http.MethodGet, "/products?"+query, nil))
}

func TestParsePaginationTable(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantPage    int
		wantPerPage int
	}{
		{"defaults when absent", "", 1, 20},
		{"reads explicit values", "page=3&per_page=50", 3, 50},
		{"clamps per_page to the maximum", "per_page=100000", 1, 100},
		{"falls back on a non-numeric page", "page=abc", 1, 20},
		{"falls back on a zero page", "page=0", 1, 20},
		{"falls back on a negative per_page", "per_page=-5", 1, 20},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parse(tc.query)

			assert.Equal(t, tc.wantPage, got.CurrentPage)
			assert.Equal(t, tc.wantPerPage, got.PerPage)
		})
	}
}

func TestOffsetIsZeroOnTheFirstPage(t *testing.T) {
	assert.Equal(t, 0, parse("page=1&per_page=20").Offset())
}

func TestOffsetSkipsPreviousPages(t *testing.T) {
	assert.Equal(t, 40, parse("page=3&per_page=20").Offset())
}

func TestWithTotalRoundsPartialPagesUp(t *testing.T) {
	// 41 rows at 20 per page is 3 pages, not 2.
	got := parse("per_page=20").WithTotal(41)

	assert.Equal(t, int64(41), got.TotalData)
	assert.Equal(t, 3, got.TotalPages)
}

func TestWithTotalReportsNoPagesForAnEmptyResult(t *testing.T) {
	assert.Equal(t, 0, parse("").WithTotal(0).TotalPages)
}
