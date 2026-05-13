package accounting

import (
	"os"
	"testing"
)

// Smoke test against a real Vipps export. Skipped when the sample file is
// not present (it's outside the repo so we don't ship customer data).
func TestParseVippsCSVRealSample(t *testing.T) {
	path := os.Getenv("VIPPS_SAMPLE_CSV")
	if path == "" {
		t.Skip("VIPPS_SAMPLE_CSV not set, skipping real-sample test")
	}
	f, err := os.Open(path) // #nosec G304 -- test-only, opt-in via env var
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	rows, err := ParseVippsCSV(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected rows, got 0")
	}

	var nBelastning, nFee, nPayout, nOther int
	for _, r := range rows {
		switch r.RowType {
		case VippsRowBelastning:
			nBelastning++
		case VippsRowFee:
			nFee++
		case VippsRowPayout:
			nPayout++
		default:
			nOther++
		}
	}
	t.Logf("parsed %d rows: %d belastning, %d fee, %d payout, %d other",
		len(rows), nBelastning, nFee, nPayout, nOther)
	if nBelastning == 0 {
		t.Errorf("expected at least one Belastning row in real sample")
	}
}
