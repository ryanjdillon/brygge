package accounting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// A missing client must reach the sync as a nil interface. A typed-nil
// *vipps.Client stored directly in the interface would be non-nil and slip past
// the "not configured" guard into a nil dereference.
func TestNewVippsReportSourceNilClient(t *testing.T) {
	var c *vipps.Client
	if src := NewVippsReportSource(c); src != nil {
		t.Fatalf("nil client produced a non-nil source %#v", src)
	}
	var typedNil VippsReportSource = c
	if typedNil == nil {
		t.Fatal("test premise broken: a typed-nil in the interface compares nil")
	}

	svc := NewService(nil, nil, zerolog.Nop())
	_, err := svc.SyncVippsReport(context.Background(), NewVippsReportSource(c), "club", "", "", "2026-10-25")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v, want the not configured refusal", err)
	}
}

func TestNewVippsReportSourceWrapsClient(t *testing.T) {
	c := vipps.New("http://127.0.0.1:0", "id", "secret", "sub", "123456", zerolog.Nop())
	if NewVippsReportSource(c) == nil {
		t.Fatal("a real client produced a nil source")
	}
}

// A day with no settled entries imports nothing and creates no import record
// (the service has no database here, so touching it would panic).
func TestSyncVippsReportDayWithNoEntries(t *testing.T) {
	fake := newFakeVippsReport("123456")
	svc := NewService(nil, nil, zerolog.Nop())

	res, err := svc.SyncVippsReport(context.Background(), fake, "club", "", "", "2026-10-24")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Rows != 0 || res.Imported != 0 || res.TryLater || res.ImportID != "" {
		t.Errorf("result = %+v, want an empty, final result", res)
	}
	if res.LedgerID != "900" {
		t.Errorf("ledger = %q, want the merchant's first ledger", res.LedgerID)
	}
	if len(fake.fundsCalls) != 1 || fake.fundsCalls[0] != "900/2026-10-24" ||
		len(fake.feesCalls) != 1 || fake.feesCalls[0] != "900/2026-10-24" {
		t.Errorf("calls funds=%v fees=%v, want one call each for 900/2026-10-24", fake.fundsCalls, fake.feesCalls)
	}
}

// An explicit ledger skips resolution; a not-yet-final report is reported as
// try-later with nothing written.
func TestSyncVippsReportTryLaterAndExplicitLedger(t *testing.T) {
	fake := newFakeVippsReport("123456")
	fake.ledgersErr = errors.New("ledgers must not be read")
	fake.tryLaterFees = true
	svc := NewService(nil, nil, zerolog.Nop())

	res, err := svc.SyncVippsReport(context.Background(), fake, "club", "", "777", "2026-10-25")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !res.TryLater || res.LedgerID != "777" || res.Rows != 0 {
		t.Errorf("result = %+v, want try_later on ledger 777", res)
	}
}

// Each Report API failure surfaces, wrapped with the step that failed.
func TestSyncVippsReportAPIErrorSurfaces(t *testing.T) {
	boom := errors.New("vipps unavailable")
	cases := []struct {
		name   string
		mutate func(*fakeVippsReport)
		prefix string
		wraps  bool
	}{
		{"ledgers", func(f *fakeVippsReport) { f.ledgersErr = boom }, "resolve ledger: ", true},
		{"no ledgers", func(f *fakeVippsReport) { f.ledgers = nil }, "no vipps ledgers for merchant", false},
		{"funds", func(f *fakeVippsReport) { f.fundsErr = boom }, "funds report: ", true},
		{"fees", func(f *fakeVippsReport) { f.feesErr = boom }, "fees report: ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeVippsReport("123456")
			tc.mutate(fake)
			svc := NewService(nil, nil, zerolog.Nop())

			res, err := svc.SyncVippsReport(context.Background(), fake, "club", "", "", "2026-10-25")
			if err == nil || !strings.HasPrefix(err.Error(), tc.prefix) {
				t.Fatalf("err = %v, want prefix %q", err, tc.prefix)
			}
			if tc.wraps && !errors.Is(err, boom) {
				t.Errorf("err = %v does not wrap the API error", err)
			}
			if res.Rows != 0 || res.Imported != 0 {
				t.Errorf("result = %+v, want nothing imported", res)
			}
		})
	}
}
