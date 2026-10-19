package finance

import "testing"

// The line's stored category must come first: the item's is only the
// fallback for lines that never stored one.
func TestLineCategorySQLIsLineFirst(t *testing.T) {
	if got := LineCategorySQL("il", "pi"); got != "COALESCE(il.category, pi.category)" {
		t.Errorf("LineCategorySQL(il, pi) = %s", got)
	}
}
