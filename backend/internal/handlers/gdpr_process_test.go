package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/passkey"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

const processDeletionPath = "/admin/gdpr/deletion-requests/{requestID}/process"

func TestHandleProcessDeletionMalformedIDIs404(t *testing.T) {
	// The id check runs before any query, so no database is needed.
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, processDeletionPath, h.HandleProcessDeletion)

	req := httptest.NewRequest(http.MethodPost, "/admin/gdpr/deletion-requests/not-a-uuid/process", nil)
	req.Header.Set("Authorization", authHeader(generateTestToken("admin-1", "club-1", []string{"admin"})))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

type gdprProcessEnv struct {
	db     *pgxpool.Pool
	h      *GDPRHandler
	router chi.Router
	clubID string
	admin  string
}

func newGDPRProcessEnv(t *testing.T, opts ...testutil.DBOption) *gdprProcessEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t, opts...)
	clubID := testutil.SeedClub(t, db)
	adminID, _ := testutil.SeedUser(t, db, clubID, []string{"admin"})
	h := NewGDPRHandler(db, &config.Config{Port: 8080}, zerolog.Nop())
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Post(processDeletionPath, h.HandleProcessDeletion)
	return &gdprProcessEnv{db: db, h: h, router: r, clubID: clubID, admin: adminID}
}

func (e *gdprProcessEnv) process(requestID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/admin/gdpr/deletion-requests/"+requestID+"/process", nil)
	req.Header.Set("Authorization", authHeader(generateTestToken(e.admin, e.clubID, []string{"admin"})))
	req.RemoteAddr = "203.0.113.7"
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// seedMember creates a member holding every kind of login state the deletion
// must revoke, plus a due deletion request. withRecords adds an in-window
// invoice so identity erasure is deferred.
func (e *gdprProcessEnv) seedMember(t *testing.T, withRecords bool) (userID, email, sessionID, requestID string) {
	t.Helper()
	ctx := context.Background()
	userID, email = testutil.SeedUser(t, e.db, e.clubID, []string{"member"})

	sessions := auth.NewSessionService(e.db)
	sessionID, err := sessions.CreateSession(ctx, userID, e.clubID, "198.51.100.4:5555", "test-agent")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, _, err := sessions.ValidateSession(ctx, sessionID); err != nil {
		t.Fatalf("seeded session should validate: %v", err)
	}

	store := passkey.NewStore(e.db)
	if _, err := store.LoadUser(ctx, userID); err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if err := store.SaveCredential(ctx, userID, e.clubID, passkeyTestCredential("erase-"+userID), ""); err != nil {
		t.Fatalf("save passkey: %v", err)
	}

	for _, stmt := range []string{
		`UPDATE users SET totp_secret_encrypted = '\x01', totp_enabled = true WHERE id = $1`,
		`INSERT INTO totp_recovery_codes (user_id, code_hash) VALUES ($1, 'hash')`,
	} {
		if _, err := e.db.Exec(ctx, stmt, userID); err != nil {
			t.Fatalf("seed totp: %v", err)
		}
	}
	if _, err := e.db.Exec(ctx,
		`INSERT INTO magic_links (token, email, club_id, expires_at)
		 VALUES ($1, $2, $3, now() + interval '1 hour')`,
		"tok-"+testutil.RandomHex(8), email, e.clubID,
	); err != nil {
		t.Fatalf("seed magic link: %v", err)
	}

	if _, err := e.db.Exec(ctx,
		`UPDATE users SET payables_token = $2 WHERE id = $1`, userID, "pay-"+testutil.RandomHex(8),
	); err != nil {
		t.Fatalf("seed pay link: %v", err)
	}
	if _, err := e.db.Exec(ctx,
		`INSERT INTO developer_tokens (club_id, name, token_prefix, token_hash, created_by, expires_at)
		 VALUES ($1, 'test token', 'bdt_test', $2, $3, now() + interval '1 day')`,
		e.clubID, "hash-"+testutil.RandomHex(8), userID,
	); err != nil {
		t.Fatalf("seed developer token: %v", err)
	}

	if withRecords {
		if _, err := e.db.Exec(ctx,
			`INSERT INTO invoices (club_id, user_id, kid_number, total_amount, status, issue_date, due_date)
			 VALUES ($1, $2, $3, 450, 'open', CURRENT_DATE, CURRENT_DATE)`,
			e.clubID, userID, "99"+testutil.RandomHex(5),
		); err != nil {
			t.Fatalf("seed invoice: %v", err)
		}
	}

	if err := e.db.QueryRow(ctx,
		`INSERT INTO deletion_requests (user_id, club_id, grace_end)
		 VALUES ($1, $2, now() - interval '1 day') RETURNING id`,
		userID, e.clubID,
	).Scan(&requestID); err != nil {
		t.Fatalf("seed deletion request: %v", err)
	}
	return userID, email, sessionID, requestID
}

func gdprCount(t *testing.T, db *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// Both outcomes (identity erased now, or deferred behind retention) must leave
// the member with no way to authenticate: the account is deactivated, the
// session no longer validates, and passkeys, TOTP, recovery codes and unused
// magic links are gone.
func TestIntegration_GDPRProcessDeletionRevokesAccess(t *testing.T) {
	for _, tc := range []struct {
		name        string
		withRecords bool
		erased      bool
	}{
		{"identity erased immediately", false, true},
		{"identity deferred behind retention", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newGDPRProcessEnv(t)
			ctx := context.Background()
			userID, email, sessionID, requestID := e.seedMember(t, tc.withRecords)
			var payToken string
			if err := e.db.QueryRow(ctx, `SELECT payables_token FROM users WHERE id = $1`, userID).Scan(&payToken); err != nil {
				t.Fatalf("read pay token: %v", err)
			}

			rec := e.process(requestID)
			assertStatus(t, rec, http.StatusOK)

			if _, _, err := auth.NewSessionService(e.db).ValidateSession(ctx, sessionID); !errors.Is(err, auth.ErrSessionNotFound) {
				t.Errorf("ValidateSession after processing = %v, want ErrSessionNotFound", err)
			}
			if n := gdprCount(t, e.db, `SELECT count(*) FROM sessions WHERE user_id = $1`, userID); n != 0 {
				t.Errorf("sessions left = %d, want 0", n)
			}
			if n := gdprCount(t, e.db,
				`SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NOT NULL AND deactivated_by = $2`,
				userID, e.admin); n != 1 {
				t.Errorf("user not deactivated by the processing admin")
			}
			if n := gdprCount(t, e.db, `SELECT count(*) FROM webauthn_credentials WHERE user_id = $1`, userID); n != 0 {
				t.Errorf("passkeys left = %d, want 0", n)
			}
			if n := gdprCount(t, e.db, `SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, userID); n != 0 {
				t.Errorf("recovery codes left = %d, want 0", n)
			}
			if n := gdprCount(t, e.db,
				`SELECT count(*) FROM users WHERE id = $1 AND totp_enabled = false AND totp_secret_encrypted IS NULL`,
				userID); n != 1 {
				t.Errorf("TOTP enrolment still present")
			}
			if n := gdprCount(t, e.db, `SELECT count(*) FROM magic_links WHERE club_id = $1 AND email = $2`, e.clubID, email); n != 0 {
				t.Errorf("magic links left = %d, want 0", n)
			}

			// The member cannot request a new login link: the login lookup
			// skips deactivated accounts, so a deferred member (whose email is
			// kept) gets none, and an erased member's old email no longer exists.
			if n := gdprCount(t, e.db,
				`SELECT count(*) FROM users WHERE email = $1 AND club_id = $2 AND deactivated_at IS NULL`,
				email, e.clubID); n != 0 {
				t.Errorf("member is still resolvable as an active login")
			}

			if n := gdprCount(t, e.db, `SELECT count(*) FROM users WHERE id = $1 AND payables_token IS NULL`, userID); n != 1 {
				t.Errorf("pay link token still set")
			}
			if n := gdprCount(t, e.db,
				`SELECT count(*) FROM developer_tokens WHERE created_by = $1 AND revoked_at IS NULL`, userID); n != 0 {
				t.Errorf("developer tokens left active = %d, want 0", n)
			}

			// The public pay page no longer resolves the old token, and staff
			// cannot mint a new link for the deactivated member.
			inv := &InvoiceHandler{
				db:        e.db,
				config:    &config.Config{FrontendURL: "https://example.org"},
				providers: payments.NewRegistry(vipps.NewPaymentProvider(vipps.New("", "", "", "", "", zerolog.Nop()))),
				log:       zerolog.Nop(),
			}
			recPay := httptest.NewRecorder()
			inv.HandleGetPayables(recPay, payablesReq(payToken))
			if recPay.Code != http.StatusNotFound {
				t.Errorf("old pay link status = %d, want 404", recPay.Code)
			}
			recMint := httptest.NewRecorder()
			inv.HandleGetUserPayLink(recMint, userPayLinkReq(userID, e.clubID))
			if recMint.Code != http.StatusNotFound {
				t.Errorf("minting a pay link for the member status = %d, want 404", recMint.Code)
			}

			var erasedEmail bool
			if err := e.db.QueryRow(ctx,
				`SELECT email <> $2 FROM users WHERE id = $1`, userID, email).Scan(&erasedEmail); err != nil {
				t.Fatalf("read email: %v", err)
			}
			if erasedEmail != tc.erased {
				t.Errorf("identity erased = %v, want %v", erasedEmail, tc.erased)
			}

			var ip, action string
			var revoked int
			if err := e.db.QueryRow(ctx,
				`SELECT actor_ip, action, (details->>'sessions_revoked')::int
				   FROM audit_log WHERE club_id = $1 AND resource_id = $2`,
				e.clubID, userID).Scan(&ip, &action, &revoked); err != nil {
				t.Fatalf("read audit row: %v", err)
			}
			if action != "gdpr.deletion_processed" || ip != "203.0.113.7" || revoked != 1 {
				t.Errorf("audit = (%q, %q, %d), want (gdpr.deletion_processed, 203.0.113.7, 1)", action, ip, revoked)
			}
		})
	}
}

// An erased member must not be picked up again by the retention sweep, which
// would rewrite the email to the shared sentinel.
func TestIntegration_GDPRErasedMemberSkippedByPurgeSweep(t *testing.T) {
	t.Parallel()
	e := newGDPRProcessEnv(t)
	_, _, _, requestID := e.seedMember(t, false)
	assertStatus(t, e.process(requestID), http.StatusOK)

	svc := accounting.NewService(e.db, nil, zerolog.Nop())
	purged, err := svc.PurgeExpiredMemberPII(context.Background())
	if err != nil {
		t.Fatalf("PurgeExpiredMemberPII: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0 for an already-erased member", purged)
	}
}

// Two deferred erasures in one club must both complete once retention clears:
// each gets its own address, so the sweep never collides on UNIQUE (club_id, email).
func TestIntegration_GDPRDeferredErasuresCompleteViaPurgeSweep(t *testing.T) {
	t.Parallel()
	e := newGDPRProcessEnv(t)
	ctx := context.Background()

	var userIDs []string
	for range 2 {
		userID, _, _, requestID := e.seedMember(t, true)
		assertStatus(t, e.process(requestID), http.StatusOK)
		userIDs = append(userIDs, userID)
	}

	svc := accounting.NewService(e.db, nil, zerolog.Nop())
	if purged, err := svc.PurgeExpiredMemberPII(ctx); err != nil || purged != 0 {
		t.Fatalf("sweep inside retention = (%d, %v), want (0, nil)", purged, err)
	}

	if _, err := e.db.Exec(ctx,
		`UPDATE invoices SET issue_date = CURRENT_DATE - interval '8 years' WHERE club_id = $1`, e.clubID); err != nil {
		t.Fatalf("age invoices: %v", err)
	}
	purged, err := svc.PurgeExpiredMemberPII(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredMemberPII: %v", err)
	}
	if purged != 2 {
		t.Fatalf("purged = %d, want 2", purged)
	}
	if n := gdprCount(t, e.db,
		`SELECT count(DISTINCT email) FROM users WHERE id = ANY($1) AND email LIKE '%'||$2`,
		userIDs, accounting.ErasedEmailSuffix); n != 2 {
		t.Errorf("distinct erased addresses = %d, want 2", n)
	}
}

func TestIntegration_GDPRProcessDeletionRefusesSecondRun(t *testing.T) {
	t.Parallel()
	e := newGDPRProcessEnv(t)
	userID, _, _, requestID := e.seedMember(t, false)

	assertStatus(t, e.process(requestID), http.StatusOK)
	assertStatus(t, e.process(requestID), http.StatusBadRequest)

	if n := gdprCount(t, e.db, `SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = 'gdpr.deletion_processed'`, userID); n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
}

// Two admins processing the same request at once serialise on the row lock:
// exactly one succeeds and the erasure is audited once.
func TestIntegration_GDPRProcessDeletionConcurrent(t *testing.T) {
	t.Parallel()
	// Two connections: if the handler asked for a second one while holding
	// the row lock, the waiting runs would exhaust the pool and hang.
	e := newGDPRProcessEnv(t, testutil.WithMaxConns(2))
	userID, _, _, requestID := e.seedMember(t, false)

	const runs = 4
	codes := make([]int, runs)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.process(requestID).Code
		}()
	}
	wg.Wait()

	ok := 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusBadRequest:
		default:
			t.Errorf("unexpected status %d in %v", c, codes)
		}
	}
	if ok != 1 {
		t.Errorf("successful runs = %d (%v), want exactly 1", ok, codes)
	}
	if n := gdprCount(t, e.db, `SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = 'gdpr.deletion_processed'`, userID); n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
}

// An admin cannot process their own deletion request: it would revoke the
// session they are acting from.
func TestIntegration_GDPRProcessDeletionRefusesOwnRequest(t *testing.T) {
	t.Parallel()
	e := newGDPRProcessEnv(t)
	ctx := context.Background()

	var requestID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO deletion_requests (user_id, club_id, grace_end)
		 VALUES ($1, $2, now() - interval '1 day') RETURNING id`,
		e.admin, e.clubID,
	).Scan(&requestID); err != nil {
		t.Fatalf("seed deletion request: %v", err)
	}

	assertStatus(t, e.process(requestID), http.StatusBadRequest)

	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM deletion_requests WHERE id = $1 AND status = 'pending'`, requestID); n != 1 {
		t.Errorf("own request should stay pending")
	}
	if n := gdprCount(t, e.db,
		`SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NULL`, e.admin); n != 1 {
		t.Errorf("admin must not be deactivated")
	}
}

// uuid.Parse accepts forms Postgres does not (urn:uuid:, braces); those must
// answer 404 like any unknown id, never 500.
func TestIntegration_GDPRProcessDeletionAlternateUUIDForms(t *testing.T) {
	t.Parallel()
	e := newGDPRProcessEnv(t)
	const unknown = "00000000-0000-4000-8000-000000000001"
	for _, id := range []string{"urn:uuid:" + unknown, "{" + unknown + "}"} {
		assertStatus(t, e.process(id), http.StatusNotFound)
	}
}
