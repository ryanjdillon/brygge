package finance

import "fmt"

// GenerateKID creates a KID number with a Modulo 10 (Luhn) check digit.
// Format: {clubPrefix}{memberSeq}{invoiceSeq}{checkDigit}
// Example: GenerateKID("000", 123, 1) → "00001230011"
func GenerateKID(clubPrefix string, memberSeq, invoiceSeq int) string {
	base := fmt.Sprintf("%s%05d%03d", clubPrefix, memberSeq, invoiceSeq)
	return base + luhnCheckDigit(base)
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

// ValidateKID verifies that a KID number has a valid Luhn check digit.
func ValidateKID(kid string) bool {
	if len(kid) < 2 {
		return false
	}
	base := kid[:len(kid)-1]
	expected := luhnCheckDigit(base)
	return kid[len(kid)-1:] == expected
}
