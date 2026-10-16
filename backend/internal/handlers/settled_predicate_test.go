package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// settledFixture seeds one sent invoice in each settlement state the shared
// predicate distinguishes.
type settledFixture struct {
	club                                  *fin.Club
	h                                     *InvoiceHandler
	unpaid, vippsPaid, voided, reconciled fin.Invoice
}

func newSettledFixture(t *testing.T) *settledFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	club := fin.NewClub(t, pool)
	f := &settledFixture{
		club: club,
		h:    &InvoiceHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()},
	}
	f.unpaid = club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.vippsPaid = club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, VippsPaid: true})
	f.voided = club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Status: "voided"})
	f.reconciled = club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})

	var paymentID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (club_id, user_id, type, amount, status)
		 VALUES ($1, $2, 'dues', 450, 'completed') RETURNING id`,
		club.ID, f.reconciled.MemberID,
	).Scan(&paymentID); err != nil {
		t.Fatalf("payment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE invoices SET payment_id = $1 WHERE id = $2`, paymentID, f.reconciled.ID,
	); err != nil {
		t.Fatalf("link payment: %v", err)
	}
	// Reminders need a stored PDF to be eligible at all.
	if _, err := pool.Exec(ctx,
		`UPDATE invoices SET pdf_data = '\x25504446'::bytea WHERE club_id = $1`, club.ID,
	); err != nil {
		t.Fatalf("pdf: %v", err)
	}
	return f
}

func TestReminderEnqueueSkipsSettledInvoices(t *testing.T) {
	f := newSettledFixture(t)
	items, res, err := f.h.collectReminderItems(context.Background(), f.club.ID,
		[]string{f.unpaid.ID, f.vippsPaid.ID, f.voided.ID, f.reconciled.ID}, nil)
	if err != nil {
		t.Fatalf("collectReminderItems: %v", err)
	}
	if len(items) != 1 || items[0].InvoiceID != f.unpaid.ID {
		t.Errorf("enqueued %+v, want only the unpaid invoice", items)
	}
	if res.Skipped != 3 {
		t.Errorf("skipped = %d, want 3 (vipps-paid, voided, reconciled)", res.Skipped)
	}
}

// The worker re-checks at send time: a member can pay through the pay link
// while the reminder sits in the queue.
func TestReminderSendSkipsInvoicePaidAfterEnqueue(t *testing.T) {
	f := newSettledFixture(t)
	for name, inv := range map[string]fin.Invoice{
		"vipps-paid": f.vippsPaid, "voided": f.voided, "reconciled": f.reconciled,
	} {
		err := f.h.sendOneReminder(context.Background(), f.club.ID, f.club.Treasurer.ID, "",
			inv.ID, "Synthetic Club", "", "nb")
		if !errors.Is(err, errSkip) {
			t.Errorf("%s: sendOneReminder = %v, want errSkip", name, err)
		}
	}
}

func TestReissueKIDSkipsSettledInvoices(t *testing.T) {
	f := newSettledFixture(t)
	for name, inv := range map[string]fin.Invoice{
		"vipps-paid": f.vippsPaid, "voided": f.voided, "reconciled": f.reconciled,
	} {
		err := f.h.reissueOneKID(context.Background(), f.club.ID, f.club.Treasurer.ID, "", inv.ID, nil, "mod10")
		if !errors.Is(err, errSkip) {
			t.Errorf("%s: reissueOneKID = %v, want errSkip", name, err)
		}
	}
}

func TestOrderTotalCountsOnlyUnsettledSources(t *testing.T) {
	f := newSettledFixture(t)
	order := f.club.NewOrderForInvoice(t, f.unpaid)
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO order_sources (order_id, invoice_id) VALUES ($1, $2)`, order.ID, f.vippsPaid.ID,
	); err != nil {
		t.Fatalf("second source: %v", err)
	}
	total, _, err := f.h.orderTotal(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("orderTotal: %v", err)
	}
	if total != 450 {
		t.Errorf("order total = %v, want 450 (the vipps-paid source is settled)", total)
	}
}

func TestVoidRefusesSettledInvoice(t *testing.T) {
	f := newSettledFixture(t)
	claims := &auth.Claims{UserID: f.club.Treasurer.ID, ClubID: f.club.ID, Roles: []string{"treasurer"}}
	void := func(id string) int {
		r := chi.NewRouter()
		r.Post("/invoices/{invoiceID}/void", f.h.HandleVoidInvoice)
		req := httptest.NewRequest(http.MethodPost, "/invoices/"+id+"/void", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	for name, c := range map[string]struct {
		id   string
		want int
	}{
		"unpaid":         {f.unpaid.ID, http.StatusNoContent},
		"already voided": {f.voided.ID, http.StatusNoContent},
		"vipps-paid":     {f.vippsPaid.ID, http.StatusConflict},
		"reconciled":     {f.reconciled.ID, http.StatusConflict},
	} {
		if got := void(c.id); got != c.want {
			t.Errorf("%s: void status = %d, want %d", name, got, c.want)
		}
	}

	var status string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT status FROM invoices WHERE id = $1`, f.reconciled.ID,
	).Scan(&status); err != nil || status != "open" {
		t.Errorf("refused void changed the invoice: status=%q err=%v", status, err)
	}
}
