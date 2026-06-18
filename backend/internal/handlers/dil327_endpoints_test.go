package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// TestDocumentCommentsCreateAndList covers the DIL-327 document-comment
// endpoints: POST creates a comment, GET lists it back with the author's
// display name.
func TestDocumentCommentsCreateAndList(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"member"})

	var docID string
	if err := db.QueryRow(ctx,
		`INSERT INTO documents (club_id, title, filename, s3_key, uploaded_by)
		 VALUES ($1, 'Vedtekter', 'vedtekter.pdf', 'docs/vedtekter.pdf', $2)
		 RETURNING id`,
		clubID, userID,
	).Scan(&docID); err != nil {
		t.Fatalf("seeding document: %v", err)
	}

	h := NewAdminDocumentsHandler(db, testConfig(), zerolog.Nop())
	token := generateTestToken(userID, clubID, []string{"member"})

	// POST a comment.
	postR := setupRoleProtectedRouter(http.MethodPost, "/documents/{docID}/comments", h.HandleCreateComment, "member")
	req := httptest.NewRequest(http.MethodPost, "/documents/"+docID+"/comments", strings.NewReader(`{"body":"Når er neste dugnad?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	postR.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST comment: status %d, body %s", rec.Code, rec.Body.String())
	}

	// Empty body rejected.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/documents/"+docID+"/comments", strings.NewReader(`{"body":"  "}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", authHeader(token))
	postR.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("empty-body POST: status %d, want 400", rec2.Code)
	}

	// GET lists the comment with author.
	getR := setupRoleProtectedRouter(http.MethodGet, "/documents/{docID}/comments", h.HandleListComments, "member")
	greq := httptest.NewRequest(http.MethodGet, "/documents/"+docID+"/comments", nil)
	greq.Header.Set("Authorization", authHeader(token))
	grec := httptest.NewRecorder()
	getR.ServeHTTP(grec, greq)
	if grec.Code != http.StatusOK {
		t.Fatalf("GET comments: status %d, body %s", grec.Code, grec.Body.String())
	}
	var comments []struct {
		ID     string `json:"id"`
		Author string `json:"author"`
		Body   string `json:"body"`
	}
	if err := json.NewDecoder(grec.Body).Decode(&comments); err != nil {
		t.Fatalf("decode comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].Body != "Når er neste dugnad?" {
		t.Errorf("body = %q", comments[0].Body)
	}
	if comments[0].Author == "" {
		t.Error("author is empty")
	}
}

// TestWaitingListDeclineOffer covers the DIL-327 decline endpoint: an
// offered entry returns to the active queue (the enum has no 'declined').
func TestWaitingListDeclineOffer(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"member"})

	var entryID string
	if err := db.QueryRow(ctx,
		`INSERT INTO waiting_list_entries (user_id, club_id, position, status, offer_deadline)
		 VALUES ($1, $2, 1, 'offered', now() + interval '7 days')
		 RETURNING id`,
		userID, clubID,
	).Scan(&entryID); err != nil {
		t.Fatalf("seeding waiting-list entry: %v", err)
	}

	h := NewWaitingListHandler(db, nil, testConfig(), zerolog.Nop())
	r := setupRoleProtectedRouter(http.MethodPost, "/waiting-list/{entryID}/decline", h.HandleDeclineOffer, "member")
	token := generateTestToken(userID, clubID, []string{"member"})

	req := httptest.NewRequest(http.MethodPost, "/waiting-list/"+entryID+"/decline", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("decline: status %d, body %s", rec.Code, rec.Body.String())
	}

	var status string
	var deadline *time.Time
	if err := db.QueryRow(ctx,
		`SELECT status, offer_deadline FROM waiting_list_entries WHERE id = $1`, entryID,
	).Scan(&status, &deadline); err != nil {
		t.Fatalf("re-read entry: %v", err)
	}
	if status != "active" {
		t.Errorf("status = %q, want active", status)
	}
	if deadline != nil {
		t.Errorf("offer_deadline = %v, want NULL", deadline)
	}
}
