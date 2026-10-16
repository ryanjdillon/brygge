package accounting

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestBankRowKindClauses(t *testing.T) {
	cases := []struct {
		kind          string
		wantClauses   int // including the dismissed_at condition
		wantDismissed bool
		wantErr       bool
	}{
		{"", 1, false, false},
		{"all", 1, false, false},
		{"incoming", 2, false, false},
		{"incoming,bank,duplicate", 4, false, false},
		{"dismissed,vipps", 2, true, false},
		{" outgoing , double_payment ", 3, false, false},
		{"incoming,bogus", 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			got, err := bankRowKindClauses(c.kind)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if len(got) != c.wantClauses {
				t.Errorf("clauses = %d, want %d: %v", len(got), c.wantClauses, got)
			}
			if strings.Contains(got[0], "IS NOT NULL") != c.wantDismissed {
				t.Errorf("dismissed condition = %q, want dismissed=%v", got[0], c.wantDismissed)
			}
		})
	}
}

// Filters from different groups narrow the list together: direction AND
// source AND flags.
func TestListUnmatchedBankRowsCombinedKinds(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	svc := NewService(pool, nil, zerolog.Nop())

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'x.csv', 'sparebank-norge-v1', '1920', $2, 0) RETURNING id`,
		clubID, userID,
	).Scan(&importID); err != nil {
		t.Fatalf("insert import: %v", err)
	}

	d := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	if _, err := svc.ImportBankRows(ctx, clubID, importID, "", []BankRow{
		{Date: d, Description: "Overførsel", Amount: 450, Reference: "IN-BANK"},
		{Date: d, Description: "Betaling", Amount: -200, Reference: "OUT-BANK"},
		{Date: d, Description: "Utb. 2000001 Vippsnr 123456", Amount: 300, Reference: "IN-VIPPS"},
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	refs := func(kind string) []string {
		t.Helper()
		rows, err := svc.ListUnmatchedBankRows(ctx, clubID, kind, "", 0, 50, 0)
		if err != nil {
			t.Fatalf("list %q: %v", kind, err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Reference)
		}
		sort.Strings(out)
		return out
	}

	for kind, want := range map[string]string{
		"":               "IN-BANK,IN-VIPPS,OUT-BANK",
		"incoming":       "IN-BANK,IN-VIPPS",
		"bank":           "IN-BANK,OUT-BANK",
		"incoming,bank":  "IN-BANK",
		"incoming,vipps": "IN-VIPPS",
		"outgoing,vipps": "",
	} {
		if got := strings.Join(refs(kind), ","); got != want {
			t.Errorf("kind %q = %q, want %q", kind, got, want)
		}
	}
}
