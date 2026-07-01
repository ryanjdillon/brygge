package finance

import (
	"fmt"
	"testing"
)

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

// Every generated mod-11 KID must validate under mod-11, across a range that
// exercises the control-digit==10 skip path.
func TestMod11GenerateValidatesRoundTrip(t *testing.T) {
	for member := 1; member <= 300; member++ {
		kid, err := GenerateKIDMethod("000", member, 1, KIDMethodMod11)
		if err != nil {
			t.Fatalf("GenerateKIDMethod mod11 member=%d: %v", member, err)
		}
		if !ValidateKIDMethod(kid, KIDMethodMod11) {
			t.Errorf("member=%d KID %q does not validate under mod-11", member, kid)
		}
		if len(kid) != 12 {
			t.Errorf("member=%d KID %q wrong length", member, kid)
		}
	}
}

// When a base's mod-11 control digit would be 10 (unusable), the generator must
// advance the invoiceSeq component to a valid KID rather than emit an invalid one.
func TestMod11SkipsControlDigitTen(t *testing.T) {
	found := false
	for member := 1; member <= 2000; member++ {
		base := fmt.Sprintf("%s%05d%03d", "000", member, 1)
		if _, ok := mod11CheckDigit(base); ok {
			continue
		}
		found = true
		kid, err := GenerateKIDMethod("000", member, 1, KIDMethodMod11)
		if err != nil {
			t.Fatalf("member=%d: %v", member, err)
		}
		if !ValidateKIDMethod(kid, KIDMethodMod11) {
			t.Errorf("member=%d advanced KID %q invalid under mod-11", member, kid)
		}
		if kid[8:11] == "001" {
			t.Errorf("member=%d expected invoiceSeq bump past 001, got %q", member, kid)
		}
		break
	}
	if !found {
		t.Skip("no control-digit==10 base found in range")
	}
}

// The real Trond/Preben KIDs are mod-10 valid but mod-11 invalid; their numbers
// must regenerate to mod-11-valid KIDs, and ValidateKIDAny accepts both schemes.
func TestValidateKIDAnyAndRealCases(t *testing.T) {
	if !ValidateKIDMethod("000000740019", KIDMethodMod10) || ValidateKIDMethod("000000740019", KIDMethodMod11) {
		t.Error("Trond KID should be mod10-valid, mod11-invalid")
	}
	if !ValidateKIDMethod("000001050012", KIDMethodMod10) || ValidateKIDMethod("000001050012", KIDMethodMod11) {
		t.Error("Preben KID should be mod10-valid, mod11-invalid")
	}
	for _, member := range []int{74, 105} {
		kid, err := GenerateKIDMethod("000", member, 1, KIDMethodMod11)
		if err != nil {
			t.Fatalf("member=%d: %v", member, err)
		}
		if !ValidateKIDMethod(kid, KIDMethodMod11) {
			t.Errorf("member=%d regenerated mod-11 KID %q invalid", member, kid)
		}
		if !ValidateKIDAny(kid) {
			t.Errorf("ValidateKIDAny rejected mod-11 KID %q", kid)
		}
	}
	if !ValidateKIDAny("000000740019") {
		t.Error("ValidateKIDAny should accept a historical mod-10 KID")
	}
}
