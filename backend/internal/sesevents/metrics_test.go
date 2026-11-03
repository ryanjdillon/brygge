package sesevents

import "testing"

func TestSMTPStatusClass(t *testing.T) {
	tests := map[string]string{
		"5.1.1":   "5.1",
		"4.2.2":   "4.2",
		"2.6.0":   "2.6",
		"5.7.1":   "5.7",
		"5.7":     "5.7",
		"":        smtpStatusNone,
		"550":     smtpStatusNone,
		"5.9.1":   smtpStatusNone,
		"3.1.1":   smtpStatusNone,
		"5.12.1":  smtpStatusNone,
		"garbage": smtpStatusNone,
	}
	for in, want := range tests {
		if got := smtpStatusClass(in); got != want {
			t.Errorf("smtpStatusClass(%q) = %q, want %q", in, got, want)
		}
	}
}
