package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// fakePushSender stands in for the push service: it answers per endpoint and
// records which endpoints it was asked to deliver to, and the last payload.
type fakePushSender struct {
	mu      sync.Mutex
	status  map[string]int
	fail    map[string]bool
	calls   []string
	payload []byte
}

func (f *fakePushSender) Send(_ context.Context, sub pushSubscriptionRow, payload []byte) (pushOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sub.Endpoint)
	f.payload = payload
	if f.fail[sub.Endpoint] {
		return pushOutcome{}, errors.New("connection refused")
	}
	if s, ok := f.status[sub.Endpoint]; ok {
		return pushOutcome{Status: s, Body: `{"reason":"test"}`}, nil
	}
	return pushOutcome{Status: http.StatusCreated}, nil
}

func seedPushSubscription(t *testing.T, env *integrationEnv, userID, endpoint string) {
	t.Helper()
	seedPushSubscriptionInClub(t, env, userID, env.clubID, endpoint)
}

func seedPushSubscriptionInClub(t *testing.T, env *integrationEnv, userID, clubID, endpoint string) {
	t.Helper()
	if _, err := env.db.Exec(context.Background(),
		`INSERT INTO push_subscriptions (user_id, club_id, endpoint, p256dh, auth)
		 VALUES ($1, $2, $3, 'p256dh-test', 'auth-test')`,
		userID, clubID, endpoint,
	); err != nil {
		t.Fatalf("seeding push subscription: %v", err)
	}
}

func (e *integrationEnv) notificationsHandler(sender pushSender) *NotificationsHandler {
	cfg := *e.cfg
	cfg.VAPIDPublicKey = "test-vapid-public-key"
	cfg.VAPIDPrivateKey = "test-vapid-private-key"
	h := NewNotificationsHandler(e.db, &cfg, e.log)
	h.push = sender
	return h
}

func postTestPush(t *testing.T, h *NotificationsHandler, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Post("/notifications/test", h.HandleTestPush)
	return serveTestPush(r, token)
}

func serveTestPush(r http.Handler, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/notifications/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(rec, req)
	return rec
}

func TestIntegration_TestPushOwnSubscriptionsOnly(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)

	me, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})
	other, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})

	const (
		okEndpoint    = "https://push.example.invalid/ok"
		goneEndpoint  = "https://push.example.invalid/gone"
		failEndpoint  = "https://push.example.invalid/fail"
		otherEndpoint = "https://push.example.invalid/other-user"
		refusedEndpnt = "https://push.example.invalid/refused"
		missingEndpnt = "https://push.example.invalid/missing"
	)
	seedPushSubscription(t, env, me, okEndpoint)
	seedPushSubscription(t, env, me, goneEndpoint)
	seedPushSubscription(t, env, me, missingEndpnt)
	seedPushSubscription(t, env, me, failEndpoint)
	seedPushSubscription(t, env, me, refusedEndpnt)
	seedPushSubscription(t, env, other, otherEndpoint)

	sender := &fakePushSender{
		status: map[string]int{
			goneEndpoint:  http.StatusGone,
			missingEndpnt: http.StatusNotFound,
			refusedEndpnt: http.StatusForbidden,
		},
		fail: map[string]bool{failEndpoint: true},
	}
	h := env.notificationsHandler(sender)

	rec := postTestPush(t, h, env.generateToken(me, []string{"member"}))
	assertStatus(t, rec, http.StatusOK)

	got := decodeJSON[testPushResult](t, rec)
	want := testPushResult{Sent: 1, Removed: 2, Failed: 2, Total: 5}
	if got != want {
		t.Fatalf("result = %+v, want %+v", got, want)
	}

	for _, c := range sender.calls {
		if c == otherEndpoint {
			t.Fatalf("test push reached another user's subscription")
		}
	}
	if len(sender.calls) != 5 {
		t.Fatalf("expected 5 sends, got %d: %v", len(sender.calls), sender.calls)
	}

	rows, err := env.db.Query(context.Background(),
		`SELECT endpoint FROM push_subscriptions ORDER BY endpoint`)
	if err != nil {
		t.Fatalf("listing subscriptions: %v", err)
	}
	remaining := map[string]bool{}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatalf("scan: %v", err)
		}
		remaining[e] = true
	}
	rows.Close()
	for _, gone := range []string{goneEndpoint, missingEndpnt} {
		if remaining[gone] {
			t.Errorf("expired subscription %s was not removed", gone)
		}
	}
	for _, kept := range []string{okEndpoint, failEndpoint, refusedEndpnt, otherEndpoint} {
		if !remaining[kept] {
			t.Errorf("subscription %s was removed but should be kept", kept)
		}
	}
}

func TestIntegration_TestPushNoSubscription(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)

	me, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})
	other, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})
	// Another member's device must not count as the caller's.
	seedPushSubscription(t, env, other, "https://push.example.invalid/other-user")

	sender := &fakePushSender{}
	h := env.notificationsHandler(sender)

	rec := postTestPush(t, h, env.generateToken(me, []string{"member"}))
	assertStatus(t, rec, http.StatusConflict)

	body := decodeJSON[map[string]string](t, rec)
	if body["code"] != codeNoPushSubscription {
		t.Fatalf("code = %q, want %q", body["code"], codeNoPushSubscription)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("expected no sends, got %v", sender.calls)
	}
}

func TestIntegration_TestPushClubScoped(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)

	me, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})
	otherClub := testutil.SeedClub(t, env.db)
	// The same user id with a subscription filed under another club (a
	// session in one club must never push to devices registered in another).
	seedPushSubscriptionInClub(t, env, me, otherClub, "https://push.example.invalid/other-club")

	sender := &fakePushSender{}
	h := env.notificationsHandler(sender)

	rec := postTestPush(t, h, env.generateToken(me, []string{"member"}))
	assertStatus(t, rec, http.StatusConflict)
	if len(sender.calls) != 0 {
		t.Fatalf("pushed to another club's subscription: %v", sender.calls)
	}

	seedPushSubscription(t, env, me, "https://push.example.invalid/own-club")
	rec = postTestPush(t, h, env.generateToken(me, []string{"member"}))
	assertStatus(t, rec, http.StatusOK)
	if len(sender.calls) != 1 || sender.calls[0] != "https://push.example.invalid/own-club" {
		t.Fatalf("expected only the own-club subscription, got %v", sender.calls)
	}
}

func TestIntegration_TestPushLanguageAndClubName(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)

	me, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})
	if _, err := env.db.Exec(context.Background(),
		`UPDATE users SET preferred_language = 'nn' WHERE id = $1`, me); err != nil {
		t.Fatalf("setting language: %v", err)
	}
	var clubName string
	if err := env.db.QueryRow(context.Background(),
		`SELECT name FROM clubs WHERE id = $1`, env.clubID).Scan(&clubName); err != nil {
		t.Fatalf("club name: %v", err)
	}
	seedPushSubscription(t, env, me, "https://push.example.invalid/ok")

	sender := &fakePushSender{}
	h := env.notificationsHandler(sender)
	rec := postTestPush(t, h, env.generateToken(me, []string{"member"}))
	assertStatus(t, rec, http.StatusOK)

	var p map[string]string
	if err := json.Unmarshal(sender.payload, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p["title"] != "Testvarsel frå "+clubName {
		t.Errorf("title = %q", p["title"])
	}
	if p["body"] != "Varslar fungerer på denne eininga." {
		t.Errorf("body = %q", p["body"])
	}
	if p["url"] != "/portal/notifications" {
		t.Errorf("url = %q", p["url"])
	}
}

func TestIntegration_TestPushRateLimited(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)

	me, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})
	seedPushSubscription(t, env, me, "https://push.example.invalid/ok")

	sender := &fakePushSender{}
	h := env.notificationsHandler(sender)
	rl := middleware.NewRateLimiter(env.redis, env.log, PushTestRateLimit, time.Minute, PushTestRateKey)

	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.With(rl.Handler).Post("/notifications/test", h.HandleTestPush)

	token := env.generateToken(me, []string{"member"})
	for i := 0; i < PushTestRateLimit; i++ {
		assertStatus(t, serveTestPush(r, token), http.StatusOK)
	}
	assertStatus(t, serveTestPush(r, token), http.StatusTooManyRequests)
	if len(sender.calls) != PushTestRateLimit {
		t.Fatalf("expected %d sends, got %d", PushTestRateLimit, len(sender.calls))
	}
}
