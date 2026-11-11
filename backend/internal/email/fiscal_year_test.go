package email

import (
	"strings"
	"testing"
	"time"
)

func TestFiscalYearWaitingCopy(t *testing.T) {
	oldest := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name, locale string
		n            FiscalYearWaiting
		subject      string
		body         []string
		never        []string
	}{
		{
			name: "missing, one payment, bokmål", locale: "nb",
			n:       FiscalYearWaiting{Year: 2027, State: FiscalYearMissing, Payments: 1, Oldest: oldest, URL: "https://x.invalid/a?openPeriod=2027"},
			subject: "Opprett regnskapsåret 2027",
			body:    []string{"1 betaling datert i 2027 venter", "02.01.2027", "Opprett regnskapsåret 2027", "?openPeriod=2027"},
		},
		{
			name: "closed, many payments, nynorsk", locale: "nn",
			n:       FiscalYearWaiting{Year: 2025, State: FiscalYearClosed, Payments: 3, URL: "https://x.invalid/a?reopenPeriod=p"},
			subject: "Rekneskapsåret 2025 er lukka – 3 betalingar ventar",
			body:    []string{"3 betalingar datert i 2025 ventar", "lukk året igjen etterpå"},
			never:   []string{"Den eldste"},
		},
		{
			name: "locked, one payment", locale: "nb",
			n:       FiscalYearWaiting{Year: 2024, State: FiscalYearLocked, Payments: 1, URL: "https://x.invalid/a?period=p"},
			subject: "Regnskapsåret 2024 er låst – 1 betaling venter",
			body:    []string{"låses opp utenfor appen", "Vis regnskapsåret 2024"},
			never:   []string{"Gjenåpne", "Tildel"},
		},
		{
			name: "unknown locale falls back to bokmål", locale: "en",
			n:       FiscalYearWaiting{Year: 2027, State: FiscalYearMissing, Payments: 2},
			subject: "Opprett regnskapsåret 2027",
			body:    []string{"2 betalinger datert i 2027"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FiscalYearWaitingSubject(tc.locale, tc.n); got != tc.subject {
				t.Errorf("subject = %q, want %q", got, tc.subject)
			}
			body := FiscalYearWaitingBody(tc.locale, tc.n)
			for _, w := range tc.body {
				if !strings.Contains(body, w) {
					t.Errorf("body lacks %q", w)
				}
			}
			for _, n := range tc.never {
				if strings.Contains(body, n) {
					t.Errorf("body has %q", n)
				}
			}
		})
	}
}
