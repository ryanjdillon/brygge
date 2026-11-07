package handlers

import "strings"

// normalizePhone returns raw as an E.164 number, or false when it is not a
// plausible one. Spaces, dashes, dots and brackets (also around the country
// code, as in "(+47)") are dropped and a leading 00 is read as +. A number without a country code must be Norwegian: eight
// digits, not starting with 0 or 1, which gets +47. A +47 number must be
// exactly that after the code; any other country code takes 8 to 15 digits in
// all, as E.164 allows. These are deliberately simple rules rather than a
// full numbering-plan library: they catch typos and missing digits, which is
// what the join form needs. frontend/src/lib/phone.ts mirrors them.
func normalizePhone(raw string) (string, bool) {
	n, ok := phoneDigits(raw)
	if !ok {
		return "", false
	}
	if strings.HasPrefix(n, "00") {
		n = "+" + n[2:]
	}
	if !strings.HasPrefix(n, "+") {
		if !isNorwegianNational(n) {
			return "", false
		}
		return "+47" + n, true
	}
	digits := n[1:]
	if strings.HasPrefix(digits, "47") {
		if !isNorwegianNational(digits[2:]) {
			return "", false
		}
		return n, true
	}
	if len(digits) < 8 || len(digits) > 15 || digits[0] == '0' {
		return "", false
	}
	return n, true
}

// phoneDigits keeps the digits of raw and a + before any digit (so "(+47)"
// works), dropping separators; false on any other character.
func phoneDigits(raw string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= '0' && r <= '9', r == '+' && b.Len() == 0:
			b.WriteRune(r)
		case strings.ContainsRune("  -.()", r):
		default:
			return "", false
		}
	}
	return b.String(), true
}

// isNorwegianNational is an eight-digit Norwegian number: none starts with 0
// or 1.
func isNorwegianNational(d string) bool {
	return len(d) == 8 && d[0] >= '2' && d[0] <= '9'
}
