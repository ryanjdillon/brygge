package finance

import (
	"testing"
	"time"
)

func TestSettledPredicates(t *testing.T) {
	if got := SettledForMemberSQL("i"); got != "(i.payment_id IS NOT NULL OR i.vipps_paid_at IS NOT NULL OR i.status = 'voided')" {
		t.Errorf("SettledForMemberSQL(i) = %s", got)
	}
	if got := SettledForGLSQL(""); got != "(payment_id IS NOT NULL)" {
		t.Errorf("SettledForGLSQL() = %s", got)
	}

	pid := "p1"
	now := time.Now()
	cases := []struct {
		name   string
		status string
		pid    *string
		vipps  *time.Time
		want   bool
	}{
		{"open unpaid", "open", nil, nil, false},
		{"reconciled", "open", &pid, nil, true},
		{"vipps captured, awaiting settlement", "open", nil, &now, true},
		{"voided", "voided", nil, nil, true},
	}
	for _, c := range cases {
		if got := SettledForMember(c.status, c.pid, c.vipps); got != c.want {
			t.Errorf("%s: SettledForMember = %v, want %v", c.name, got, c.want)
		}
	}
}
