package paymentfilters

// iconAllowList is every icon id a group may carry, in the order the editor
// shows them. The ids are kebab-case lucide names; each carries a fixed tint in
// the SPA (frontend/src/lib/paymentFilterIcons.ts), so groups have no colour
// setting. Keep the two lists in step: the OpenAPI enum on the group types is
// checked against this list, and the generated TypeScript type makes a missing
// icon in the SPA a compile error.
var iconAllowList = []string{
	"sailboat",
	"van",
	"forklift",
	"file-text",
	"store",
	"circle-dollar-sign",
	"anchor",
	"zap",
	"wrench",
	"tent",
	"key-round",
	"shopping-bag",
	"coffee",
	"ticket",
}

// IconAllowList returns the icon ids a group may carry.
func IconAllowList() []string {
	return append([]string(nil), iconAllowList...)
}

// ValidIcon reports whether id may be stored on a group.
func ValidIcon(id string) bool {
	for _, i := range iconAllowList {
		if i == id {
			return true
		}
	}
	return false
}

// Default icons of the two built-ins.
const (
	iconFaktura = "file-text"
	iconOther   = "circle-dollar-sign"
)
