package audit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// Migration 000127 rewrites every settings-update audit row to the names of
// the fields set, whatever shape it has, drops the secret and the board
// contact details with the values, leaves rows already in that form, rows
// that are not objects and other actions alone, and is safe to run twice.
func TestIntegration_Migration000127RewritesSettingsRowsToFieldNames(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)

	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000127_scrub_secret_settings_audit.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	const settings = "update_financial_settings"
	type row struct{ id, details, want string }
	var rows []row
	seed := func(action, details, want string) {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO audit_log (club_id, action, resource, resource_id, details)
			 VALUES ($1, $2, 'clubs', $3, $4::jsonb) RETURNING id`, clubID, action, clubID, details).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", action, err)
		}
		rows = append(rows, row{id, details, want})
	}

	// The shape the handler wrote: the whole request under "new".
	seed(settings, `{"new":{"vipps_per_charge_enabled":true,"anthropic_api_key":"sk-ant-synthetic-0001","chairman_email":"chair-synthetic@example.invalid","phone":"+4700000001","bank_account":"12345678903","vipps_auto_assign":true,"vipps_auto_assign_tie_category":"slip_fee"}}`,
		`{"fields":["anthropic_api_key","bank_account","chairman_email","phone","vipps_auto_assign","vipps_auto_assign_tie_category","vipps_per_charge_enabled"]}`)
	seed(settings, `{"new":{"anthropic_api_key":"sk-ant-synthetic-0002"}}`, `{"fields":["anthropic_api_key"]}`)
	seed(settings, `{"new":{"treasurer_email":"treasurer-synthetic@example.invalid"},"old":{"treasurer_email":"old-synthetic@example.invalid"}}`,
		`{"fields":["treasurer_email"]}`)
	// A row whose keys are the fields themselves.
	seed(settings, `{"anthropic_api_key":"sk-ant-synthetic-0003","address":"1 Synthetic Road"}`, `{"fields":["address","anthropic_api_key"]}`)
	seed(settings, `{"new":{}}`, `{"fields":[]}`)
	seed(settings, `{}`, `{"fields":[]}`)
	seed(settings, `{"new":"anthropic_api_key"}`, `{"fields":[]}`)
	// Already the new form, not an object, or another action: unchanged.
	seed(settings, `{"fields":["anthropic_api_key","phone"]}`, `{"fields":["anthropic_api_key","phone"]}`)
	seed(settings, `{"fields":[]}`, `{"fields":[]}`)
	seed(settings, `"anthropic_api_key"`, `"anthropic_api_key"`)
	seed(settings, `[{"new":{"phone":"+4700000002"}}]`, `[{"new":{"phone":"+4700000002"}}]`)
	seed("update_booking_settings", `{"new":{"hoist_open_hour":8}}`, `{"new":{"hoist_open_hour":8}}`)
	seed("update_other_settings", `{"new":{"anthropic_api_key":"sk-ant-synthetic-0004"}}`, `{"new":{"anthropic_api_key":"sk-ant-synthetic-0004"}}`)

	// A row with no details at all.
	var nullID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO audit_log (club_id, action, resource, resource_id)
		 VALUES ($1, $2, 'clubs', $3) RETURNING id`, clubID, settings, clubID).Scan(&nullID); err != nil {
		t.Fatalf("seed null details: %v", err)
	}

	for run := 1; run <= 2; run++ {
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		for _, r := range rows {
			var same bool
			if err := pool.QueryRow(ctx,
				`SELECT details = $2::jsonb FROM audit_log WHERE id = $1`, r.id, r.want).Scan(&same); err != nil {
				t.Fatalf("run %d: read %s: %v", run, r.id, err)
			}
			if !same {
				var got string
				_ = pool.QueryRow(ctx, `SELECT details::text FROM audit_log WHERE id = $1`, r.id).Scan(&got)
				t.Errorf("run %d: %s became %s, want %s", run, r.details, got, r.want)
			}
		}
		var isNull bool
		if err := pool.QueryRow(ctx, `SELECT details IS NULL FROM audit_log WHERE id = $1`, nullID).Scan(&isNull); err != nil || !isNull {
			t.Errorf("run %d: a row without details changed (%v)", run, err)
		}
	}

	// No settings row keeps a value: nothing of the secret, the addresses or
	// the numbers survives anywhere in the settings rows.
	var leaked int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		  WHERE action = $1 AND details::text ~ '(sk-ant|synthetic|\+47000|12345678903|Synthetic Road)'`, settings).Scan(&leaked); err != nil {
		t.Fatalf("count leaks: %v", err)
	}
	// Only the untouched array row (a non-object) still names a phone number.
	if leaked != 1 {
		t.Errorf("%d settings rows still carry a value, want only the non-object row", leaked)
	}
}
