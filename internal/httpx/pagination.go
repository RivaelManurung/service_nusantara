package httpx

import (
	"net/http"
	"strconv"
)

// Pagination limits are enforced server-side: an unbounded `per_page` lets a
// single request pull an entire table, which is how the previous listing
// endpoints could be turned into a denial of service.
const (
	defaultPage    = 1
	defaultPerPage = 20
	maxPerPage     = 100
)

// Pagination is both the query input and the response metadata.
type Pagination struct {
	CurrentPage int   `json:"current_page"`
	PerPage     int   `json:"per_page"`
	TotalData   int64 `json:"total_data"`
	TotalPages  int   `json:"total_pages"`
}

// Offset is the SQL OFFSET for the current page.
func (p Pagination) Offset() int { return (p.CurrentPage - 1) * p.PerPage }

// Limit is the SQL LIMIT for the current page.
func (p Pagination) Limit() int { return p.PerPage }

// WithTotal returns a copy carrying the total row count and derived page count.
func (p Pagination) WithTotal(total int64) Pagination {
	p.TotalData = total
	if p.PerPage > 0 {
		p.TotalPages = int((total + int64(p.PerPage) - 1) / int64(p.PerPage))
	}
	return p
}

// ParsePagination reads `page` and `per_page`, clamping both into safe ranges.
// Malformed values fall back to defaults rather than failing the request,
// because a bad query string should not break a list screen.
func ParsePagination(r *http.Request) Pagination {
	q := r.URL.Query()

	page := positiveInt(q.Get("page"), defaultPage)
	perPage := positiveInt(q.Get("per_page"), defaultPerPage)
	if perPage > maxPerPage {
		perPage = maxPerPage
	}

	return Pagination{CurrentPage: page, PerPage: perPage}
}

func positiveInt(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		return fallback
	}
	return v
}
