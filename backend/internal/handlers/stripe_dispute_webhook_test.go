package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/stripe"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

const disputeTreasurer = "kasserer@example.invalid"

// disputeEnv is a Stripe webhook environment with a test-mode key, a mock
// mail transport and a treasurer address.
func newDisputeEnv(t *testing.T) (*stripeEnv, *email.MockSender) {
	t.Helper()
	e := newStripeEnv(t)
	e.h.cfg.StripeSecretKey = stripeTestKey
	mail := &email.MockSender{}
	e.h.SetMailer(mail)
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE clubs SET treasurer_email = $2, name = 'Synthetic Båtlag' WHERE id = $1`, e.clubID, disputeTreasurer); err != nil {
		t.Fatal(err)
	}
	return e, mail
}

func disputeEvent(eventType, eventID, disputeID, piID, chargeID, status string, dueBy int64) map[string]any {
	obj := map[string]any{
		"id": disputeID, "object": "dispute", "amount": 45000, "currency": "nok", "reason": "fraudulent",
		"status": status, "charge": chargeID, "payment_intent": piID,
		"evidence_details": map[string]any{"due_by": dueBy, "has_evidence": false},
		// Stripe's own metadata, which says nothing about Brygge's attempt.
		"metadata": map[string]any{},
	}
	return map[string]any{
		"id": eventID, "type": eventType, "created": time.Now().Unix(),
		"data": map[string]any{"object": obj},
	}
}

type storedWebhookDispute struct {
	ID, Status, Reason, Currency, ClubID string
	Attempt, Order                       *string
	AmountKr                             *float64
	Due                                  *time.Time
}

func (e *stripeEnv) dispute(t *testing.T, id string) (storedWebhookDispute, bool) {
	t.Helper()
	var d storedWebhookDispute
	err := e.pool.QueryRow(context.Background(),
		`SELECT provider_dispute_id, status, reason, currency, club_id::text, attempt_id::text, order_id::text,
		        amount::float8, evidence_due_by
		   FROM payment_disputes WHERE provider = 'stripe' AND provider_dispute_id = $1`, id,
	).Scan(&d.ID, &d.Status, &d.Reason, &d.Currency, &d.ClubID, &d.Attempt, &d.Order, &d.AmountKr, &d.Due)
	if err != nil {
		return d, false
	}
	return d, true
}

func (e *stripeEnv) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A dispute is tied to its payment through the PaymentIntent's metadata,
// stored, and told to the treasurer once: redelivery, later events and
// events out of order change neither the mail nor a closed dispute's
// status. The webhook writes nothing but payment_disputes.
func TestStripeWebhook_DisputeStoredAndTreasurerToldOnce(t *testing.T) {
	e, mail := newDisputeEnv(t)
	userID, _ := testutil.SeedUser(t, e.pool, e.clubID, []string{"member"})
	invID := seedPayableInvoice(t, e.pool, e.clubID, userID, "open", 701)
	orderID := e.seedOrder(t, userID)
	addOrderSource(t, e.pool, orderID, invID)
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_d1", "captured")
	e.fake.intentMeta["pi_d1"] = map[string]string{"attempt_id": attemptID, "club_slug": e.h.cfg.ClubSlug}
	due := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC).Unix()

	orderBefore := e.order(t, orderID)
	created := disputeEvent(stripe.EventChargeDisputeCreated, "evt_dp_1", "dp_1", "pi_d1", "ch_d1", "needs_response", due)
	assertWebhookOK(t, e.post(t, created))

	d, ok := e.dispute(t, "dp_1")
	if !ok || d.ClubID != e.clubID || d.Attempt == nil || *d.Attempt != attemptID || d.Order == nil || *d.Order != orderID ||
		d.Status != "needs_response" || d.Reason != "fraudulent" || d.Currency != "NOK" ||
		d.AmountKr == nil || *d.AmountKr != 450 || d.Due == nil || d.Due.Unix() != due {
		t.Fatalf("stored dispute = %+v (found %v)", d, ok)
	}
	if got := e.fake.intentGetCalls(); len(got) != 1 || got[0] != "pi_d1" {
		t.Errorf("payment intent lookups = %v, want one for pi_d1", got)
	}
	if len(mail.Calls) != 1 {
		t.Fatalf("mails = %d, want 1", len(mail.Calls))
	}
	c := mail.Calls[0]
	if c.To != disputeTreasurer || !strings.HasPrefix(c.Subject, "Omstridt kortbetaling – svar innen ") ||
		!strings.Contains(c.Subject, time.Unix(due, 0).In(time.Local).Format("02.01.2006")) {
		t.Errorf("mail to %q with subject %q", c.To, c.Subject)
	}
	for _, want := range []string{"kr 450,00", "faktura 701", "svindel", "https://dashboard.stripe.com/test/disputes/dp_1", "Synthetic Båtlag"} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("mail lacks %q:\n%s", want, c.HTML)
		}
	}
	if strings.Contains(c.HTML, "acct_") || strings.Contains(c.HTML, "pi_d1") || strings.Contains(c.HTML, "ch_d1") {
		t.Errorf("mail names an account or payment id:\n%s", c.HTML)
	}

	// Redelivery of the same event, and later events: no second mail.
	assertWebhookOK(t, e.post(t, created))
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeUpdated, "evt_dp_2", "dp_1", "pi_d1", "ch_d1", "under_review", due)))
	if d, _ := e.dispute(t, "dp_1"); d.Status != "under_review" {
		t.Errorf("status after the update = %q", d.Status)
	}
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeClosed, "evt_dp_3", "dp_1", "pi_d1", "ch_d1", "lost", due)))
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeFundsWithdrawn, "evt_dp_4", "dp_1", "pi_d1", "ch_d1", "lost", due)))
	// A delayed earlier event does not reopen the dispute.
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeUpdated, "evt_dp_5", "dp_1", "pi_d1", "ch_d1", "under_review", due)))
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeFundsReinstated, "evt_dp_6", "dp_1", "pi_d1", "ch_d1", "won", due)))
	if d, _ := e.dispute(t, "dp_1"); d.Status != "lost" {
		t.Errorf("status after out-of-order events = %q, want lost", d.Status)
	}
	if len(mail.Calls) != 1 {
		t.Errorf("mails = %d after redelivery and later events, want 1", len(mail.Calls))
	}
	if n := e.count(t, `SELECT count(*) FROM payment_disputes`); n != 1 {
		t.Errorf("dispute rows = %d, want 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM payment_webhook_events WHERE club_id = $1 AND event_name LIKE 'charge.dispute.%'`, e.clubID); n != 6 {
		t.Errorf("stored events = %d, want 6", n)
	}

	// Nothing but payment_disputes was written.
	if o := e.order(t, orderID); o != orderBefore {
		t.Errorf("order changed: %+v -> %+v", orderBefore, o)
	}
	var invStatus string
	var paymentID *string
	if err := e.pool.QueryRow(context.Background(), `SELECT status, payment_id::text FROM invoices WHERE id = $1`, invID).Scan(&invStatus, &paymentID); err != nil ||
		invStatus != "open" || paymentID != nil {
		t.Errorf("invoice = %q %v (%v), want it untouched", invStatus, paymentID, err)
	}
	if s := e.attemptStatus(t, attemptID); s != "captured" {
		t.Errorf("attempt = %q", s)
	}
	for _, table := range []string{"payment_refunds", "journal_entries", "stripe_balance_transactions", "refund_documents"} {
		if n := e.count(t, `SELECT count(*) FROM `+table); n != 0 {
			t.Errorf("%s has %d rows, want none", table, n)
		}
	}
}

// The club of a dispute is the club of the payment it names, not the
// instance's: the event is stored under it, and its treasurer is told.
func TestStripeWebhook_DisputeClubFollowsThePayment(t *testing.T) {
	e, mail := newDisputeEnv(t)
	other := testutil.SeedClub(t, e.pool)
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE clubs SET treasurer_email = 'annen-kasserer@example.invalid' WHERE id = $1`, other); err != nil {
		t.Fatal(err)
	}
	var orderID, attemptID string
	ctx := context.Background()
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, token) VALUES ($1, 450, $2) RETURNING id`, other, genCheckoutToken(),
	).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount)
		 VALUES ($1, $2, 'stripe', 'cs_other', 'captured', 450) RETURNING id`, other, orderID,
	).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	e.fake.intentMeta["pi_o"] = map[string]string{"attempt_id": attemptID, "club_slug": e.h.cfg.ClubSlug}

	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_o", "dp_o", "pi_o", "ch_o", "needs_response", 0)))

	if d, ok := e.dispute(t, "dp_o"); !ok || d.ClubID != other {
		t.Errorf("dispute = %+v (found %v), want it under the payment's club", d, ok)
	}
	if n := e.count(t, `SELECT count(*) FROM payment_webhook_events WHERE club_id = $1 AND event_id = 'evt_o'`, other); n != 1 {
		t.Errorf("event stored under the payment's club %d times, want once", n)
	}
	if len(mail.Calls) != 1 || mail.Calls[0].To != "annen-kasserer@example.invalid" {
		t.Errorf("mails = %+v, want one to the payment's club", mail.Calls)
	}
	if strings.Contains(mail.Calls[0].Subject, " innen ") {
		t.Errorf("subject %q names a deadline Stripe did not give", mail.Calls[0].Subject)
	}
}

// When the PaymentIntent names no attempt of Brygge's (or cannot be
// fetched), the stored charge, found by its charge id, does.
func TestStripeWebhook_DisputeFallsBackToTheChargeRow(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(e *stripeEnv)
	}{
		{"payment intent without metadata", func(e *stripeEnv) {}},
		{"payment intent not found", func(e *stripeEnv) { e.fake.intentMissing["pi_c"] = true }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e, mail := newDisputeEnv(t)
			orderID := e.seedOrder(t, "")
			attemptID := e.seedAttempt(t, orderID, "stripe", "cs_c", "captured")
			if _, err := e.pool.Exec(context.Background(),
				`INSERT INTO stripe_balance_transactions
				     (club_id, balance_transaction_id, reporting_category, amount_ore, net_ore, currency, created_at,
				      charge_id, payment_intent_id, attempt_id, order_id, status)
				 VALUES ($1, 'txn_c', 'charge', 45000, 45000, 'nok', now(), 'ch_c', 'pi_c', $2, $3, 'unbooked')`,
				e.clubID, attemptID, orderID); err != nil {
				t.Fatal(err)
			}
			tt.setup(e)

			assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_c", "dp_c", "pi_c", "ch_c", "needs_response", 0)))

			d, ok := e.dispute(t, "dp_c")
			if !ok || d.Attempt == nil || *d.Attempt != attemptID || d.Order == nil || *d.Order != orderID {
				t.Fatalf("dispute = %+v (found %v), want it tied to the charge's attempt", d, ok)
			}
			if len(mail.Calls) != 1 || !strings.Contains(mail.Calls[0].HTML, "ordre "+orderID[:8]) {
				t.Errorf("mails = %+v, want one naming the order", mail.Calls)
			}
		})
	}
}

// A dispute on a payment of another deployment sharing the Stripe account
// is left alone: nothing stored, nobody told.
func TestStripeWebhook_DisputeOfAnotherDeploymentIgnored(t *testing.T) {
	e, mail := newDisputeEnv(t)
	e.fake.intentMeta["pi_f"] = map[string]string{"attempt_id": "00000000-0000-0000-0000-000000000000", "club_slug": "another-club"}

	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_f", "dp_f", "pi_f", "ch_f", "needs_response", 0)))

	if _, ok := e.dispute(t, "dp_f"); ok || len(mail.Calls) != 0 {
		t.Errorf("stored %v, mails %d; want neither", ok, len(mail.Calls))
	}
}

// A dispute Brygge can tie to nothing is still recorded for the instance's
// club and told, so a deadline is not missed. Without an address or a mail
// transport, or when the mail fails, it is stored all the same and the event
// is answered 200.
func TestStripeWebhook_DisputeWithoutPaymentOrMail(t *testing.T) {
	t.Run("untied", func(t *testing.T) {
		e, mail := newDisputeEnv(t)
		assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_u", "dp_u", "pi_u", "ch_u", "needs_response", 0)))
		d, ok := e.dispute(t, "dp_u")
		if !ok || d.ClubID != e.clubID || d.Attempt != nil || d.Order != nil {
			t.Errorf("dispute = %+v (found %v)", d, ok)
		}
		if len(mail.Calls) != 1 || strings.Contains(mail.Calls[0].HTML, "Gjelder:") {
			t.Errorf("mails = %+v, want one naming no payment", mail.Calls)
		}
	})
	t.Run("no treasurer address", func(t *testing.T) {
		e, mail := newDisputeEnv(t)
		if _, err := e.pool.Exec(context.Background(), `UPDATE clubs SET treasurer_email = '  '`); err != nil {
			t.Fatal(err)
		}
		assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_n", "dp_n", "pi_n", "ch_n", "needs_response", 0)))
		if _, ok := e.dispute(t, "dp_n"); !ok || len(mail.Calls) != 0 {
			t.Errorf("stored %v, mails %d", ok, len(mail.Calls))
		}
	})
	t.Run("no mail transport", func(t *testing.T) {
		e, _ := newDisputeEnv(t)
		e.h.mailer = nil
		assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_m", "dp_m", "pi_m", "ch_m", "needs_response", 0)))
		if _, ok := e.dispute(t, "dp_m"); !ok {
			t.Error("dispute not stored")
		}
	})
	t.Run("mail fails", func(t *testing.T) {
		e, mail := newDisputeEnv(t)
		mail.Err = errors.New("smtp down")
		assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_x", "dp_x", "pi_x", "ch_x", "needs_response", 0)))
		if _, ok := e.dispute(t, "dp_x"); !ok || len(mail.Calls) != 1 {
			t.Errorf("stored %v, mail attempts %d", ok, len(mail.Calls))
		}
		// Redelivery does not try again: the mail is best effort and once.
		assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_x", "dp_x", "pi_x", "ch_x", "needs_response", 0)))
		if len(mail.Calls) != 1 {
			t.Errorf("mail attempts = %d after redelivery, want 1", len(mail.Calls))
		}
	})
}

// The dispute object is not trusted to have any shape: what does not decode
// is left out, and nothing in it fails the event.
func TestStripeWebhook_DisputeToleratesMalformedObjects(t *testing.T) {
	e, mail := newDisputeEnv(t)
	post := func(id string, object any) {
		t.Helper()
		assertWebhookOK(t, e.post(t, map[string]any{
			"id": id, "type": stripe.EventChargeDisputeUpdated, "created": time.Now().Unix(),
			"data": map[string]any{"object": object},
		}))
	}
	post("evt_bad1", map[string]any{"id": "dp_bad1", "amount": "lots", "currency": 7, "payment_intent": 12,
		"evidence_details": "none", "metadata": map[string]any{"k": 1}, "status": "under_review"})
	post("evt_bad2", "dp_bad2")
	post("evt_bad3", map[string]any{"object": "dispute"})
	post("evt_bad4", map[string]any{"id": "dp_bad4"})

	if d, ok := e.dispute(t, "dp_bad1"); !ok || d.Status != "under_review" || d.AmountKr != nil || d.Due != nil {
		t.Errorf("dp_bad1 = %+v (found %v)", d, ok)
	}
	if d, ok := e.dispute(t, "dp_bad4"); !ok || d.Status != "" || d.Currency != "NOK" {
		t.Errorf("dp_bad4 = %+v (found %v)", d, ok)
	}
	if n := e.count(t, `SELECT count(*) FROM payment_disputes`); n != 2 {
		t.Errorf("dispute rows = %d, want 2", n)
	}
	if len(mail.Calls) != 1 {
		t.Errorf("mails = %d, want one: a dispute with no status is not told", len(mail.Calls))
	}
}

// A dispute that cannot be stored answers 500, so Stripe delivers the event
// again; the event itself is kept.
func TestStripeWebhook_DisputeStoreFailureAsksForRedelivery(t *testing.T) {
	e, _ := newDisputeEnv(t)
	if _, err := e.pool.Exec(context.Background(), `DROP TABLE payment_disputes`); err != nil {
		t.Fatal(err)
	}
	rec := e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_5xx", "dp_5xx", "pi_5xx", "ch_5xx", "needs_response", 0))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if n := e.count(t, `SELECT count(*) FROM payment_webhook_events WHERE event_id = 'evt_5xx'`); n != 1 {
		t.Errorf("stored events = %d, want the event kept", n)
	}
}

// When Stripe cannot be asked about the dispute's payment and nothing else
// places it, the event is answered with an error so Stripe delivers it again,
// rather than guessing it is ours (or another deployment's). A payment Stripe
// says does not exist is an answer, not a failure.
func TestStripeWebhook_DisputeLookupFailure(t *testing.T) {
	e, mail := newDisputeEnv(t)
	e.fake.intentErrors["pi_l"] = http.StatusInternalServerError

	rec := e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_l", "dp_l", "pi_l", "ch_l", "needs_response", 0))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if _, ok := e.dispute(t, "dp_l"); ok || len(mail.Calls) != 0 {
		t.Errorf("stored %v, mails %d; want neither", ok, len(mail.Calls))
	}

	// The charge row places it even though Stripe cannot be asked.
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_l", "captured")
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, amount_ore, net_ore, currency, created_at,
		      charge_id, payment_intent_id, attempt_id, order_id, status)
		 VALUES ($1, 'txn_l', 'charge', 45000, 45000, 'nok', now(), 'ch_l', 'pi_l', $2, $3, 'unbooked')`,
		e.clubID, attemptID, orderID); err != nil {
		t.Fatal(err)
	}
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_l", "dp_l", "pi_l", "ch_l", "needs_response", 0)))
	if d, ok := e.dispute(t, "dp_l"); !ok || d.Attempt == nil || *d.Attempt != attemptID {
		t.Errorf("dispute = %+v (found %v), want it placed by the charge", d, ok)
	}

	// A payment Stripe does not know is stored for the instance, unplaced.
	e.fake.intentMissing["pi_gone"] = true
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_g", "dp_g", "pi_gone", "ch_gone", "needs_response", 0)))
	if d, ok := e.dispute(t, "dp_g"); !ok || d.Attempt != nil {
		t.Errorf("dispute = %+v (found %v), want it stored unplaced", d, ok)
	}
}

// An attempt of ours settles which deployment a payment is, whatever slug its
// metadata carries: a renamed club slug does not drop disputes on older
// payments.
func TestStripeWebhook_DisputeOurAttemptBeatsSlug(t *testing.T) {
	e, mail := newDisputeEnv(t)
	orderID := e.seedOrder(t, "")
	attemptID := e.seedAttempt(t, orderID, "stripe", "cs_r", "captured")
	e.fake.intentMeta["pi_r"] = map[string]string{"attempt_id": attemptID, "club_slug": "the-old-slug"}

	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_r", "dp_r", "pi_r", "ch_r", "needs_response", 0)))
	if d, ok := e.dispute(t, "dp_r"); !ok || d.Attempt == nil || *d.Attempt != attemptID {
		t.Errorf("dispute = %+v (found %v)", d, ok)
	}
	if len(mail.Calls) != 1 {
		t.Errorf("mails = %d, want 1", len(mail.Calls))
	}
}

// The treasurer is told once for the inquiry and once more when it becomes a
// chargeback (a new deadline); an old inquiry event, a redelivery or a
// dispute that arrives already closed tell nobody.
func TestStripeWebhook_DisputeEscalationToldOnce(t *testing.T) {
	e, mail := newDisputeEnv(t)
	inquiry := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC).Unix()
	chargeback := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC).Unix()
	send := func(id, status string, due int64) {
		t.Helper()
		assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeUpdated, id, "dp_e", "pi_e", "ch_e", status, due)))
	}

	send("evt_e1", "warning_needs_response", inquiry)
	send("evt_e1", "warning_needs_response", inquiry)
	if len(mail.Calls) != 1 {
		t.Fatalf("mails after the inquiry = %d, want 1", len(mail.Calls))
	}
	send("evt_e2", "warning_closed", inquiry)
	send("evt_e3", "needs_response", chargeback)
	send("evt_e3", "needs_response", chargeback)
	send("evt_e4", "warning_under_review", 0)
	if len(mail.Calls) != 2 {
		t.Fatalf("mails after the escalation = %d, want 2", len(mail.Calls))
	}
	if !strings.Contains(mail.Calls[1].Subject, time.Unix(chargeback, 0).In(time.Local).Format("02.01.2006")) {
		t.Errorf("escalation subject %q names the old deadline", mail.Calls[1].Subject)
	}
	d, _ := e.dispute(t, "dp_e")
	if d.Status != "needs_response" || d.Due == nil || d.Due.Unix() != chargeback {
		t.Errorf("dispute = %+v, want the chargeback", d)
	}

	// Arrived closed: stored, nobody told.
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeClosed, "evt_c1", "dp_c1", "pi_c1", "ch_c1", "won", 0)))
	if _, ok := e.dispute(t, "dp_c1"); !ok || len(mail.Calls) != 2 {
		t.Errorf("closed dispute stored %v, mails %d; want stored and no mail", ok, len(mail.Calls))
	}
}

// The Dashboard link names the dispute when its id is one Stripe could have
// sent, and the disputes page otherwise; the id is never put in a link as is.
func TestStripeWebhook_DisputeDashboardLink(t *testing.T) {
	e, mail := newDisputeEnv(t)
	assertWebhookOK(t, e.post(t, disputeEvent(stripe.EventChargeDisputeCreated, "evt_k1", "dp_odd id/../x", "pi_k", "ch_k", "needs_response", 0)))
	if len(mail.Calls) != 1 || !strings.Contains(mail.Calls[0].HTML, `href="https://dashboard.stripe.com/test/disputes"`) {
		t.Fatalf("mails = %+v, want a link to the disputes page", mail.Calls)
	}
	if strings.Contains(mail.Calls[0].HTML, "odd") {
		t.Errorf("the odd id reached the mail: %s", mail.Calls[0].HTML)
	}
}
