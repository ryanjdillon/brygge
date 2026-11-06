package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

type cancelEnv struct {
	db     *pgxpool.Pool
	clubID string
	router *chi.Mux
	kicks  int
}

func newCancelEnv(t *testing.T) *cancelEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	e := &cancelEnv{db: db, clubID: testutil.SeedClub(t, db)}
	h := NewMembersHandler(db, testConfig(), zerolog.Nop())
	h.SetDeliveryKick(func() { e.kicks++ })
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Get("/members/me/membership", h.HandleGetMyMembership)
	r.Post("/members/me/membership/cancel", h.HandleCancelMyMembership)
	r.Post("/members/me/membership/undo", h.HandleUndoCancelMyMembership)
	e.router = r
	return e
}

func (e *cancelEnv) call(t *testing.T, userID, clubID string, roles []string, method, path, body string) (*httptest.ResponseRecorder, myMembership) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", authHeader(generateTestToken(userID, clubID, roles)))
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	var m myMembership
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode membership: %v", err)
		}
	}
	return rec, m
}

// daysAgo is the server-local date n days before today, as the code under
// test computes it, so a test never mixes the database's CURRENT_DATE with
// Go's local date.
func daysAgo(n int) string {
	return time.Now().In(time.Local).AddDate(0, 0, -n).Format(time.DateOnly)
}

// deliveryKinds lists the user's queued deliveries, sorted.
func deliveryKinds(t *testing.T, db *pgxpool.Pool, userID string) []string {
	t.Helper()
	var kinds []string
	if err := db.QueryRow(context.Background(),
		`SELECT COALESCE(array_agg(kind ORDER BY kind), '{}') FROM deliveries WHERE user_id = $1`, userID,
	).Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	return kinds
}

func auditCount(t *testing.T, db *pgxpool.Pool, userID, action string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = $2`, userID, action,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Cancelling sets the year-end date, queues the receipt and the board's
// notice once, and a repeat keeps the first call's date and reason.
func TestCancelMembershipSetsYearEndOnce(t *testing.T) {
	e := newCancelEnv(t)
	userID, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	roles := []string{"member"}

	rec, m := e.call(t, userID, e.clubID, roles, http.MethodPost, "/members/me/membership/cancel", `{"reason":"  Selt båten  "}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel = %d %s", rec.Code, rec.Body)
	}
	wantEnd := time.Now().In(time.Local).Format("2006") + "-12-31"
	if m.EndsOn == nil || *m.EndsOn != wantEnd || m.CancelledAt == nil || !m.CanUndo {
		t.Fatalf("after cancel = %+v, want ends_on %s and undoable", m, wantEnd)
	}
	first := *m.CancelledAt

	rec, m = e.call(t, userID, e.clubID, roles, http.MethodPost, "/members/me/membership/cancel", `{"reason":"something else"}`)
	if rec.Code != http.StatusOK || m.CancelledAt == nil || !m.CancelledAt.Equal(first) {
		t.Fatalf("repeat cancel = %d %+v, want the first cancellation unchanged", rec.Code, m)
	}

	var reason string
	if err := e.db.QueryRow(context.Background(),
		`SELECT membership_cancel_reason FROM users WHERE id = $1`, userID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "Selt båten" {
		t.Errorf("reason = %q, want the first call's, trimmed", reason)
	}
	want := []string{bcast.KindMembershipCancelBoardNotice, bcast.KindMembershipCancelReceipt}
	if got := deliveryKinds(t, e.db, userID); !slices.Equal(got, want) {
		t.Errorf("queued = %v, want %v", got, want)
	}
	if n := auditCount(t, e.db, userID, "membership.cancelled"); n != 1 {
		t.Errorf("membership.cancelled rows = %d, want 1", n)
	}
	if e.kicks != 1 {
		t.Errorf("delivery kicks = %d, want 1", e.kicks)
	}
	// The reason is the member's text and stays out of the audit log.
	var details string
	if err := e.db.QueryRow(context.Background(),
		`SELECT details::text FROM audit_log WHERE resource_id = $1 AND action = 'membership.cancelled'`, userID,
	).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, "Selt") {
		t.Errorf("audit details carry the reason: %s", details)
	}
}

// Undo clears the cancellation and tells the board while the end date is
// ahead, is a no-op without a cancellation, and is refused once it passed.
func TestUndoMembershipCancellation(t *testing.T) {
	e := newCancelEnv(t)
	ctx := context.Background()
	userID, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	roles := []string{"member"}

	if rec, m := e.call(t, userID, e.clubID, roles, http.MethodPost, "/members/me/membership/undo", ""); rec.Code != http.StatusOK || m.CancelledAt != nil {
		t.Fatalf("undo without cancellation = %d %+v, want 200 and nothing", rec.Code, m)
	}
	if got := deliveryKinds(t, e.db, userID); len(got) != 0 {
		t.Fatalf("undo without cancellation queued %v", got)
	}

	e.call(t, userID, e.clubID, roles, http.MethodPost, "/members/me/membership/cancel", "")
	rec, m := e.call(t, userID, e.clubID, roles, http.MethodPost, "/members/me/membership/undo", "")
	if rec.Code != http.StatusOK || m.CancelledAt != nil || m.EndsOn != nil {
		t.Fatalf("undo = %d %+v, want cleared", rec.Code, m)
	}
	if n := auditCount(t, e.db, userID, "membership.cancel_undone"); n != 1 {
		t.Errorf("membership.cancel_undone rows = %d, want 1", n)
	}
	want := []string{bcast.KindMembershipCancelBoardNotice, bcast.KindMembershipCancelReceipt, bcast.KindMembershipCancelUndone}
	if got := deliveryKinds(t, e.db, userID); !slices.Equal(got, want) {
		t.Errorf("queued = %v, want %v", got, want)
	}

	// Cancelling again is a new cancellation with its own notices.
	e.call(t, userID, e.clubID, roles, http.MethodPost, "/members/me/membership/cancel", "")
	if got := deliveryKinds(t, e.db, userID); len(got) != 5 {
		t.Errorf("after a second cancellation queued = %v, want 5 rows", got)
	}

	// The end date has passed but the member is still active (the job could
	// not deactivate them, e.g. the last admin): undo still works.
	mustExec(t, e.db, ctx, `UPDATE users SET membership_ends_on = $2::date WHERE id = $1`, userID, daysAgo(1))
	rec, m = e.call(t, userID, e.clubID, roles, http.MethodPost, "/members/me/membership/undo", "")
	if rec.Code != http.StatusOK || m.CancelledAt != nil {
		t.Fatalf("undo past the end while active = %d %+v, want cleared", rec.Code, m)
	}

	// Once the membership has ended (deactivated), the cancellation stands.
	e.call(t, userID, e.clubID, roles, http.MethodPost, "/members/me/membership/cancel", "")
	mustExec(t, e.db, ctx, `UPDATE users SET membership_ends_on = $2::date, deactivated_at = now() WHERE id = $1`, userID, daysAgo(1))
	rec, _ = e.call(t, userID, e.clubID, roles, http.MethodPost, "/members/me/membership/undo", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), codeMembershipEnded) {
		t.Fatalf("undo after the end = %d %s, want 409 %s", rec.Code, rec.Body, codeMembershipEnded)
	}
	var endsOn *time.Time
	if err := e.db.QueryRow(ctx, `SELECT membership_ends_on FROM users WHERE id = $1`, userID).Scan(&endsOn); err != nil || endsOn == nil {
		t.Fatalf("ends_on cleared by a refused undo: %v %v", endsOn, err)
	}
}

// Who may cancel: an applicant-only session never; a board member who holds
// no member role may. A session for another club finds no one.
func TestCancelMembershipWhoAndClubScope(t *testing.T) {
	e := newCancelEnv(t)
	applicant, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"applicant"})
	if rec, _ := e.call(t, applicant, e.clubID, []string{"applicant"}, http.MethodPost, "/members/me/membership/cancel", ""); rec.Code != http.StatusForbidden {
		t.Errorf("applicant cancel = %d, want 403", rec.Code)
	}

	secretary, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"secretary"})
	rec, m := e.call(t, secretary, e.clubID, []string{"secretary"}, http.MethodPost, "/members/me/membership/cancel", "")
	if rec.Code != http.StatusOK || m.EndsOn == nil {
		t.Fatalf("board cancel = %d %+v, want cancelled", rec.Code, m)
	}
	if !slices.Equal(m.BoardRoles, []string{"secretary"}) {
		t.Errorf("board_roles = %v, want [secretary]", m.BoardRoles)
	}

	other := testutil.SeedClub(t, e.db)
	member, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	if rec, _ := e.call(t, member, other, []string{"member"}, http.MethodPost, "/members/me/membership/cancel", ""); rec.Code != http.StatusNotFound {
		t.Errorf("cancel under another club = %d, want 404", rec.Code)
	}
	var cancelled bool
	if err := e.db.QueryRow(context.Background(),
		`SELECT membership_cancelled_at IS NOT NULL FROM users WHERE id = $1`, member).Scan(&cancelled); err != nil || cancelled {
		t.Errorf("another club's session cancelled the member: %v %v", cancelled, err)
	}
}

// The GET feeds the page's notices: open fakturas, a permanent slip, a
// harbor share, the waiting list and board roles.
func TestGetMyMembershipNotices(t *testing.T) {
	e := newCancelEnv(t)
	ctx := context.Background()
	userID, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member", "treasurer"})
	mustExec(t, e.db, ctx, `UPDATE users SET harbor_share_owner = true, member_since = '2020-05-01' WHERE id = $1`, userID)
	var slipID string
	if err := e.db.QueryRow(ctx, `INSERT INTO slips (club_id, number) VALUES ($1, 'Q9') RETURNING id`, e.clubID).Scan(&slipID); err != nil {
		t.Fatal(err)
	}
	var boatID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO boats (user_id, club_id, name) VALUES ($1, $2, 'Synthetic') RETURNING id`, userID, e.clubID,
	).Scan(&boatID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.db, ctx,
		`INSERT INTO slip_assignments (slip_id, user_id, club_id, boat_id, assignment_type) VALUES ($1, $2, $3, $4, 'permanent')`,
		slipID, userID, e.clubID, boatID)
	mustExec(t, e.db, ctx,
		`INSERT INTO invoices (club_id, user_id, kid_number, total_amount, status, issue_date, due_date, sent_at)
		 VALUES ($1, $2, $3, 450, 'open', $4::date, $4::date, now())`,
		e.clubID, userID, "98"+testutil.RandomHex(5), daysAgo(0))
	mustExec(t, e.db, ctx,
		`INSERT INTO waiting_list_entries (user_id, club_id, position, status) VALUES ($1, $2, 3, 'active')`,
		userID, e.clubID)

	rec, m := e.call(t, userID, e.clubID, []string{"member", "treasurer"}, http.MethodGet, "/members/me/membership", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if m.OpenInvoices != 1 || !m.HoldsSlip || !m.HarborShareOwner || !m.OnWaitingList ||
		!slices.Equal(m.BoardRoles, []string{"treasurer"}) || m.MemberSince == nil || *m.MemberSince != "2020-05-01" ||
		m.CancelledAt != nil || m.CancelEndsOn == "" {
		t.Errorf("membership = %+v", m)
	}
}

// seedWaiting puts the user on the waiting list with the given status.
func seedWaiting(t *testing.T, db *pgxpool.Pool, clubID, userID, status string, position int) string {
	t.Helper()
	var id string
	if err := db.QueryRow(context.Background(),
		`INSERT INTO waiting_list_entries (user_id, club_id, position, status, offer_deadline)
		 VALUES ($1, $2, $3, $4::waiting_list_status, CASE WHEN $4::text = 'offered' THEN now() + interval '7 days' END) RETURNING id`,
		userID, clubID, position, status,
	).Scan(&id); err != nil {
		t.Fatalf("seed waiting-list entry: %v", err)
	}
	return id
}

func waitingStatuses(t *testing.T, db *pgxpool.Pool, userID string) []string {
	t.Helper()
	var s []string
	if err := db.QueryRow(context.Background(),
		`SELECT COALESCE(array_agg(status ORDER BY status), '{}') FROM waiting_list_entries WHERE user_id = $1`, userID,
	).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func systemActorFor(db *pgxpool.Pool) func(context.Context, string) (string, error) {
	return accounting.NewService(db, nil, zerolog.Nop()).SystemActor
}

// The daily job ends a cancellation the day after its end date: the member
// is deactivated as the system actor and leaves the waiting list, active and
// offered entries alike. The end date itself is still a member's day, and a
// second run changes nothing.
func TestEndCancelledMemberships(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, db)
	testutil.SeedUser(t, db, clubID, []string{"admin"})
	ended, _ := testutil.SeedUser(t, db, clubID, []string{"member"})
	lastDay, _ := testutil.SeedUser(t, db, clubID, []string{"member"})
	seedWaiting(t, db, clubID, ended, "active", 1)
	seedWaiting(t, db, clubID, ended, "offered", 2)
	seedWaiting(t, db, clubID, lastDay, "active", 3)
	mustExec(t, db, ctx, `INSERT INTO sessions (id, user_id, club_id, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')`,
		"end-"+testutil.RandomHex(6), ended, clubID)
	now := time.Now()
	yesterday := now.In(time.Local).AddDate(0, 0, -1).Format(time.DateOnly)
	mustExec(t, db, ctx, `UPDATE users SET membership_cancelled_at = now() - interval '1 year', membership_ends_on = $2 WHERE id = $1`, ended, yesterday)
	mustExec(t, db, ctx, `UPDATE users SET membership_cancelled_at = now(), membership_ends_on = $2 WHERE id = $1`, lastDay, localToday(now))

	// Another club's cancelled member ends under that club's system actor.
	otherClub := testutil.SeedClub(t, db)
	otherAdmin, _ := testutil.SeedUser(t, db, otherClub, []string{"admin"})
	otherMember, _ := testutil.SeedUser(t, db, otherClub, []string{"member"})
	mustExec(t, db, ctx, `UPDATE users SET membership_cancelled_at = now(), membership_ends_on = $2 WHERE id = $1`, otherMember, yesterday)
	_ = otherAdmin

	sysActor := systemActorFor(db)
	res, err := EndCancelledMemberships(ctx, db, sysActor, now)
	if err != nil {
		t.Fatalf("EndCancelledMemberships: %v", err)
	}
	if res.Ended != 2 || res.Withdrawn != 2 || len(res.Refused) != 0 {
		t.Fatalf("result = %+v, want 2 ended, 2 withdrawn", res)
	}

	for _, c := range []struct {
		user, club string
	}{{ended, clubID}, {otherMember, otherClub}} {
		actor, err := sysActor(ctx, c.club)
		if err != nil {
			t.Fatal(err)
		}
		var by *string
		var sessions int
		if err := db.QueryRow(ctx,
			`SELECT deactivated_by::text, (SELECT count(*) FROM sessions WHERE user_id = u.id) FROM users u WHERE id = $1`, c.user,
		).Scan(&by, &sessions); err != nil {
			t.Fatal(err)
		}
		if by == nil || *by != actor || sessions != 0 {
			t.Errorf("member %s: deactivated_by = %v (want %s), sessions = %d", c.user, by, actor, sessions)
		}
		if n := auditCount(t, db, c.user, "membership.ended"); n != 1 {
			t.Errorf("member %s: membership.ended rows = %d, want 1", c.user, n)
		}
	}
	if got := waitingStatuses(t, db, ended); !slices.Equal(got, []string{"withdrawn", "withdrawn"}) {
		t.Errorf("ended member's entries = %v, want both withdrawn", got)
	}
	var lastDayActive bool
	if err := db.QueryRow(ctx, `SELECT deactivated_at IS NULL FROM users WHERE id = $1`, lastDay).Scan(&lastDayActive); err != nil || !lastDayActive {
		t.Errorf("member on their last day deactivated: %v", err)
	}
	if got := waitingStatuses(t, db, lastDay); !slices.Equal(got, []string{"active"}) {
		t.Errorf("last-day member's entry = %v, want active", got)
	}

	again, err := EndCancelledMemberships(ctx, db, sysActor, now)
	if err != nil || again.Ended != 0 || again.Withdrawn != 0 {
		t.Errorf("second run = %+v, %v; want nothing", again, err)
	}
	if n := auditCount(t, db, ended, "membership.ended"); n != 1 {
		t.Errorf("membership.ended rows after a second run = %d, want 1", n)
	}
}

// The club's only active admin is not deactivated: nothing is withdrawn, and
// the board is told once, however many runs see it.
func TestEndCancelledMembershipRefusesLastAdmin(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, db)
	adminID, _ := testutil.SeedUser(t, db, clubID, []string{"admin", "member"})
	seedWaiting(t, db, clubID, adminID, "active", 1)
	mustExec(t, db, ctx, `UPDATE users SET membership_cancelled_at = now(), membership_ends_on = $2::date WHERE id = $1`, adminID, daysAgo(2))

	for range 2 {
		res, err := EndCancelledMemberships(ctx, db, systemActorFor(db), time.Now())
		if err != nil {
			t.Fatalf("EndCancelledMemberships: %v", err)
		}
		if res.Ended != 0 || !slices.Equal(res.Refused, []string{adminID}) {
			t.Fatalf("result = %+v, want the admin refused", res)
		}
	}
	var active bool
	if err := db.QueryRow(ctx, `SELECT deactivated_at IS NULL FROM users WHERE id = $1`, adminID).Scan(&active); err != nil || !active {
		t.Errorf("last admin deactivated: %v", err)
	}
	if got := waitingStatuses(t, db, adminID); !slices.Equal(got, []string{"active"}) {
		t.Errorf("entries = %v, want untouched", got)
	}
	if got := deliveryKinds(t, db, adminID); !slices.Equal(got, []string{bcast.KindMembershipEndRefused}) {
		t.Errorf("queued = %v, want one end-refused notice", got)
	}
	if n := auditCount(t, db, adminID, "membership.ended"); n != 0 {
		t.Errorf("membership.ended rows = %d, want 0", n)
	}
}

// DeactivateMemberTx does what the board's button always did: deactivated_at
// and deactivated_by, a user.deactivated row with empty details, sessions
// dropped, developer tokens revoked; and refuses the last admin and a member
// already deactivated or in another club.
func TestDeactivateMemberTx(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, db)
	adminID, _ := testutil.SeedUser(t, db, clubID, []string{"admin"})
	memberID, _ := testutil.SeedUser(t, db, clubID, []string{"member", "admin"})
	mustExec(t, db, ctx, `INSERT INTO sessions (id, user_id, club_id, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')`,
		"svc-"+testutil.RandomHex(6), memberID, clubID)
	mustExec(t, db, ctx,
		`INSERT INTO developer_tokens (club_id, name, token_prefix, token_hash, created_by, expires_at)
		 VALUES ($1, 'test token', 'bdt_test', $2, $3, now() + interval '1 day')`,
		clubID, "hash-"+testutil.RandomHex(8), memberID)

	run := func(userID, clubID string) error {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if err := DeactivateMemberTx(ctx, tx, clubID, userID, adminID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	if err := run(memberID, testutil.SeedClub(t, db)); !errors.Is(err, errNotDeactivatable) {
		t.Errorf("another club = %v, want errNotDeactivatable", err)
	}
	if err := run(memberID, clubID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	var (
		by                string
		sessions, revoked int
		details           string
	)
	if err := db.QueryRow(ctx,
		`SELECT u.deactivated_by::text,
		        (SELECT count(*) FROM sessions WHERE user_id = u.id),
		        (SELECT count(*) FROM developer_tokens WHERE created_by = u.id AND revoked_at IS NOT NULL),
		        (SELECT details::text FROM audit_log WHERE resource_id = u.id::text AND action = 'user.deactivated' AND actor_id = $2)
		   FROM users u WHERE u.id = $1`, memberID, adminID,
	).Scan(&by, &sessions, &revoked, &details); err != nil {
		t.Fatal(err)
	}
	if by != adminID || sessions != 0 || revoked != 1 || details != "{}" {
		t.Errorf("by=%s sessions=%d revoked=%d details=%s", by, sessions, revoked, details)
	}
	if err := run(memberID, clubID); !errors.Is(err, errNotDeactivatable) {
		t.Errorf("again = %v, want errNotDeactivatable", err)
	}
	// The member above was also an admin; now adminID is the only one left.
	if err := run(adminID, clubID); !errors.Is(err, errLastAdmin) {
		t.Errorf("last admin = %v, want errLastAdmin", err)
	}
}

// WithdrawWaitingListTx withdraws active and offered entries, leaves other
// statuses and other clubs alone, and audits each as the actor.
func TestWithdrawWaitingListTx(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"member"})
	seedWaiting(t, db, clubID, userID, "active", 1)
	seedWaiting(t, db, clubID, userID, "offered", 2)
	seedWaiting(t, db, clubID, userID, "accepted", 3)

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := WithdrawWaitingListTx(ctx, tx, testutil.SeedClub(t, db), userID, userID); err != nil || len(got) != 0 {
		t.Errorf("another club withdrew %d, %v", len(got), err)
	}
	got, err := WithdrawWaitingListTx(ctx, tx, clubID, userID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var old []string
	for _, w := range got {
		old = append(old, w.oldStatus)
		if w.entry.Status != "withdrawn" {
			t.Errorf("returned entry status = %s", w.entry.Status)
		}
	}
	slices.Sort(old)
	if !slices.Equal(old, []string{"active", "offered"}) {
		t.Errorf("withdrew %v, want active and offered", old)
	}
	if s := waitingStatuses(t, db, userID); !slices.Equal(s, []string{"accepted", "withdrawn", "withdrawn"}) {
		t.Errorf("statuses = %v", s)
	}
	var audits int
	if err := db.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'withdraw' AND resource = 'waiting_list_entry' AND actor_id = $1`, userID,
	).Scan(&audits); err != nil || audits != 2 {
		t.Errorf("withdraw audit rows = %d, %v; want 2", audits, err)
	}
}

// Reactivating a member whose cancellation ended clears it, so the daily
// job does not deactivate them again.
func TestReactivateClearsEndedCancellation(t *testing.T) {
	e := newGuardEnv(t)
	ctx := context.Background()
	memberID, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	mustExec(t, e.db, ctx,
		`UPDATE users SET membership_cancelled_at = now(), membership_ends_on = $2::date,
		        membership_cancel_reason = 'x', deactivated_at = now() WHERE id = $1`, memberID, daysAgo(1))
	if rec := e.call(e.admin, []string{"admin"}, http.MethodPost, "/admin/users/"+memberID+"/reactivate", ""); rec.Code != http.StatusOK {
		t.Fatalf("reactivate = %d %s", rec.Code, rec.Body)
	}
	var cleared bool
	if err := e.db.QueryRow(ctx,
		`SELECT membership_cancelled_at IS NULL AND membership_ends_on IS NULL AND membership_cancel_reason IS NULL
		   FROM users WHERE id = $1`, memberID).Scan(&cleared); err != nil || !cleared {
		t.Errorf("ended cancellation kept after reactivation: %v", err)
	}
}

// The cancellation emails render from the queue: the member's receipt in the
// club's language, stating the end date and no refund, and the board's
// notices with the member's name and the end date.
func TestMembershipCancelNoticesSend(t *testing.T) {
	e := newCancelEnv(t)
	ctx := context.Background()
	mustExec(t, e.db, ctx, `UPDATE clubs SET board_notification_email = 'styret@club.example.test', default_language = 'nn' WHERE id = $1`, e.clubID)
	userID, addr := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	e.call(t, userID, e.clubID, []string{"member"}, http.MethodPost, "/members/me/membership/cancel", `{"reason":"Flyttar"}`)
	e.call(t, userID, e.clubID, []string{"member"}, http.MethodPost, "/members/me/membership/undo", "")

	mailer := &flakyMailer{}
	cfg := &config.Config{ClubName: "Testklubben", Domain: "club.example.test", FrontendURL: "https://club.example.test"}
	w := bcast.NewWorker(bcast.NewStore(e.db), 0, zerolog.Nop())
	sender := NewMembershipNoticeSender(e.db, cfg, mailer, zerolog.Nop())
	for _, k := range MembershipNoticeKinds {
		w.RegisterSender(k, sender)
	}
	runWorkerUntilDrained(t, e.db, w)

	sent, _ := mailer.snapshot()
	if len(sent) != 3 {
		t.Fatalf("sent %d emails, want 3", len(sent))
	}
	year := time.Now().In(time.Local).Format("2006")
	var receipt, board, undone *email.MockCall
	for i := range sent {
		switch c := &sent[i]; {
		case c.To == addr:
			receipt = c
		case strings.HasPrefix(c.Subject, "Utmelding angra"):
			undone = c
		case strings.HasPrefix(c.Subject, "Utmelding:"):
			board = c
		}
	}
	if receipt == nil || receipt.Subject != "Utmelding stadfesta" ||
		!strings.Contains(receipt.HTML, "31.12."+year) || !strings.Contains(receipt.HTML, "ikkje refundert") {
		t.Errorf("receipt = %+v", receipt)
	}
	if board == nil || board.To != "styret@club.example.test" || board.Subject != "Utmelding: Test User (til og med 31.12)" {
		t.Errorf("board notice = %+v", board)
	}
	if undone == nil || undone.To != "styret@club.example.test" {
		t.Errorf("undo notice = %+v", undone)
	}
	// The member was on no waiting list, so the receipt does not mention one.
	if receipt != nil && strings.Contains(receipt.HTML, "ventelista") {
		t.Error("receipt warns about a waiting list the member is not on")
	}
}

// The job's list is read without locks; a member whose cancellation was
// undone, or who was deactivated (and perhaps reactivated, which clears an
// ended cancellation) since, is left alone under the row lock.
func TestEndMembershipRechecksUnderLock(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, db)
	testutil.SeedUser(t, db, clubID, []string{"admin"})
	undone, _ := testutil.SeedUser(t, db, clubID, []string{"member"})
	deactivated, _ := testutil.SeedUser(t, db, clubID, []string{"member"})
	seedWaiting(t, db, clubID, undone, "active", 1)
	actor, err := systemActorFor(db)(ctx, clubID)
	if err != nil {
		t.Fatal(err)
	}
	today := daysAgo(0)

	// As read by the list: both ended yesterday. Then, before the member's
	// transaction, one undoes and the board deactivates the other.
	mustExec(t, db, ctx, `UPDATE users SET membership_cancelled_at = NULL, membership_ends_on = NULL WHERE id = $1`, undone)
	mustExec(t, db, ctx, `UPDATE users SET membership_cancelled_at = now(), membership_ends_on = $2::date, deactivated_at = now() WHERE id = $1`,
		deactivated, daysAgo(1))

	if _, err := endMembership(ctx, db, clubID, undone, daysAgo(1), actor, today); !errors.Is(err, errMembershipNotDue) {
		t.Errorf("undone member: %v, want errMembershipNotDue", err)
	}
	if _, err := endMembership(ctx, db, clubID, deactivated, daysAgo(1), actor, today); !errors.Is(err, errMembershipNotDue) {
		t.Errorf("deactivated member: %v, want errMembershipNotDue", err)
	}
	var active bool
	if err := db.QueryRow(ctx, `SELECT deactivated_at IS NULL FROM users WHERE id = $1`, undone).Scan(&active); err != nil || !active {
		t.Errorf("undone member deactivated: %v", err)
	}
	if got := waitingStatuses(t, db, undone); !slices.Equal(got, []string{"active"}) {
		t.Errorf("undone member's entry = %v, want active", got)
	}
	if n := auditCount(t, db, undone, "membership.ended") + auditCount(t, db, deactivated, "membership.ended"); n != 0 {
		t.Errorf("membership.ended rows = %d, want 0", n)
	}
}

// The reason is capped at 2000 characters, counted as runes.
func TestCancelMembershipReasonCap(t *testing.T) {
	e := newCancelEnv(t)
	userID, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	body := func(n int) string {
		b, _ := json.Marshal(map[string]string{"reason": strings.Repeat("ø", n)})
		return string(b)
	}
	if rec, _ := e.call(t, userID, e.clubID, []string{"member"}, http.MethodPost, "/members/me/membership/cancel", body(maxCancelReasonRunes+1)); rec.Code != http.StatusBadRequest {
		t.Errorf("2001 runes = %d, want 400", rec.Code)
	}
	if rec, m := e.call(t, userID, e.clubID, []string{"member"}, http.MethodPost, "/members/me/membership/cancel", body(maxCancelReasonRunes)); rec.Code != http.StatusOK || m.CancelledAt == nil {
		t.Errorf("2000 runes (4000 bytes) = %d, want cancelled", rec.Code)
	}
}

// HandleWithdraw now writes its audit row in the withdrawal's transaction: if
// the row cannot be written, the member stays on the list and gets a 500.
func TestHandleWithdrawAuditFailure(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"member"})
	seedWaiting(t, db, clubID, userID, "active", 1)
	mustExec(t, db, ctx, `CREATE FUNCTION test_fail_withdraw_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.action = 'withdraw' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$`)
	mustExec(t, db, ctx, `CREATE TRIGGER test_fail_withdraw_audit BEFORE INSERT ON audit_log
		FOR EACH ROW EXECUTE FUNCTION test_fail_withdraw_audit()`)

	h := NewWaitingListHandler(db, nil, testConfig(), zerolog.Nop())
	r := setupAuthenticatedRouter(http.MethodPost, "/waiting-list/withdraw", h.HandleWithdraw)
	req := httptest.NewRequest(http.MethodPost, "/waiting-list/withdraw", nil)
	req.Header.Set("Authorization", authHeader(generateTestToken(userID, clubID, []string{"member"})))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("withdraw with the audit down = %d, want 500", rec.Code)
	}
	if got := waitingStatuses(t, db, userID); !slices.Equal(got, []string{"active"}) {
		t.Errorf("entry = %v, want still active", got)
	}
}

// The end date is 31 December of the server-local year, at both edges of the
// year, and today is the server-local date.
func TestCancelEndsOnAndLocalToday(t *testing.T) {
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	time.Local = oslo

	cases := []struct {
		name      string
		now       time.Time
		today, ok string
	}{
		{"31 Dec, last minute", time.Date(2026, 12, 31, 23, 59, 0, 0, oslo), "2026-12-31", "2026-12-31"},
		{"1 Jan, first minute", time.Date(2027, 1, 1, 0, 1, 0, 0, oslo), "2027-01-01", "2027-12-31"},
		// 23:30 UTC on 31 Dec is already 1 Jan in Oslo: the local year wins.
		{"1 Jan local, 31 Dec UTC", time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC), "2027-01-01", "2027-12-31"},
		// 23:30 Oslo on 31 Dec is still 31 Dec locally.
		{"31 Dec local, 22:30 UTC", time.Date(2026, 12, 31, 22, 30, 0, 0, time.UTC), "2026-12-31", "2026-12-31"},
	}
	for _, c := range cases {
		if got := localToday(c.now); got != c.today {
			t.Errorf("%s: localToday = %s, want %s", c.name, got, c.today)
		}
		if got := cancelEndsOn(c.now); got != c.ok {
			t.Errorf("%s: cancelEndsOn = %s, want %s", c.name, got, c.ok)
		}
	}
}
