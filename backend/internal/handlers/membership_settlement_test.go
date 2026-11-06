package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/membership"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// seedJoinClub sets a 650 kr membership price item as the club's join item.
func seedJoinClub(t *testing.T, pool *pgxpool.Pool, clubID string) string {
	t.Helper()
	var itemID string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO price_items (club_id, category, name, amount) VALUES ($1, 'membership', 'Medlemskontingent', 650)
		 RETURNING id`, clubID,
	).Scan(&itemID); err != nil {
		t.Fatalf("seed price item: %v", err)
	}
	mustExec(t, pool, context.Background(), `UPDATE clubs SET join_price_item_id = $2 WHERE id = $1`, clubID, itemID)
	return itemID
}

// seedJoinAttempt seeds an applicant's pending join order, as confirm opens
// it, with one Vipps attempt, and returns the attempt as settlement resolves
// it.
func seedJoinAttempt(t *testing.T, s orderSettlement, clubID, itemID, userID string) paymentAttempt {
	t.Helper()
	ctx := context.Background()
	var orderID string
	if err := s.db.QueryRow(ctx,
		`INSERT INTO orders (club_id, user_id, total_amount, token, purpose)
		 VALUES ($1, $2, 650, $3, 'membership_join') RETURNING id`,
		clubID, userID, genCheckoutToken(),
	).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, s.db, ctx,
		`INSERT INTO order_lines (order_id, price_item_id, name, quantity, unit_price, total_price)
		 VALUES ($1, $2, 'Medlemskontingent', 1, 650, 650)`, orderID, itemID)
	ref := "join-" + testutil.RandomHex(8)
	mustExec(t, s.db, ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount)
		 VALUES ($1, $2, 'vipps', $3, 650)`, clubID, orderID, ref)
	a, ok, err := s.attemptByReference(ctx, clubID, "vipps", ref)
	if err != nil || !ok {
		t.Fatalf("attemptByReference: %v, %v", ok, err)
	}
	return a
}

// A captured join order makes the applicant a member in the settlement's
// transaction and queues the welcome and the board notice, once.
func TestSettleCaptureGrantsJoinMembership(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := orderSettlement{db: pool, log: zerolog.Nop()}
	clubID := testutil.SeedClub(t, pool)
	itemID := seedJoinClub(t, pool, clubID)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"applicant"})
	a := seedJoinAttempt(t, s, clubID, itemID, userID)

	for range 2 {
		if err := s.markAttemptCaptured(ctx, a, time.Now(), nil); err != nil {
			t.Fatalf("markAttemptCaptured: %v", err)
		}
	}

	var (
		roles       []string
		memberSince *time.Time
		kinds       []string
		grants      int
	)
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT array_agg(role::text) FROM user_roles WHERE user_id = u.id),
		        u.member_since,
		        COALESCE((SELECT array_agg(kind ORDER BY kind) FROM deliveries WHERE user_id = u.id), '{}'),
		        (SELECT count(*) FROM audit_log WHERE resource_id = u.id::text AND action = 'membership.granted'
		           AND details->>'source' = 'online')
		   FROM users u WHERE u.id = $1`, userID,
	).Scan(&roles, &memberSince, &kinds, &grants); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(roles, []string{"member"}) {
		t.Errorf("roles = %v, want [member]", roles)
	}
	if memberSince == nil {
		t.Error("member_since not set")
	}
	if !slices.Equal(kinds, []string{bcast.KindMembershipBoardNotice, bcast.KindMembershipWelcome}) {
		t.Errorf("queued = %v, want one board notice and one welcome", kinds)
	}
	if grants != 1 {
		t.Errorf("membership.granted rows = %d, want 1", grants)
	}
}

func TestBoardNotificationAddress(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)

	spec := filepath.Join(t.TempDir(), "board-mailboxes.json")
	if err := os.WriteFile(spec, []byte(`[
		{"address": "kasserar@club.example.test", "role": "treasurer", "type": "shared"},
		{"address": "post@club.example.test", "role": "board", "type": "shared", "from_brand_only": true}
	]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var slug string
	if err := pool.QueryRow(ctx, `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	withSpec := &config.Config{BoardMailboxesPath: spec, ClubSlug: slug}
	otherClubsSpec := &config.Config{BoardMailboxesPath: spec, ClubSlug: "another-club"}
	noSpec := &config.Config{ClubSlug: slug}

	set := func(setting, secretary, chairman string) {
		mustExec(t, pool, ctx,
			`UPDATE clubs SET board_notification_email = $2, secretary_email = $3, chairman_email = $4 WHERE id = $1`,
			clubID, setting, secretary, chairman)
	}
	cases := []struct {
		name                         string
		cfg                          *config.Config
		setting, secretary, chairman string
		want                         string
	}{
		{"club setting wins", withSpec, "styret@club.example.test", "sekretar@club.example.test", "leiar@club.example.test", "styret@club.example.test"},
		{"general shared mailbox by default", withSpec, "", "sekretar@club.example.test", "leiar@club.example.test", "post@club.example.test"},
		{"an invalid setting counts as unset", withSpec, "not an address", "", "", "post@club.example.test"},
		{"the shared mailbox is the deployment's club's only", otherClubsSpec, "", "sekretar@club.example.test", "", "sekretar@club.example.test"},
		{"secretary without a shared mailbox", noSpec, "", "sekretar@club.example.test", "leiar@club.example.test", "sekretar@club.example.test"},
		{"chair after that", noSpec, "", "", "leiar@club.example.test", "leiar@club.example.test"},
		{"nothing configured", noSpec, "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set(tc.setting, tc.secretary, tc.chairman)
			got, err := boardNotificationAddress(ctx, pool, tc.cfg, clubID)
			if err != nil || got != tc.want {
				t.Fatalf("boardNotificationAddress = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// flakyMailer fails its first fail sends with a transient error, then
// records like MockSender. Safe for the worker goroutine.
type flakyMailer struct {
	mu    sync.Mutex
	fail  int
	sent  []email.MockCall
	tries int
}

func (m *flakyMailer) Send(_ context.Context, to, subject, html string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tries++
	if m.fail > 0 {
		m.fail--
		return errors.New("451 try again later")
	}
	m.sent = append(m.sent, email.MockCall{To: to, Subject: subject, HTML: html})
	return nil
}

func (m *flakyMailer) SendWithAttachment(ctx context.Context, to, subject, html, _ string, _ []byte) error {
	return m.Send(ctx, to, subject, html)
}

func (m *flakyMailer) SendWithHeaders(ctx context.Context, to, subject, html string, _ map[string]string) error {
	return m.Send(ctx, to, subject, html)
}

func (m *flakyMailer) snapshot() (sent []email.MockCall, tries int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sent), m.tries
}

// runWorkerUntilDrained runs the delivery worker until no delivery is pending
// or sending, then stops it.
func runWorkerUntilDrained(t *testing.T, pool *pgxpool.Pool, w *bcast.Worker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx, 20*time.Millisecond); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var open int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM deliveries WHERE status IN ('pending', 'sending')`).Scan(&open); err != nil {
			t.Fatal(err)
		}
		if open == 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("%d deliveries still open", open)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}

// The worker sends each membership email once: a transient failure is
// retried and then sent, and a later drain sends nothing again. A refund of
// the join payment tells the board and leaves the member a member.
func TestMembershipNoticesSendOnce(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := orderSettlement{db: pool, log: zerolog.Nop()}
	clubID := testutil.SeedClub(t, pool)
	itemID := seedJoinClub(t, pool, clubID)
	mustExec(t, pool, ctx, `UPDATE clubs SET board_notification_email = 'styret@club.example.test', default_language = 'nn' WHERE id = $1`, clubID)
	userID, addr := testutil.SeedUser(t, pool, clubID, []string{"applicant"})
	a := seedJoinAttempt(t, s, clubID, itemID, userID)
	if err := s.markAttemptCaptured(ctx, a, time.Now(), nil); err != nil {
		t.Fatal(err)
	}

	mailer := &flakyMailer{fail: 1}
	cfg := &config.Config{ClubName: "Testklubben", Domain: "club.example.test", FrontendURL: "https://club.example.test"}
	newWorker := func() *bcast.Worker {
		w := bcast.NewWorker(bcast.NewStore(pool), 0, zerolog.Nop())
		sender := NewMembershipNoticeSender(pool, cfg, mailer, zerolog.Nop())
		for _, k := range []string{bcast.KindMembershipWelcome, bcast.KindMembershipBoardNotice, bcast.KindMembershipJoinRefunded} {
			w.RegisterSender(k, sender)
		}
		return w
	}
	runWorkerUntilDrained(t, pool, newWorker())

	sent, tries := mailer.snapshot()
	if tries != 3 || len(sent) != 2 {
		t.Fatalf("tries = %d, sent = %d; want 3 tries (one retried) and 2 sent", tries, len(sent))
	}
	byTo := map[string]email.MockCall{}
	for _, c := range sent {
		if _, dup := byTo[c.To]; dup {
			t.Fatalf("sent twice to %s", c.To)
		}
		byTo[c.To] = c
	}
	welcome, ok := byTo[addr]
	if !ok || !strings.Contains(welcome.Subject, "Velkomen som medlem") || !strings.Contains(welcome.HTML, "/portal/waiting-list") {
		t.Errorf("welcome = %+v", welcome)
	}
	board, ok := byTo["styret@club.example.test"]
	if !ok || board.Subject != "Nytt medlem: Test User" || !strings.Contains(board.HTML, "/admin/users?user="+userID) {
		t.Errorf("board notice = %+v", board)
	}
	var failed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deliveries WHERE status <> 'sent'`).Scan(&failed); err != nil || failed != 0 {
		t.Fatalf("unsent deliveries = %d, %v", failed, err)
	}

	// The provider refunds the join payment: the board hears of it, once,
	// and the membership stays.
	mustExec(t, pool, ctx,
		`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, currency, status, reason, provider_refund_id)
		 VALUES ($1, $2, 'vipps', 650, 'NOK', 'succeeded', 'test', $3)`, clubID, a.ID, "re-"+testutil.RandomHex(6))
	for range 2 {
		if _, err := membership.QueueJoinRefundNotices(ctx, pool, clubID); err != nil {
			t.Fatal(err)
		}
	}
	runWorkerUntilDrained(t, pool, newWorker())
	// A second drain (a restart's boot sweep) finds nothing to send again.
	runWorkerUntilDrained(t, pool, newWorker())

	sent, _ = mailer.snapshot()
	if len(sent) != 3 {
		t.Fatalf("sent = %d, want the two grant emails and one refund notice", len(sent))
	}
	refund := sent[2]
	if refund.To != "styret@club.example.test" || refund.Subject != "Innmeldingsbetaling refundert: Test User" {
		t.Errorf("refund notice = %+v", refund)
	}
	var roles []string
	if err := pool.QueryRow(ctx, `SELECT array_agg(role::text) FROM user_roles WHERE user_id = $1`, userID).Scan(&roles); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(roles, []string{"member"}) {
		t.Errorf("roles after refund = %v, want [member]", roles)
	}
}

// The receipt page learns from the checkout that the order was a join, so it
// refreshes the session whose roles the payment changed.
func TestCheckoutReportsJoinPurpose(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := orderSettlement{db: pool, log: zerolog.Nop()}
	clubID := testutil.SeedClub(t, pool)
	itemID := seedJoinClub(t, pool, clubID)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"applicant"})
	a := seedJoinAttempt(t, s, clubID, itemID, userID)
	h := NewInvoiceHandler(pool, &config.Config{}, nil, nil, nil, nil, zerolog.Nop())

	resp, err := h.buildCheckoutResp(ctx, a.OrderID, clubID, "pending")
	if err != nil || resp.Purpose != membership.Purpose {
		t.Fatalf("join order purpose = %q, %v", resp.Purpose, err)
	}
	other, _ := seedCheckoutOrder(t, pool, clubID, userID)
	resp, err = h.buildCheckoutResp(ctx, other, clubID, "pending")
	if err != nil || resp.Purpose != "" {
		t.Fatalf("ordinary order purpose = %q, %v", resp.Purpose, err)
	}
}

// A board notice with no address to go to is not lost: it fails with a note,
// and once the club has an address the hourly requeue sends it.
func TestBoardNoticeWaitsForAnAddress(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := orderSettlement{db: pool, log: zerolog.Nop()}
	clubID := testutil.SeedClub(t, pool)
	itemID := seedJoinClub(t, pool, clubID)
	userID, addr := testutil.SeedUser(t, pool, clubID, []string{"applicant"})
	a := seedJoinAttempt(t, s, clubID, itemID, userID)
	if err := s.markAttemptCaptured(ctx, a, time.Now(), nil); err != nil {
		t.Fatal(err)
	}

	mailer := &flakyMailer{}
	sender := NewMembershipNoticeSender(pool, &config.Config{}, mailer, zerolog.Nop())
	newWorker := func() *bcast.Worker {
		w := bcast.NewWorker(bcast.NewStore(pool), 0, zerolog.Nop())
		w.RegisterSender(bcast.KindMembershipWelcome, sender)
		w.RegisterSender(bcast.KindMembershipBoardNotice, sender)
		return w
	}
	runWorkerUntilDrained(t, pool, newWorker())

	var status, note string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, error, attempts FROM deliveries WHERE kind = 'membership_board_notice' AND user_id = $1`, userID,
	).Scan(&status, &note, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != bcast.DeliveryFailed || note != errNoBoardAddress.Error() || attempts != 1 {
		t.Fatalf("board notice = %s/%q after %d attempts; want failed at once with the no-address note", status, note, attempts)
	}
	if n, err := sender.RequeueUnaddressed(ctx); err != nil || n != 0 {
		t.Fatalf("requeue without an address = %d, %v; want 0", n, err)
	}

	mustExec(t, pool, ctx, `UPDATE clubs SET board_notification_email = 'styret@club.example.test' WHERE id = $1`, clubID)
	if n, err := sender.RequeueUnaddressed(ctx); err != nil || n != 1 {
		t.Fatalf("requeue = %d, %v; want 1", n, err)
	}
	runWorkerUntilDrained(t, pool, newWorker())
	sent, _ := mailer.snapshot()
	var tos []string
	for _, c := range sent {
		tos = append(tos, c.To)
	}
	want := []string{addr, "styret@club.example.test"}
	slices.Sort(tos)
	slices.Sort(want)
	if !slices.Equal(tos, want) {
		t.Fatalf("sent to %v; want the welcome and the board notice once each", tos)
	}
}

// Without mail, or without the member, a membership row fails with a note
// saying why; it is never recorded as sent.
func TestMembershipNoticeUnsendableIsNotSent(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := orderSettlement{db: pool, log: zerolog.Nop()}
	clubID := testutil.SeedClub(t, pool)
	itemID := seedJoinClub(t, pool, clubID)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"applicant"})
	a := seedJoinAttempt(t, s, clubID, itemID, userID)
	if err := s.markAttemptCaptured(ctx, a, time.Now(), nil); err != nil {
		t.Fatal(err)
	}

	w := bcast.NewWorker(bcast.NewStore(pool), 0, zerolog.Nop())
	sender := NewMembershipNoticeSender(pool, &config.Config{}, nil, zerolog.Nop())
	w.RegisterSender(bcast.KindMembershipWelcome, sender)
	w.RegisterSender(bcast.KindMembershipBoardNotice, sender)
	runWorkerUntilDrained(t, pool, w)

	rows, err := pool.Query(ctx, `SELECT status, error FROM deliveries WHERE user_id = $1`, userID)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ status, note string }
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.status, &x.note)
	})
	if err != nil || len(got) != 2 {
		t.Fatalf("deliveries = %+v, %v", got, err)
	}
	for _, r := range got {
		if r.status != bcast.DeliveryFailed || !strings.Contains(r.note, "no mail transport configured") {
			t.Errorf("delivery = %+v; want failed with the no-mail note", r)
		}
	}
}

// A grant that fails inside the capture is rolled back alone: the order is
// paid all the same, and the membership sweep grants afterwards.
func TestSettleCaptureSurvivesGrantFailure(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := orderSettlement{db: pool, log: zerolog.Nop()}
	clubID := testutil.SeedClub(t, pool)
	itemID := seedJoinClub(t, pool, clubID)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"applicant"})
	a := seedJoinAttempt(t, s, clubID, itemID, userID)

	mustExec(t, pool, ctx, `CREATE OR REPLACE FUNCTION test_fail_member_grant() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.role = 'member' THEN RAISE EXCEPTION 'synthetic grant failure'; END IF;
			RETURN NEW;
		END $$`)
	mustExec(t, pool, ctx, `CREATE TRIGGER test_fail_member_grant BEFORE INSERT ON user_roles
		FOR EACH ROW EXECUTE FUNCTION test_fail_member_grant()`)
	if err := s.markAttemptCaptured(ctx, a, time.Now(), nil); err != nil {
		t.Fatalf("capture failed with the grant: %v", err)
	}
	var orderStatus, attemptStatus string
	var roles []string
	load := func() {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT o.status::text, pa.status, (SELECT array_agg(role::text) FROM user_roles WHERE user_id = o.user_id)
			   FROM orders o JOIN payment_attempts pa ON pa.order_id = o.id WHERE pa.id = $1`, a.ID,
		).Scan(&orderStatus, &attemptStatus, &roles); err != nil {
			t.Fatal(err)
		}
	}
	load()
	if orderStatus != "paid" || attemptStatus != "captured" || !slices.Equal(roles, []string{"applicant"}) {
		t.Fatalf("after a failed grant: order %s, attempt %s, roles %v", orderStatus, attemptStatus, roles)
	}

	mustExec(t, pool, ctx, `DROP TRIGGER test_fail_member_grant ON user_roles`)
	if res, err := membership.Sweep(ctx, pool); err != nil || res.Granted != 1 {
		t.Fatalf("sweep = %+v, %v", res, err)
	}
	load()
	if !slices.Equal(roles, []string{"member"}) {
		t.Fatalf("roles after sweep = %v", roles)
	}
}
