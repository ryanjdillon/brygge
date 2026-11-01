package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// vippsCSV is one synthetic Vipps charge in the settlement report export.
const vippsCSV = `Salgssted,MSN/Vippsnummer,Land,Betalingsløsning,Tidspunkt,Bokføringsdato,Type,Beløp,Balanse,Gebyr,Nettobeløp,Valuta,Kundens navn,Kundens telefonnummer,Melding,Kategori,PSP-referanse,Ordre-ID/Referanse,Utbetalingsnummer,Bankkonto for utbetaling,Planlagt utbetalingsdato
Synthetic Club,000009,Norge,Valgfritt beløp,2026-06-03 10:00:00,2026-06-03,Belastning,777.00,777.00,-10.00,767.00,NOK,Synthetic Payer,+47 00000000,Synthetic,,psp-handler-1,,,,
`

func multipartCSV(t *testing.T, csv string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "vipps.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(csv)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, w.FormDataContentType()
}

// withinTenSeconds runs fn and fails the test, instead of hanging, when it has
// not returned in ten seconds.
func withinTenSeconds(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not return within 10 s", what)
	}
}

// While another Vipps booking holds the club's booking slot, Synk Vipps
// answers 409 vipps_booking_busy after the person's bounded wait and books
// nothing, and a Vipps CSV upload keeps its import and answers
// booking_deferred. Once the slot is free both run normally.
func TestVippsBookingSlotBusyAnswersPeople(t *testing.T) {
	var svc *accounting.Service
	env := newAliasTestEnv(t, func(r chi.Router, s *accounting.Service, as *audit.Service) {
		svc = s
		ah := NewAccountingHandler(s, as, nil, nil, nil, zerolog.Nop())
		r.Post("/vipps-resync", ah.HandleResyncVipps)
		r.Post("/vipps-import", ah.HandleImportVippsSettlement)
	})
	club := env.club
	ctx := context.Background()
	if _, err := club.Pool.Exec(ctx, `UPDATE clubs SET vipps_per_charge_enabled = true WHERE id = $1`, club.ID); err != nil {
		t.Fatalf("enable per-charge: %v", err)
	}
	svc.SetVippsBookingSlotWait(150 * time.Millisecond)

	// A long booking in flight: an order-linked charge whose faktura a test
	// transaction has locked, so the booking holds the slot while it waits.
	inv := club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := club.NewOrderForInvoice(t, inv)
	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, OrderID: order.VippsReference})
	lock := testutil.HoldRowLock(t, club.Pool, `SELECT id FROM invoices WHERE id = $1 FOR UPDATE`, inv.ID)
	holder := make(chan error, 1)
	go func() {
		_, err := svc.BookVippsQueue(ctx, club.ID, club.Treasurer.ID, 30*time.Second)
		holder <- err
	}()
	lock.WaitForBlocked(30 * time.Second)

	// A bypassed slot would queue these behind the blocked booking; the bound
	// makes that a failure instead of a hang.
	var rec *httptest.ResponseRecorder
	withinTenSeconds(t, "Synk Vipps while the slot is held", func() {
		rec = env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-resync", nil)
	})
	wantRefusalCode(t, rec, vippsBookingBusyCode)

	body, contentType := multipartCSV(t, vippsCSV)
	req := httptest.NewRequest(http.MethodPost, "/vipps-import", body)
	req.Header.Set("Content-Type", contentType)
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
	up := httptest.NewRecorder()
	withinTenSeconds(t, "upload while the slot is held", func() { env.r.ServeHTTP(up, req) })
	var got map[string]any
	if up.Code != http.StatusCreated || json.Unmarshal(up.Body.Bytes(), &got) != nil {
		t.Fatalf("upload while busy = %d %s, want 201", up.Code, up.Body.String())
	}
	if got["booking_deferred"] != true || got["imported"] != float64(1) {
		t.Errorf("upload body = %v, want the row imported and booking_deferred", got)
	}
	var unbooked int
	if err := club.Pool.QueryRow(ctx,
		`SELECT count(*) FROM vipps_import_rows WHERE club_id = $1 AND psp_ref = 'psp-handler-1' AND journal_entry_id IS NULL`, club.ID,
	).Scan(&unbooked); err != nil || unbooked != 1 {
		t.Errorf("uploaded row unbooked = %d (err %v), want it imported and waiting", unbooked, err)
	}
	var bookedCharge *string
	if err := club.Pool.QueryRow(ctx, `SELECT journal_entry_id::text FROM vipps_import_rows WHERE id = $1`, charge).Scan(&bookedCharge); err != nil || bookedCharge != nil {
		t.Fatalf("the in-flight charge was booked while its faktura was locked (%v, %v)", bookedCharge, err)
	}

	lock.Release()
	select {
	case err := <-holder:
		if err != nil {
			t.Fatalf("in-flight booking: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("in-flight booking never finished")
	}

	ok := env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-resync", nil)
	if ok.Code != http.StatusOK {
		t.Errorf("Synk Vipps after the slot is free = %d %s, want 200", ok.Code, ok.Body.String())
	}
}
