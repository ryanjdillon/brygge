package shared

import (
	"net/http"
	"strconv"
)

// Pagination holds parsed limit/offset query parameters.
type Pagination struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// ParsePagination extracts limit and offset from query parameters with defaults.
// maxLimit caps the maximum allowed limit.
func ParsePagination(r *http.Request, defaultLimit, maxLimit int) Pagination {
	q := r.URL.Query()

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}

	return Pagination{Limit: limit, Offset: offset}
}

// PaginatedResponse wraps a list of items with pagination metadata.
type PaginatedResponse struct {
	Items   any  `json:"items"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}

// NewPaginatedResponse creates a paginated response.
// Pass the actual count of items returned to determine has_more.
func NewPaginatedResponse(items any, count int, p Pagination) PaginatedResponse {
	return PaginatedResponse{
		Items:   items,
		Limit:   p.Limit,
		Offset:  p.Offset,
		HasMore: count >= p.Limit,
	}
}
