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

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestPriceItemDeleteArchiveAndAudit(t *testing.T) {
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := "22222222-2222-2222-2222-" + testutil.RandomHex(6)
	userID := "11111111-1111-1111-1111-" + testutil.RandomHex(6)
	mustExec(t, db, ctx, "INSERT INTO clubs (id, slug, name) VALUES ($1, $2, 'Synthetic Club')", clubID, "s-"+testutil.RandomHex(4))
	mustExec(t, db, ctx, "INSERT INTO users (id, club_id, email) VALUES ($1, $2, $3)", userID, clubID, testutil.RandomHex(4)+"@example.test")
	claims := &auth.Claims{UserID: userID, ClubID: clubID, Roles: []string{"treasurer"}}

	h := NewPriceItemsHandler(db, testConfig(), audit.NewService(db, zerolog.Nop()), zerolog.Nop())
	do := func(fn http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Method(method, "/admin/pricing", fn)
		r.Method(method, "/admin/pricing/{itemID}", fn)
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	create := func(name string) string {
		rec := do(h.HandleCreate, http.MethodPost, "/admin/pricing",
			`{"name":"`+name+`","category":"service","amount":100}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: status %d (%s)", name, rec.Code, rec.Body.String())
		}
		var item struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		return item.ID
	}
	auditCount := func(action, itemID string) int {
		var n int
		if err := db.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE action = $1 AND resource_id = $2`, action, itemID,
		).Scan(&n); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		return n
	}

	t.Run("unreferenced item is deleted and audited", func(t *testing.T) {
		id := create("Unused service")
		if auditCount(audit.ActionPricingCreated, id) != 1 {
			t.Errorf("create not audited")
		}
		if rec := do(h.HandleDelete, http.MethodDelete, "/admin/pricing/"+id, ""); rec.Code != http.StatusOK {
			t.Fatalf("delete: status %d (%s)", rec.Code, rec.Body.String())
		}
		var n int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM price_items WHERE id = $1`, id).Scan(&n); err != nil || n != 0 {
			t.Fatalf("item still present (n=%d, err=%v)", n, err)
		}
		if auditCount(audit.ActionPricingDeleted, id) != 1 {
			t.Errorf("delete not audited")
		}
	})

	t.Run("referenced item returns 409 and is left intact", func(t *testing.T) {
		id := create("Used service")
		invoiceID := "33333333-3333-3333-3333-" + testutil.RandomHex(6)
		mustExec(t, db, ctx,
			`INSERT INTO invoices (id, club_id, user_id, total_amount, due_date, kid_number)
			 VALUES ($1, $2, $3, 100, CURRENT_DATE + 14, $4)`,
			invoiceID, clubID, userID, "9"+testutil.RandomHex(4))
		mustExec(t, db, ctx,
			`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total,
			                            price_item_id, category)
			 VALUES ($1, 'Synthetic line', 1, 100, 100, $2::uuid,
			         (SELECT category FROM price_items WHERE id = $2::uuid))`,
			invoiceID, id)

		rec := do(h.HandleDelete, http.MethodDelete, "/admin/pricing/"+id, "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("delete referenced: status %d, want 409 (%s)", rec.Code, rec.Body.String())
		}
		var body struct {
			Code       string              `json:"code"`
			References priceItemReferences `json:"references"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode 409: %v", err)
		}
		if body.Code != priceItemInUseCode || body.References.InvoiceLines != 1 {
			t.Errorf("409 body = %+v", body)
		}
		var n int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM price_items WHERE id = $1`, id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("referenced item was removed (n=%d, err=%v)", n, err)
		}
		if auditCount(audit.ActionPricingDeleted, id) != 0 {
			t.Errorf("refused delete was audited as deleted")
		}

		// Archiving through update hides it from the public list and is
		// audited as an archive, not a plain update.
		upd := do(h.HandleUpdate, http.MethodPut, "/admin/pricing/"+id,
			`{"name":"Used service","category":"service","amount":100,"is_active":false}`)
		if upd.Code != http.StatusOK {
			t.Fatalf("archive: status %d (%s)", upd.Code, upd.Body.String())
		}
		if auditCount(audit.ActionPricingArchived, id) != 1 {
			t.Errorf("archive not audited as %s", audit.ActionPricingArchived)
		}
		var active bool
		if err := db.QueryRow(ctx, `SELECT is_active FROM price_items WHERE id = $1`, id).Scan(&active); err != nil || active {
			t.Errorf("item still active after archive (active=%v, err=%v)", active, err)
		}
	})

	t.Run("archived item stops a QR target from charging", func(t *testing.T) {
		id := create("QR service")
		token := "tok-" + testutil.RandomHex(8)
		mustExec(t, db, ctx,
			`INSERT INTO harbor_payment_targets (club_id, token, label, kind, price_item_id)
			 VALUES ($1, $2, 'Synthetic poster', 'service', $3)`,
			clubID, token, id)
		qr := &InvoiceHandler{db: db}
		if _, ok := qr.loadQRTarget(ctx, token); !ok {
			t.Fatalf("active item: QR target should resolve")
		}

		if rec := do(h.HandleDelete, http.MethodDelete, "/admin/pricing/"+id, ""); rec.Code != http.StatusConflict {
			t.Fatalf("delete with QR target: status %d, want 409", rec.Code)
		}
		upd := do(h.HandleUpdate, http.MethodPut, "/admin/pricing/"+id,
			`{"name":"QR service","category":"service","amount":100,"is_active":false}`)
		if upd.Code != http.StatusOK {
			t.Fatalf("archive: status %d (%s)", upd.Code, upd.Body.String())
		}
		if _, ok := qr.loadQRTarget(ctx, token); ok {
			t.Errorf("archived item: QR target should no longer resolve")
		}
	})

	t.Run("price change is audited with before and after", func(t *testing.T) {
		id := create("Repriced service")
		rec := do(h.HandleUpdate, http.MethodPut, "/admin/pricing/"+id,
			`{"name":"Repriced service","category":"service","amount":150}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("update: status %d (%s)", rec.Code, rec.Body.String())
		}
		var before, after float64
		if err := db.QueryRow(ctx,
			`SELECT (details->>'amount_before')::numeric, (details->>'amount_after')::numeric
			   FROM audit_log WHERE action = $1 AND resource_id = $2`,
			audit.ActionPricingUpdated, id,
		).Scan(&before, &after); err != nil {
			t.Fatalf("read update audit: %v", err)
		}
		if before != 100 || after != 150 {
			t.Errorf("audit amounts = %v -> %v, want 100 -> 150", before, after)
		}
	})

	t.Run("unknown item is 404", func(t *testing.T) {
		rec := do(h.HandleDelete, http.MethodDelete, "/admin/pricing/44444444-4444-4444-4444-444444444444", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404", rec.Code)
		}
	})
}
