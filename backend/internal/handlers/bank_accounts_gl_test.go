package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// TestBankAccountCreateAutoSeedsGLAccount locks in DIL-390: saving a bank
// account whose gl_code isn't yet in the chart of accounts must auto-create
// the asset account, so the journal_lines→accounts FK never makes KID
// auto-matching silently fail.
func TestBankAccountCreateAutoSeedsGLAccount(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"treasurer"})

	// Chart starts without 1935.
	var pre int
	if err := db.QueryRow(ctx,
		`SELECT count(*) FROM accounts WHERE club_id = $1 AND code = '1935'`, clubID,
	).Scan(&pre); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if pre != 0 {
		t.Fatalf("expected no 1935 account before create, got %d", pre)
	}

	h := NewBankAccountsHandler(db, zerolog.Nop())
	r := setupRoleProtectedRouter(http.MethodPost, "/", h.HandleCreate, "treasurer", "admin")

	token := generateTestToken(userID, clubID, []string{"treasurer"})
	body := `{"account_number":"1234.56.78901","role":"hoyrente","gl_code":"1935","label":"Høyrente"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// The 1935 asset account must now exist (auto-seeded).
	var name, accType string
	if err := db.QueryRow(ctx,
		`SELECT name, account_type::text FROM accounts WHERE club_id = $1 AND code = '1935'`, clubID,
	).Scan(&name, &accType); err != nil {
		t.Fatalf("expected auto-seeded 1935 account: %v", err)
	}
	if accType != "asset" {
		t.Errorf("account_type = %q, want asset", accType)
	}
	if name == "" {
		t.Error("auto-seeded account has empty name")
	}

	// And the bank account row references it.
	var glCode string
	if err := db.QueryRow(ctx,
		`SELECT gl_code FROM club_bank_accounts WHERE club_id = $1 AND gl_code = '1935'`, clubID,
	).Scan(&glCode); err != nil {
		t.Fatalf("bank account row not found with gl_code 1935: %v", err)
	}
}
