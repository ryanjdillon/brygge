package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// importedSnapshot is everything a refused mutation must leave untouched on
// an imported faktura.
type importedSnapshot struct {
	Status string
	SentAt *time.Time
	KID    string
	PDF    []byte
	S3Key  string
}

func (f *creditFixture) importedRow(t *testing.T, inv fin.Invoice) importedSnapshot {
	t.Helper()
	var s importedSnapshot
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT status, sent_at, COALESCE(kid_number, ''), pdf_data, COALESCE(s3_key, '')
		   FROM invoices WHERE id = $1`, inv.ID,
	).Scan(&s.Status, &s.SentAt, &s.KID, &s.PDF, &s.S3Key); err != nil {
		t.Fatalf("load faktura %d: %v", inv.Number, err)
	}
	return s
}

// serveInvoiceMutation runs one of the invoice mutation endpoints that
// creditFixture's router doesn't mount.
func (f *creditFixture) serveInvoiceMutation(method, path string, body any) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Delete("/invoices/{invoiceID}", f.h.HandleDeleteInvoice)
	r.Post("/invoices/{invoiceID}/send", f.h.HandleSendInvoice)
	r.Post("/invoices/{invoiceID}/void", f.h.HandleVoidInvoice)
	r.Post("/invoices/bulk-regenerate-pdf", f.h.HandleBulkRegeneratePDF)
	r.Post("/invoices/bulk-reissue-kid", f.h.HandleBulkReissueKID)
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req = req.WithContext(middleware.WithClaims(req.Context(), f.claims))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// newImportedFaktura is an open imported faktura carrying a stored PDF, so a
// send or regenerate that slipped past the guard would have what it needs to
// change it.
func (f *creditFixture) newImportedFaktura(t *testing.T) fin.Invoice {
	t.Helper()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Imported: true,
		IssueDate: time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)})
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET pdf_data = $2 WHERE id = $1`, inv.ID, []byte("%PDF-1.4 original")); err != nil {
		t.Fatalf("store pdf: %v", err)
	}
	return inv
}

// Imported fakturas are the previous system's record: delete, void and send
// refuse them with code "imported", and the row is unchanged.
func TestImportedFakturaRefusesDeleteVoidSend(t *testing.T) {
	f := newCreditFixture(t)
	inv := f.newImportedFaktura(t)
	before := f.importedRow(t, inv)

	for _, tc := range []struct {
		name, method, path string
	}{
		{"delete", http.MethodDelete, "/invoices/" + inv.ID},
		{"void", http.MethodPost, "/invoices/" + inv.ID + "/void"},
		{"send", http.MethodPost, "/invoices/" + inv.ID + "/send"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantCode(t, f.serveInvoiceMutation(tc.method, tc.path, nil), http.StatusConflict, "imported")
		})
	}

	after := f.importedRow(t, inv)
	if after.Status != before.Status || after.SentAt != nil || after.KID != before.KID ||
		!bytes.Equal(after.PDF, before.PDF) || after.S3Key != before.S3Key {
		t.Errorf("imported faktura changed by refused mutations: before %+v, after %+v", before, after)
	}
	if mock, ok := f.h.email.(*email.MockSender); !ok || len(mock.Calls) != 0 {
		t.Errorf("an email went out for an imported faktura (mock %v)", ok)
	}
}

// An imported faktura keeps its previous-system status, so even one that is
// already "voided" there is refused rather than answered with the idempotent
// 204, and can't be deleted the way a voided Brygge faktura can.
func TestImportedVoidedFakturaRefusedNotIdempotent(t *testing.T) {
	f := newCreditFixture(t)
	inv := f.newImportedFaktura(t)
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET status = 'voided' WHERE id = $1`, inv.ID); err != nil {
		t.Fatalf("mark voided: %v", err)
	}
	wantCode(t, f.serveInvoiceMutation(http.MethodPost, "/invoices/"+inv.ID+"/void", nil), http.StatusConflict, "imported")
	wantCode(t, f.serveInvoiceMutation(http.MethodDelete, "/invoices/"+inv.ID, nil), http.StatusConflict, "imported")
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM invoices WHERE id = $1`, inv.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("imported faktura deleted (count %d, err %v)", n, err)
	}
}

// The bulk PDF actions skip imported fakturas: no Brygge PDF or KID replaces
// the original.
func TestImportedFakturaSkippedByBulkPDFActions(t *testing.T) {
	f := newCreditFixture(t)
	inv := f.newImportedFaktura(t)

	// A sent_at on the row keeps the reissue leg from passing on its unsent
	// skip: imported rows must be skipped for being imported.
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET sent_at = now() - interval '1 year' WHERE id = $1`, inv.ID); err != nil {
		t.Fatalf("stamp sent_at: %v", err)
	}
	before := f.importedRow(t, inv)

	for _, path := range []string{"/invoices/bulk-regenerate-pdf", "/invoices/bulk-reissue-kid"} {
		t.Run(path, func(t *testing.T) {
			rec := f.serveInvoiceMutation(http.MethodPost, path, map[string]any{"ids": []string{inv.ID}})
			wantCode(t, rec, http.StatusOK, "")
			var res bulkInvoiceResult
			if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if res.Processed != 0 || res.Skipped != 1 || len(res.Failures) != 0 {
				t.Errorf("result = %+v, want one skipped", res)
			}
		})
	}

	after := f.importedRow(t, inv)
	if after.KID != before.KID || !bytes.Equal(after.PDF, before.PDF) || after.S3Key != before.S3Key {
		t.Errorf("bulk action changed the imported faktura: before %+v, after %+v", before, after)
	}
	var archived int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM invoice_pdf_archive WHERE invoice_id = $1`, inv.ID).Scan(&archived); err != nil {
		t.Fatalf("archive count: %v", err)
	}
	if archived != 0 {
		t.Errorf("%d PDFs archived for a skipped imported faktura", archived)
	}
}
