package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func newTestFaqsHandler(t *testing.T) *FaqsHandler {
	t.Helper()
	return NewFaqsHandler(nil, testConfig(), zerolog.Nop())
}

func TestHandleFaqAdminListUnauthenticated(t *testing.T) {
	h := newTestFaqsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/faqs", h.HandleAdminList, "board", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/faqs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleFaqAdminListForbidden(t *testing.T) {
	h := newTestFaqsHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/faqs", h.HandleAdminList, "board", "admin")

	token := generateTestToken("user-1", "club-1", []string{"member"})
	req := httptest.NewRequest(http.MethodGet, "/admin/faqs", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleFaqCreateInvalidBody(t *testing.T) {
	h := newTestFaqsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/faqs", h.HandleAdminCreate, "board", "admin")

	token := generateTestToken("user-1", "club-1", []string{"board"})
	req := httptest.NewRequest(http.MethodPost, "/admin/faqs", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleFaqCreateValidation(t *testing.T) {
	h := newTestFaqsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/faqs", h.HandleAdminCreate, "board", "admin")
	token := generateTestToken("user-1", "club-1", []string{"board"})

	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{"invalid group", `{"group_key":"bogus","question":"q","answer":"a"}`, "invalid group"},
		{"missing question", `{"group_key":"members","question":"","answer":"a"}`, "question and answer are required"},
		{"missing answer", `{"group_key":"members","question":"q","answer":"  "}`, "question and answer are required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/faqs", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", authHeader(token))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}
			var resp errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, resp.Error)
			}
		})
	}
}

func TestHandleFaqReorderInvalidGroup(t *testing.T) {
	h := newTestFaqsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/faqs/reorder", h.HandleReorder, "board", "admin")

	token := generateTestToken("user-1", "club-1", []string{"board"})
	req := httptest.NewRequest(http.MethodPut, "/admin/faqs/reorder", strings.NewReader(`{"group_key":"nope","ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestFaqCreateThenPublicList exercises the full round-trip: a board member
// creates two published items in different groups plus one draft, and the
// public endpoint returns only the published pair, ordered by the fixed group
// order (newcomers before members) — proving both the publish gate and the
// group ordering.
func TestFaqCreateThenPublicList(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"board"})

	var clubSlug string
	if err := db.QueryRow(ctx, `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&clubSlug); err != nil {
		t.Fatalf("fetching club slug: %v", err)
	}
	cfg := &config.Config{ClubSlug: clubSlug}
	h := NewFaqsHandler(db, cfg, zerolog.Nop())

	adminRouter := setupRoleProtectedRouter(http.MethodPost, "/admin/faqs", h.HandleAdminCreate, "board", "admin")
	token := generateTestToken(userID, clubID, []string{"board"})

	create := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/admin/faqs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", authHeader(token))
		rec := httptest.NewRecorder()
		adminRouter.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: expected 201, got %d; body: %s", rec.Code, rec.Body.String())
		}
	}

	create(`{"group_key":"members","question":"Members Q","answer":"A","published":true}`)
	create(`{"group_key":"newcomers","question":"Newcomers Q","answer":"A","published":true}`)
	create(`{"group_key":"members","question":"Draft Q","answer":"A","published":false}`)

	// Public list — no auth.
	pub := chi.NewRouter()
	pub.Get("/faqs", h.HandleListPublic)
	req := httptest.NewRequest(http.MethodGet, "/faqs", nil)
	rec := httptest.NewRecorder()
	pub.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("public list: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Items []faqItem `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 published items, got %d", len(resp.Items))
	}
	if resp.Items[0].GroupKey != "newcomers" || resp.Items[1].GroupKey != "members" {
		t.Errorf("expected newcomers then members, got %q then %q",
			resp.Items[0].GroupKey, resp.Items[1].GroupKey)
	}
	for _, it := range resp.Items {
		if !it.Published {
			t.Errorf("public list returned a draft: %q", it.Question)
		}
	}
}
