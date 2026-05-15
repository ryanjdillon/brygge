package accounting

import (
	"regexp"
	"strings"

	"github.com/brygge-klubb/brygge/internal/finance"
)

// DNB and other Norwegian banks sometimes leave the dedicated KID
// column empty for incoming wire transfers and instead embed the
// payment reference inside the description ("Forklaring") string.
// We rescue it via these helpers so auto-match doesn't miss the
// row.
//
// Examples from a live export (kasserar's høyrente account):
//
//	"Mobilbank Dato 12.05.2026 Kl. 21.17.23 · Fakturanummer 51"
//	"Fra: Ryan James Dillon Betalt: 12.05.26 · 000000880013"
//	"Fra: Anette Rakli Børnes Betalt: 12.05.26 · 000000290015"
//	"Nettgiro I Dag"           (no payer info; manual reconcile)
//	"Overførsel"               (no payer info; manual reconcile)

// kidAfterBetalt matches `Betalt: <something> · <digits>` — the
// standard pattern for Norwegian online-banking debit references.
// The 6+ digit lower bound rules out trailing year fragments and
// the like; Brygge KIDs are 11 digits but we accept anything that
// would Luhn-validate at the call site.
var kidAfterBetalt = regexp.MustCompile(`Betalt:\s*\S+\s*(?:·|•|∙)\s*(\d{6,25})\b`)

// invoiceRef matches "Fakturanummer 51", "Faktura 51", "Faktura nr 51",
// case-insensitive, in the description.
var invoiceRef = regexp.MustCompile(`(?i)\bfaktura(?:nummer|nr\.?|\snr\.?)?\s*[:#]?\s*(\d{1,9})\b`)

// payerFromPrefix matches "Fra: <name> Betalt:" — captures the name
// segment between "Fra:" and "Betalt:".
var payerFromPrefix = regexp.MustCompile(`^Fra:\s*(.+?)\s+Betalt:`)

// ExtractKIDFromDescription pulls a KID out of the description tail
// when the CSV's KID column was empty. Validates with the Norwegian
// mod-10 (Luhn) check so we don't false-match arbitrary digit runs
// (e.g. a phone number).
func ExtractKIDFromDescription(desc string) string {
	if desc == "" {
		return ""
	}
	m := kidAfterBetalt.FindStringSubmatch(desc)
	if len(m) < 2 {
		return ""
	}
	candidate := m[1]
	if !finance.ValidateKID(candidate) {
		return ""
	}
	return candidate
}

// ExtractInvoiceNumberFromDescription pulls an "invoice number"
// reference (`Fakturanummer 51`) for the mobilbank fallback path
// where the payer typed the human-readable number rather than the
// full KID. Returns the digit string or "" if no match.
func ExtractInvoiceNumberFromDescription(desc string) string {
	if desc == "" {
		return ""
	}
	m := invoiceRef.FindStringSubmatch(desc)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// ExtractPayerFromDescription strips the "Fra: <name> Betalt:" prefix
// pattern. Useful when the bank's Motpart column carries the receiving
// account's own name ("Klokkarvik Båtlag") for every row, drowning
// the actual payer identity. Returns "" if the pattern doesn't match.
func ExtractPayerFromDescription(desc string) string {
	if desc == "" {
		return ""
	}
	m := payerFromPrefix.FindStringSubmatch(desc)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}
