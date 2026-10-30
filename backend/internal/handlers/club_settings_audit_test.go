package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func TestSuppliedSettingFields(t *testing.T) {
	s, on := "x", true
	got := suppliedSettingFields(updateFinancialSettingsRequest{
		VippsPortalURL: &s, AnthropicAPIKey: &s, BankAutoAssign: &on, Phone: &s,
	})
	want := []string{"anthropic_api_key", "bank_auto_assign", "phone", "vipps_portal_url"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
	if got := suppliedSettingFields(updateFinancialSettingsRequest{}); got == nil || len(got) != 0 {
		t.Errorf("empty request = %#v, want an empty list", got)
	}
}

// The settings update records which fields changed and nothing they were set
// to: a secret or a board member's address in the request never reaches
// audit_log.
func TestFinancialSettingsAuditHoldsNoValues(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	club := fin.NewClub(t, pool)
	h := &ClubSettingsHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}

	const (
		secret = "sk-ant-synthetic-secret-0001"
		mail   = "synthetic-chair@example.invalid"
		phone  = "+4700000001"
	)
	body := `{"anthropic_api_key":"` + secret + `","chairman_email":"` + mail + `","phone":"` + phone + `",` +
		`"vipps_portal_url":"https://portal.vippsmobilepay.com/100200/home","bank_auto_assign":true}`
	req := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
	rec := httptest.NewRecorder()
	h.HandleUpdateFinancialSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	ctx := context.Background()
	var details []byte
	var resource, resourceID string
	if err := pool.QueryRow(ctx,
		`SELECT details, resource, resource_id FROM audit_log WHERE club_id = $1 AND action = $2`,
		club.ID, audit.ActionFinancialSettingsUpdated,
	).Scan(&details, &resource, &resourceID); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if audit.ActionFinancialSettingsUpdated != "update_financial_settings" {
		t.Errorf("the shipped action string changed to %q", audit.ActionFinancialSettingsUpdated)
	}
	if resource != "clubs" || resourceID != club.ID {
		t.Errorf("resource = %q %q, want clubs and the club id", resource, resourceID)
	}
	for _, leaked := range []string{secret, mail, phone, "100200"} {
		if strings.Contains(string(details), leaked) {
			t.Errorf("audit details carry %q: %s", leaked, details)
		}
	}
	var got struct {
		Fields []string `json:"fields"`
	}
	if err := json.Unmarshal(details, &got); err != nil {
		t.Fatalf("details: %v (%s)", err, details)
	}
	want := []string{"anthropic_api_key", "bank_auto_assign", "chairman_email", "phone", "vipps_portal_url"}
	if !reflect.DeepEqual(got.Fields, want) {
		t.Errorf("fields = %v, want %v", got.Fields, want)
	}
	// The key was still saved: only the log keeps no value.
	var hasKey bool
	if err := pool.QueryRow(ctx,
		`SELECT anthropic_api_key = $2 FROM clubs WHERE id = $1`, club.ID, secret).Scan(&hasKey); err != nil || !hasKey {
		t.Errorf("anthropic_api_key not saved (%v, %v)", hasKey, err)
	}
}
