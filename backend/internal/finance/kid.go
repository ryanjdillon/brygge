package finance

import (
	"fmt"
	"strconv"
)

// KID check-digit methods. Norwegian OCR (KID) agreements register one of
// these with the bank; the payer's bank rejects a KID ("feil KID") that
// doesn't validate under the registered method.
const (
	KIDMethodMod10 = "mod10" // Luhn
	KIDMethodMod11 = "mod11"
)

// GenerateKID creates a KID number with a Modulo 10 (Luhn) check digit.
// Format: {clubPrefix}{memberSeq}{invoiceSeq}{checkDigit}
// Example: GenerateKID("000", 123, 1) → "00001230011"
//
// Retained for back-compatibility and as the mod-10 path; new callers should
// prefer GenerateKIDMethod with the club's configured method.
func GenerateKID(clubPrefix string, memberSeq, invoiceSeq int) string {
	base := fmt.Sprintf("%s%05d%03d", clubPrefix, memberSeq, invoiceSeq)
	return base + luhnCheckDigit(base)
}

// GenerateKIDMethod builds a KID using the club's configured check-digit
// method. Modulo 11 is the common Norwegian OCR default; Modulo 10 (Luhn) is
// kept for clubs whose agreement uses it and for historical KIDs.
//
// Under Modulo 11 a base can be "unusable" (the control digit would be 10);
// when that happens we advance the trailing invoiceSeq component and recompute,
// so the issued KID is always valid and still unique per (memberSeq, invoiceSeq).
func GenerateKIDMethod(clubPrefix string, memberSeq, invoiceSeq int, method string) (string, error) {
	switch method {
	case KIDMethodMod10, "":
		return GenerateKID(clubPrefix, memberSeq, invoiceSeq), nil
	case KIDMethodMod11:
		for seq := invoiceSeq; seq < invoiceSeq+1000; seq++ {
			base := fmt.Sprintf("%s%05d%03d", clubPrefix, memberSeq, seq)
			if check, ok := mod11CheckDigit(base); ok {
				return base + check, nil
			}
		}
		return "", fmt.Errorf("no valid mod-11 KID for prefix=%s member=%d", clubPrefix, memberSeq)
	default:
		return "", fmt.Errorf("unknown KID check method %q", method)
	}
}

// luhnCheckDigit calculates the Modulo 10 (Luhn) check digit for a numeric string.
func luhnCheckDigit(s string) string {
	sum := 0
	// Process from right to left, doubling every other digit
	for i := len(s) - 1; i >= 0; i-- {
		d := int(s[i] - '0')
		if (len(s)-i)%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	check := (10 - (sum % 10)) % 10
	return fmt.Sprintf("%d", check)
}

// mod11CheckDigit calculates the Modulo 11 control digit for a numeric string
// using weights 2,3,4,5,6,7 cycling from the right. Returns ok=false when the
// control digit would be 10 — an invalid KID under mod-11 that must be skipped
// by advancing the sequence.
func mod11CheckDigit(s string) (string, bool) {
	sum := 0
	weight := 2
	for i := len(s) - 1; i >= 0; i-- {
		sum += int(s[i]-'0') * weight
		weight++
		if weight > 7 {
			weight = 2
		}
	}
	check := (11 - (sum % 11)) % 11
	if check == 10 {
		return "", false
	}
	return strconv.Itoa(check), true
}

// ValidateKID verifies that a KID number has a valid Luhn (Modulo 10) check
// digit. Retained for back-compatibility; reconciliation should use
// ValidateKIDAny so both mod-10 (historical) and mod-11 (new) KIDs validate.
func ValidateKID(kid string) bool {
	return ValidateKIDMethod(kid, KIDMethodMod10)
}

// ValidateKIDMethod verifies a KID against a specific check-digit method.
func ValidateKIDMethod(kid, method string) bool {
	if len(kid) < 2 {
		return false
	}
	base := kid[:len(kid)-1]
	last := kid[len(kid)-1:]
	switch method {
	case KIDMethodMod10, "":
		return luhnCheckDigit(base) == last
	case KIDMethodMod11:
		check, ok := mod11CheckDigit(base)
		return ok && check == last
	default:
		return false
	}
}

// ValidateKIDAny accepts a KID that is valid under either method. Used by
// bank reconciliation so historical mod-10 KIDs and new mod-11 KIDs both match.
func ValidateKIDAny(kid string) bool {
	return ValidateKIDMethod(kid, KIDMethodMod10) || ValidateKIDMethod(kid, KIDMethodMod11)
}
