package finance

import "testing"

func TestLuhnCheckDigitStandardVector(t *testing.T) {
	// Standard Luhn test: "7992739871" should have check digit "3"
	got := luhnCheckDigit("7992739871")
	if got != "3" {
		t.Errorf("luhnCheckDigit(%q) = %q, want %q", "7992739871", got, "3")
	}
}

func TestGenerateKIDRoundTrip(t *testing.T) {
	tests := []struct {
		prefix     string
		memberSeq  int
		invoiceSeq int
	}{
		{"000", 1, 1},
		{"000", 123, 1},
		{"000", 123, 17},
		{"001", 1, 1},
		{"000", 99999, 999},
		{"999", 42, 7},
	}

	for _, tt := range tests {
		kid := GenerateKID(tt.prefix, tt.memberSeq, tt.invoiceSeq)
		t.Run(kid, func(t *testing.T) {
			if !ValidateKID(kid) {
				t.Errorf("GenerateKID(%q, %d, %d) = %q — ValidateKID returned false",
					tt.prefix, tt.memberSeq, tt.invoiceSeq, kid)
			}
			if len(kid) < 10 {
				t.Errorf("KID too short: %q", kid)
			}
		})
	}
}

func TestValidateKIDInvalid(t *testing.T) {
	valid := GenerateKID("000", 123, 1)

	// Corrupt last digit
	corrupted := valid[:len(valid)-1] + "0"
	if corrupted == valid {
		corrupted = valid[:len(valid)-1] + "1"
	}
	if ValidateKID(corrupted) {
		t.Errorf("ValidateKID(%q) = true for corrupted KID (original: %q)", corrupted, valid)
	}

	if ValidateKID("") {
		t.Error("ValidateKID empty string should be false")
	}
	if ValidateKID("1") {
		t.Error("ValidateKID single char should be false")
	}
}

func TestGenerateKIDUniqueness(t *testing.T) {
	k1 := GenerateKID("000", 1, 1)
	k2 := GenerateKID("000", 1, 2)
	k3 := GenerateKID("000", 2, 1)
	if k1 == k2 || k1 == k3 || k2 == k3 {
		t.Errorf("KIDs should be unique: %q, %q, %q", k1, k2, k3)
	}
}
