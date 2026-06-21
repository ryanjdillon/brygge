package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/mail"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

const bulkAddr = "kasserar@klokkarvikbaatlag.no"

func bulkSpec() []mail.MailboxSpec {
	return []mail.MailboxSpec{{
		Address:     bulkAddr,
		Role:        "board",
		DisplayName: "Kasserar",
		Type:        "shared",
		SendAs:      true,
	}}
}

func newBulkHandler(t *testing.T, pool *pgxpool.Pool, withPassword bool) *InboxHandler {
	t.Helper()
	pw := mail.PrincipalPasswords{}
	if withPassword {
		pw[strings.ToLower(bulkAddr)] = "secret"
	}
	return &InboxHandler{
		db:          pool,
		passwords:   pw,
		audit:       audit.NewService(nil, zerolog.Nop()), // nil db → Log is a no-op
		spec:        bulkSpec(),
		log:         zerolog.Nop(),
		broadcasts:  bcast.NewStore(pool),
		sharedIDs:   map[string]string{},
		sendTargets: map[string]sendTarget{},
	}
}

func bulkRequest(t *testing.T, h *InboxHandler, clubID, actorID string, body SendRequest) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	ctx := withTestClaims(context.Background(), actorID, clubID, []string{"board", "admin"})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("address", bulkAddr)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req := httptest.NewRequest(http.MethodPost, "/inbox/x/send", bytes.NewReader(raw)).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.HandleSend(rec, req)
	return rec
}

// TestIsBulkSend covers the send-path fork in isolation (no DB/JMAP).
func TestIsBulkSend(t *testing.T) {
	plain := mail.MailboxSpec{Address: bulkAddr, Role: "board", Type: "shared"}
	autoBcc := mail.MailboxSpec{Address: bulkAddr, Role: "board", Type: "shared", BccMembers: true}

	cases := []struct {
		name string
		req  SendRequest
		spec mail.MailboxSpec
		want bool
	}{
		{"to only", SendRequest{To: []emailAddr{{Email: "a@x.no"}}}, plain, false},
		{"to and cc", SendRequest{To: []emailAddr{{Email: "a@x.no"}}, Cc: []emailAddr{{Email: "b@x.no"}}}, plain, false},
		{"explicit bcc", SendRequest{Bcc: []emailAddr{{Email: "a@x.no"}}}, plain, true},
		{"group", SendRequest{BccGroups: []string{"members"}}, plain, true},
		{"auto bcc members mailbox", SendRequest{To: []emailAddr{{Email: "a@x.no"}}}, autoBcc, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBulkSend(tc.req, tc.spec); got != tc.want {
				t.Errorf("isBulkSend = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHandleBulkSend_GroupEnqueues202(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	for i := 0; i < 3; i++ {
		testutil.SeedUser(t, pool, clubID, []string{"member"})
	}
	h := newBulkHandler(t, pool, true)

	rec := bulkRequest(t, h, clubID, actor, SendRequest{
		Subject:   "AGM på lørdag",
		BodyText:  "Hei alle",
		BccGroups: []string{"members"},
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		BroadcastID    string `json:"broadcast_id"`
		RecipientCount int    `json:"recipient_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if resp.BroadcastID == "" || resp.RecipientCount != 3 {
		t.Fatalf("resp = %+v, want id + recipient_count 3", resp)
	}

	list, err := bcast.NewStore(pool).List(context.Background(), clubID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("broadcasts = %d, want 1", len(list))
	}
	b := list[0]
	if b.Total != 3 || b.Pending != 3 || b.Sent != 0 {
		t.Errorf("counts = total%d/pending%d/sent%d, want 3/3/0", b.Total, b.Pending, b.Sent)
	}
	if b.Status != bcast.BroadcastPending {
		t.Errorf("status = %q, want pending", b.Status)
	}
}

func TestHandleBulkSend_DedupAndPrefersMember(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	// Actor holds a role outside the targeted groups so they aren't
	// themselves a recipient (keeps the dedup count unambiguous).
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	// userA holds both member and board roles → appears in both groups.
	userA, emailA := testutil.SeedUser(t, pool, clubID, []string{"member", "board"})
	testutil.SeedUser(t, pool, clubID, []string{"member"}) // userB, member only
	h := newBulkHandler(t, pool, true)

	rec := bulkRequest(t, h, clubID, actor, SendRequest{
		Subject:   "Notice",
		BodyText:  "x",
		BccGroups: []string{"members", "board"},
		// Explicit bcc: a duplicate of userA plus a brand-new external addr.
		Bcc: []emailAddr{{Email: strings.ToUpper(emailA)}, {Email: "ext@example.com"}},
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		BroadcastID    string `json:"broadcast_id"`
		RecipientCount int    `json:"recipient_count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	// Unique recipients: userA, userB, ext@example.com = 3.
	if resp.RecipientCount != 3 {
		t.Fatalf("recipient_count = %d, want 3 (deduped)", resp.RecipientCount)
	}

	// userA's delivery row must carry the member mapping despite first
	// being added via the case-different explicit bcc.
	var userIDForA *string
	if err := pool.QueryRow(ctx,
		`SELECT user_id FROM broadcast_deliveries WHERE broadcast_id = $1 AND lower(email) = lower($2)`,
		resp.BroadcastID, emailA,
	).Scan(&userIDForA); err != nil {
		t.Fatalf("query userA delivery: %v", err)
	}
	if userIDForA == nil || *userIDForA != userA {
		t.Errorf("userA delivery user_id = %v, want %s", userIDForA, userA)
	}

	// The external address has no member mapping.
	var userIDForExt *string
	if err := pool.QueryRow(ctx,
		`SELECT user_id FROM broadcast_deliveries WHERE broadcast_id = $1 AND email = $2`,
		resp.BroadcastID, "ext@example.com",
	).Scan(&userIDForExt); err != nil {
		t.Fatalf("query ext delivery: %v", err)
	}
	if userIDForExt != nil {
		t.Errorf("ext delivery user_id = %v, want nil", *userIDForExt)
	}
}

func TestHandleBulkSend_ExcludesOptedOutMembers(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	inUser, inEmail := testutil.SeedUser(t, pool, clubID, []string{"member"})
	outUser, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})

	// outUser opts out of the broadcast category in their notification
	// settings — they must be excluded from group sends.
	if _, err := pool.Exec(ctx,
		`INSERT INTO communication_preferences (user_id, club_id, category, email_enabled)
		 VALUES ($1, $2, 'broadcast', false)`, outUser, clubID); err != nil {
		t.Fatalf("seed opt-out: %v", err)
	}

	h := newBulkHandler(t, pool, true)
	rec := bulkRequest(t, h, clubID, actor, SendRequest{
		Subject:   "Notice",
		BodyText:  "x",
		BccGroups: []string{"members"},
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		BroadcastID    string `json:"broadcast_id"`
		RecipientCount int    `json:"recipient_count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.RecipientCount != 1 {
		t.Fatalf("recipient_count = %d, want 1 (opted-out member excluded)", resp.RecipientCount)
	}

	var gotUser, gotEmail string
	if err := pool.QueryRow(ctx,
		`SELECT user_id, email FROM broadcast_deliveries WHERE broadcast_id = $1`, resp.BroadcastID,
	).Scan(&gotUser, &gotEmail); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if gotUser != inUser || gotEmail != inEmail {
		t.Errorf("delivery = %s/%s, want opted-in %s/%s", gotUser, gotEmail, inUser, inEmail)
	}
}

func TestHandleBulkSend_UnknownGroup400(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	h := newBulkHandler(t, pool, true)

	rec := bulkRequest(t, h, clubID, actor, SendRequest{
		Subject:   "x",
		BodyText:  "y",
		BccGroups: []string{"bogus"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	assertNoBroadcasts(t, pool, clubID)
}

func TestHandleBulkSend_NoPassword503(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	testutil.SeedUser(t, pool, clubID, []string{"member"})
	h := newBulkHandler(t, pool, false) // no service password

	rec := bulkRequest(t, h, clubID, actor, SendRequest{
		Subject:   "x",
		BodyText:  "y",
		BccGroups: []string{"members"},
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	assertNoBroadcasts(t, pool, clubID)
}

func assertNoBroadcasts(t *testing.T, pool *pgxpool.Pool, clubID string) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM broadcasts WHERE club_id = $1`, clubID).Scan(&n); err != nil {
		t.Fatalf("count broadcasts: %v", err)
	}
	if n != 0 {
		t.Errorf("broadcasts = %d, want 0 (nothing enqueued)", n)
	}
}
