package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func newBroadcastsHandler(t *testing.T, pool *pgxpool.Pool, kick func()) *BroadcastsHandler {
	t.Helper()
	return &BroadcastsHandler{
		store: bcast.NewStore(pool),
		kick:  kick,
		log:   zerolog.Nop(),
	}
}

// seedBroadcast enqueues a broadcast with the given recipient emails and
// returns its id and store.
func seedBroadcast(t *testing.T, pool *pgxpool.Pool, clubID, sentBy string, emails ...string) (*bcast.Store, string) {
	t.Helper()
	store := bcast.NewStore(pool)
	recs := make([]bcast.Recipient, 0, len(emails))
	for _, e := range emails {
		recs = append(recs, bcast.Recipient{Email: e})
	}
	id, err := store.Enqueue(context.Background(), bcast.New{
		ClubID: clubID, SentBy: sentBy, SourceAddress: "kasserar@x.no",
		Subject: "Subject", BodyText: "Body", Recipients: "members",
	}, recs)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return store, id
}

// reqWithClaimsParam builds a request carrying claims and an {id} route param.
func reqWithClaimsParam(t *testing.T, method, clubID, actorID, id string) *http.Request {
	t.Helper()
	ctx := withTestClaims(context.Background(), actorID, clubID, []string{"board"})
	if id != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}
	return httptest.NewRequest(method, "/", nil).WithContext(ctx)
}

func TestBroadcastsList(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	store, id := seedBroadcast(t, pool, clubID, actor, "a@x.no", "b@x.no", "c@x.no")

	// Resolve one sent, one failed; one stays pending.
	claimed, _ := store.ClaimPending(ctx, 10)
	_ = store.Mark(ctx, claimed[0].DeliveryID, bcast.DeliverySent, "")
	_ = store.Mark(ctx, claimed[1].DeliveryID, bcast.DeliveryFailed, "x")
	// claimed[2] left in 'sending' → counts as pending bucket

	h := newBroadcastsHandler(t, pool, nil)
	rec := httptest.NewRecorder()
	h.HandleList(rec, reqWithClaimsParam(t, http.MethodGet, clubID, actor, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Broadcasts []bcast.Summary `json:"broadcasts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Broadcasts) != 1 {
		t.Fatalf("broadcasts = %d, want 1", len(resp.Broadcasts))
	}
	b := resp.Broadcasts[0]
	if b.ID != id || b.Total != 3 || b.Sent != 1 || b.Failed != 1 || b.Pending != 1 {
		t.Errorf("summary = %+v, want total3/sent1/failed1/pending1", b)
	}
}

func TestBroadcastsGetAndScoping(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	_, id := seedBroadcast(t, pool, clubID, actor, "a@x.no", "b@x.no")
	h := newBroadcastsHandler(t, pool, nil)

	// In-club get returns the detail with delivery rows.
	rec := httptest.NewRecorder()
	h.HandleGet(rec, reqWithClaimsParam(t, http.MethodGet, clubID, actor, id))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var d bcast.Detail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.BodyText != "Body" || len(d.Deliveries) != 2 {
		t.Errorf("detail = %+v, want body + 2 deliveries", d)
	}

	// A different club gets 404 (no cross-club leakage).
	otherClub := testutil.SeedClub(t, pool)
	otherActor, _ := testutil.SeedUser(t, pool, otherClub, []string{"board"})
	rec2 := httptest.NewRecorder()
	h.HandleGet(rec2, reqWithClaimsParam(t, http.MethodGet, otherClub, otherActor, id))
	if rec2.Code != http.StatusNotFound {
		t.Errorf("cross-club status = %d, want 404", rec2.Code)
	}
}

func TestBroadcastsRetryRequeuesFailedAndKicks(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	store, id := seedBroadcast(t, pool, clubID, actor, "ok@x.no", "bad@x.no")

	claimed, _ := store.ClaimPending(ctx, 10)
	for _, c := range claimed {
		if c.Email == "ok@x.no" {
			_ = store.Mark(ctx, c.DeliveryID, bcast.DeliverySent, "")
		} else {
			_ = store.Mark(ctx, c.DeliveryID, bcast.DeliveryFailed, "550")
		}
	}
	_ = store.FinalizeIfDone(ctx, id)

	kicked := false
	h := newBroadcastsHandler(t, pool, func() { kicked = true })

	rec := httptest.NewRecorder()
	h.HandleRetry(rec, reqWithClaimsParam(t, http.MethodPost, clubID, actor, id))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Requeued int64 `json:"requeued"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Requeued != 1 {
		t.Errorf("requeued = %d, want 1 (only the failed one)", resp.Requeued)
	}
	if !kicked {
		t.Error("expected worker kick after re-queue")
	}

	// The sent row is untouched; the failed row is pending again.
	var sent, pending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status='sent'), count(*) FILTER (WHERE status='pending')
		 FROM broadcast_deliveries WHERE broadcast_id = $1`, id,
	).Scan(&sent, &pending); err != nil {
		t.Fatalf("count: %v", err)
	}
	if sent != 1 || pending != 1 {
		t.Errorf("after retry sent=%d pending=%d, want 1/1", sent, pending)
	}
}

func TestBroadcastsRetryUnknownIs404(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	h := newBroadcastsHandler(t, pool, nil)

	rec := httptest.NewRecorder()
	// Well-formed but non-existent UUID.
	h.HandleRetry(rec, reqWithClaimsParam(t, http.MethodPost, clubID, actor, "00000000-0000-0000-0000-000000000000"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
