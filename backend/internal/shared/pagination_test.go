package shared

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePagination_Defaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	pg := ParsePagination(req, 50, 100)
	if pg.Limit != 50 {
		t.Errorf("expected limit 50, got %d", pg.Limit)
	}
	if pg.Offset != 0 {
		t.Errorf("expected offset 0, got %d", pg.Offset)
	}
}

func TestParsePagination_CustomValues(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test?limit=25&offset=50", nil)
	pg := ParsePagination(req, 50, 100)
	if pg.Limit != 25 {
		t.Errorf("expected limit 25, got %d", pg.Limit)
	}
	if pg.Offset != 50 {
		t.Errorf("expected offset 50, got %d", pg.Offset)
	}
}

func TestParsePagination_CapsMaxLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test?limit=500", nil)
	pg := ParsePagination(req, 50, 100)
	if pg.Limit != 100 {
		t.Errorf("expected limit capped at 100, got %d", pg.Limit)
	}
}

func TestParsePagination_NegativeOffset(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test?offset=-10", nil)
	pg := ParsePagination(req, 50, 100)
	if pg.Offset != 0 {
		t.Errorf("expected offset 0 for negative input, got %d", pg.Offset)
	}
}

func TestParsePagination_ZeroLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test?limit=0", nil)
	pg := ParsePagination(req, 50, 100)
	if pg.Limit != 50 {
		t.Errorf("expected default limit 50 for zero input, got %d", pg.Limit)
	}
}

func TestNewPaginatedResponse_HasMore(t *testing.T) {
	pg := Pagination{Limit: 10, Offset: 0}
	resp := NewPaginatedResponse([]int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 10, pg)
	if !resp.HasMore {
		t.Error("expected has_more=true when count equals limit")
	}
}

func TestNewPaginatedResponse_NoMore(t *testing.T) {
	pg := Pagination{Limit: 10, Offset: 0}
	resp := NewPaginatedResponse([]int{1, 2, 3}, 3, pg)
	if resp.HasMore {
		t.Error("expected has_more=false when count < limit")
	}
}
