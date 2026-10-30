package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// The handler is built with a nil db: any path that reaches the database
// panics, so a clean 404 also proves no project or task was written.
func postReportIssue(t *testing.T, projects bool, body string) *httptest.ResponseRecorder {
	t.Helper()
	cfg := testConfig()
	cfg.Features.Projects = projects
	h := NewMembersHandler(nil, cfg, zerolog.Nop())

	router := setupAuthenticatedRouter(http.MethodPost, "/members/me/slip/issues", h.HandleReportIssue)
	req := httptest.NewRequest(http.MethodPost, "/members/me/slip/issues", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+generateTestToken("user-1", "club-1", []string{"member"}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHandleReportIssueProjectsOffAnswers404(t *testing.T) {
	rec := postReportIssue(t, false, `{"title":"Cleat loose","description":"Port side","priority":"high"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 with Projects off, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleReportIssueProjectsOnStillValidates(t *testing.T) {
	rec := postReportIssue(t, true, `{"title":"","description":"Port side"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing title with Projects on, got %d: %s", rec.Code, rec.Body.String())
	}
}
