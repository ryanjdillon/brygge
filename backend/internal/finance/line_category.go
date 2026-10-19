package finance

import "fmt"

// LineCategorySQL returns the SQL expression for an issued invoice line's
// price category: the category stored on the line at issue, falling back to
// the price item's current category only for lines written before every
// creation path stored one. line and item are the invoice_lines and
// price_items table aliases; the price item must be LEFT JOINed.
//
// Re-categorising a price item must not change what an already-issued
// faktura books to or how it is counted, so every reader of an issued
// line's category goes through this one expression rather than reading
// price_items.category directly.
func LineCategorySQL(line, item string) string {
	return fmt.Sprintf("COALESCE(%scategory, %scategory)", col(line), col(item))
}
