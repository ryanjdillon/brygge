package accounting

// fallbackRevenueCode is where revenue lands when nothing names its account:
// an unmapped category, an invoice residual or line without a category, and
// an unmatched Vipps charge. 3900 (Andre inntekter) keeps unexplained money
// visible for reclassification instead of mixing it into 3100 dues; it is
// never 2900 (Annen kortsiktig gjeld), which would say the club owes the
// payer (DIL-367).
const fallbackRevenueCode = "3900"

// revenueAccountMap is the one category → revenue account map for every
// payment path (invoice sync, Vipps per-charge, Vipps auto-assign, order
// split), so a price item books to the same account however it was paid.
// It holds both the payment_type enum values and the price_items.category
// strings: the two share harbor_membership and slip_fee verbatim, and
// membership↔dues and booking↔guest diverge in name only.
var revenueAccountMap = map[string]string{
	// payment_type enum values
	"dues":        "3100", // Medlemskontingent
	"booking":     "3200", // Gjestehavninntekter
	"merchandise": "3300", // Salgs- og tjenesteinntekter

	// price_items.category values
	"membership":        "3100", // Medlemskontingent
	"harbor_membership": "3110", // Havneavgift
	"slip_fee":          "3120", // Plassleie
	"electricity":       "3130", // Strøm
	"seasonal_rental":   "3120", // Plassleie (sesong)
	"guest":             "3200", // Gjestehavninntekter
	"motorhome":         "3210", // Bobil/campingplass
	"room_hire":         "3220", // Lokalleie
	"service":           "3300", // Salgs- og tjenesteinntekter
	"other":             "3900", // Andre inntekter
}

// RevenueAccount returns the revenue account a category books to and whether
// the category is mapped. Paths that book without a human looking (Vipps
// auto-assign) or that show a default (price hints) use this form so an
// unknown category is refused rather than guessed.
func RevenueAccount(category string) (string, bool) {
	code, ok := revenueAccountMap[category]
	return code, ok
}

// RevenueAccountOrFallback returns the revenue account for a category, or
// fallbackRevenueCode when the category is not mapped. Paths that must book
// the money regardless (invoice sync, Vipps per-charge, order split) use it.
func RevenueAccountOrFallback(category string) string {
	if code, ok := revenueAccountMap[category]; ok {
		return code
	}
	return fallbackRevenueCode
}
