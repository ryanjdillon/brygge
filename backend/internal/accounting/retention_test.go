package accounting

import (
	"testing"
	"time"
)

func TestRetentionClearsAt(t *testing.T) {
	cases := []struct {
		name   string
		record time.Time
		want   time.Time
	}{
		{
			name:   "record in 2026 clears 2032-01-01",
			record: time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
			want:   time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "december record uses its own year, not the next",
			record: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
			want:   time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:   "january record clears five full years later",
			record: time.Date(2020, time.January, 2, 0, 0, 0, 0, time.UTC),
			want:   time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retentionClearsAt(tc.record); !got.Equal(tc.want) {
				t.Errorf("retentionClearsAt(%s) = %s, want %s", tc.record, got, tc.want)
			}
		})
	}
}

// A record whose clears-at is in the past is out of retention; one in the
// future is still retained. This pins the boundary semantics used by
// MemberFinancialRetention.Eligible.
func TestRetentionEligibilityBoundary(t *testing.T) {
	past := retentionClearsAt(time.Date(2000, time.June, 1, 0, 0, 0, 0, time.UTC))
	if time.Now().Before(past) {
		t.Errorf("a record from 2000 should be out of retention by now (clears %s)", past)
	}
	future := retentionClearsAt(time.Now())
	if !time.Now().Before(future) {
		t.Errorf("a record from this year should still be retained (clears %s)", future)
	}
}
