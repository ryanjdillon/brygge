package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// The bank auto-assign preview is readable by board readers behind the
// accounting gate, writes nothing, and names no payer.
func TestAutoAssignPreviewForBoardReader(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	h := NewBankRowsHandler(svc, nil, zerolog.Nop())
	ctx := context.Background()

	m := club.NewMember(t, "member")
	if _, err := pool.Exec(ctx, `UPDATE users SET first_name = 'Synthetic', last_name = 'Forhandsvisning' WHERE id = $1`, m.ID); err != nil {
		t.Fatalf("name member: %v", err)
	}
	inv := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000})
	imp := club.NewBankImport(t)
	row := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000, Counterpart: "Synthetic Forhandsvisning", FromAccount: "12345600017"})

	r := chi.NewRouter()
	r.With(middleware.RequireRoleOrBoardRead("treasurer", "board", "admin")).
		Get("/bank-rows/auto-assign/preview", h.HandleAutoAssignPreview)
	serve := func(roles ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/bank-rows/auto-assign/preview", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: m.ID, ClubID: club.ID, Roles: roles}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := serve("member"); rec.Code != http.StatusForbidden {
		t.Errorf("member: status %d, want 403", rec.Code)
	}
	rec := serve("board")
	if rec.Code != http.StatusOK {
		t.Fatalf("board: status %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "Forhandsvisning") {
		t.Errorf("preview names the payer: %s", rec.Body)
	}
	var p accounting.BankAutoAssignPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Enabled || p.WouldAssign != 1 || len(p.Items) != 1 || p.Items[0].RowID != row || p.Items[0].InvoiceNumber != inv.Number {
		t.Fatalf("preview = %+v, want one deposit for faktura %d with the switch off", p, inv.Number)
	}
	var booked bool
	if err := pool.QueryRow(ctx, `SELECT journal_entry_id IS NOT NULL FROM bank_import_rows WHERE id = $1`, row).Scan(&booked); err != nil || booked {
		t.Errorf("preview booked the row (booked %v, err %v)", booked, err)
	}
}
