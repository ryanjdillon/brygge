package paymentfilters

import (
	"strconv"
	"strings"
	"unicode"
)

// slugMaxLen is the longest slug the table accepts (pfg_slug_format).
const slugMaxLen = 40

// reservedSlugs cannot be given to an item group: "all" is the SPA's no-filter
// chip, "harbor" a legacy alias that expands to guest and motorhome, and
// "faktura" and "other" are the built-ins. "alle" is the same chip in Norwegian.
var reservedSlugs = map[string]bool{"all": true, "alle": true, "harbor": true, BuiltinFaktura: true, BuiltinOther: true}

// Slugify turns a group name into a slug: lowercase, Norwegian and German
// letters transliterated, anything else outside [a-z0-9] a single hyphen, no
// leading or trailing hyphen, at most 40 characters, "group" when nothing is
// left. It is the id in ?kind= links, so it is set once at create.
func Slugify(name string) string {
	var b strings.Builder
	lastHyphen := true // swallows a leading hyphen
	write := func(s string) {
		b.WriteString(s)
		lastHyphen = false
	}
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			write(string(r))
		case r == 'æ':
			write("ae")
		case r == 'ø', r == 'ö':
			write("o")
		case r == 'å', r == 'ä':
			write("a")
		case r == 'ü':
			write("u")
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r):
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	s := trimSlug(b.String())
	if s == "" {
		return "group"
	}
	return s
}

// trimSlug cuts a slug to the column limit and strips edge hyphens.
func trimSlug(s string) string {
	if len(s) > slugMaxLen {
		s = s[:slugMaxLen]
	}
	return strings.Trim(s, "-")
}

// uniqueSlug returns base, or base-2, base-3, ... when the slug is taken,
// shortening base so the suffix still fits the limit.
func uniqueSlug(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		cut := base
		if len(cut)+len(suffix) > slugMaxLen {
			cut = trimSlug(cut[:slugMaxLen-len(suffix)])
		}
		if s := cut + suffix; !taken[s] {
			return s
		}
	}
}
