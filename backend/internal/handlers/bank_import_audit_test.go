package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// bankAuditCSV is one synthetic incoming giro dated in 2025, in the
// sparebank-norge-v1 export shape.
const bankAuditCSV = "\ufeffBokført;Transaksjonsdato;Rentedato;Kategori;Type;Beskrivelse;Melding;Beløp (NOK);Beløp (valuta);Valuta;Vekslingskurs;Arkivref.;Fra konto;Til konto;Kreditornavn;Debitornavn\n" +
	"02.06.2025;;02.06.2025;Innbetaling;OVERF;Synthetic giro;;450;450;NOK;1;990000669;77770066900;99990000669;;Synthetic Auditpayer\n"

// bankUploadForm is a multipart upload of csv with the given form fields.
func bankUploadForm(t *testing.T, csv string, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := w.CreateFormFile("file", "synthetic-bank.csv")
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

// BRY-669: every bank import path writes the keys audit-actions.md documents,
// including the closed years and the period a person forced the rows into.
func TestBankImportAuditRecordsEveryKey(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, s *accounting.Service, as *audit.Service) {
		ah := NewAccountingHandler(s, as, nil, nil, nil, zerolog.Nop())
		r.Post("/bank-import", ah.HandleImportBankStatement)
		r.Post("/bank-reimport-account", ah.HandleReimportAccountBankStatement)
		r.Post("/bank-imports/{importID}/reimport", ah.HandleReimportBankStatement)
	})
	club := env.club
	ctx := context.Background()
	if _, err := club.Pool.Exec(ctx, `UPDATE clubs SET bank_auto_assign = true WHERE id = $1`, club.ID); err != nil {
		t.Fatal(err)
	}
	var closed string
	if err := club.Pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status, closed_at)
		 VALUES ($1, 2025, '2025-01-01', '2025-12-31', 'closed', now()) RETURNING id`, club.ID).Scan(&closed); err != nil {
		t.Fatalf("closed period: %v", err)
	}

	upload := func(path string, fields map[string]string) map[string]any {
		t.Helper()
		body, ct := bankUploadForm(t, bankAuditCSV, fields)
		req := httptest.NewRequest(http.MethodPost, path, body)
		req.Header.Set("Content-Type", ct)
		req = req.WithContext(middleware.WithClaims(req.Context(),
			&auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
		rec := httptest.NewRecorder()
		env.r.ServeHTTP(rec, req)
		var got map[string]any
		if (rec.Code != http.StatusCreated && rec.Code != http.StatusOK) || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body.String())
		}
		return got
	}
	years := func(v any) []string {
		raw, _ := v.([]any)
		out := []string{}
		for _, y := range raw {
			s, _ := y.(string)
			out = append(out, s)
		}
		return out
	}
	wantKeys := func(t *testing.T, what string, d map[string]any, keys []string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := d[k]; !ok {
				t.Errorf("%s audit row lacks %q: %v", what, k, d)
			}
		}
	}
	importKeys := []string{
		"filename", "format", "bank_account_code", "rows_total", "imported", "skipped_dup", "matched",
		"transfers", "closed_periods", "missing_periods", "period_override", "deleted", "skipped_booked",
		"row_errors", "kid_skipped_settled", "refreshed", "superseded", "auto_assigned", "stripe_booked",
		"stripe_payouts_cleared", "vipps_payouts_cleared", "declined_skipped", "booking_deferred",
	}

	// A plain upload: the 2025 row waits for the closed year.
	first := upload("/bank-import", map[string]string{"format": "sparebank-norge-v1", "bank_account_code": "1920"})
	if got := years(first["closed_periods"]); !slices.Equal(got, []string{"2025"}) {
		t.Errorf("upload closed_periods = %v, want [2025]", got)
	}
	rows := env.audit("accounting.bank_imported")
	if len(rows) != 1 {
		t.Fatalf("%d bank_imported rows, want 1", len(rows))
	}
	wantKeys(t, "bank_imported", rows[0], importKeys)
	if got := years(rows[0]["closed_periods"]); !slices.Equal(got, []string{"2025"}) {
		t.Errorf("bank_imported closed_periods = %v, want [2025]", got)
	}
	if rows[0]["period_override"] != "" || rows[0]["filename"] != "synthetic-bank.csv" || rows[0]["bank_account_code"] != "1920" {
		t.Errorf("bank_imported = %v, want no override, the filename and 1920", rows[0])
	}

	// A full-account reconcile into the closed period: the override is logged.
	upload("/bank-reimport-account", map[string]string{"format": "sparebank-norge-v1", "bank_account_code": "1920", "period_id": closed})
	rows = env.audit("accounting.bank_account_reimported")
	if len(rows) != 1 {
		t.Fatalf("%d bank_account_reimported rows, want 1", len(rows))
	}
	wantKeys(t, "bank_account_reimported", rows[0], importKeys)
	if rows[0]["period_override"] != closed {
		t.Errorf("bank_account_reimported period_override = %v, want %s", rows[0]["period_override"], closed)
	}
	if got := years(rows[0]["closed_periods"]); !slices.Equal(got, []string{"2025"}) {
		t.Errorf("bank_account_reimported closed_periods = %v, want [2025]", got)
	}

	// A corrected upload for one import.
	importID, _ := first["id"].(string)
	resp := upload("/bank-imports/"+importID+"/reimport", map[string]string{"period_id": closed})
	for _, k := range []string{"transfers", "closed_periods", "missing_periods"} {
		if _, ok := resp[k]; !ok {
			t.Errorf("reimport response lacks %q: %v", k, resp)
		}
	}
	rows = env.audit("accounting.bank_reimported")
	if len(rows) != 1 {
		t.Fatalf("%d bank_reimported rows, want 1", len(rows))
	}
	wantKeys(t, "bank_reimported", rows[0], []string{
		"filename", "format", "deleted", "reimported", "matched", "skipped_dup", "skipped_booked", "auto_assigned",
		"transfers", "closed_periods", "missing_periods", "row_errors", "kid_skipped_settled", "period_override",
	})
	if rows[0]["period_override"] != closed {
		t.Errorf("bank_reimported period_override = %v, want %s", rows[0]["period_override"], closed)
	}
}
