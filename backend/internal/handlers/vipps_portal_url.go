package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const (
	// vippsPortalHost is the only host the club's portal address may point at.
	vippsPortalHost = "portal.vippsmobilepay.com"
	// maxVippsPortalURLLen caps the stored address; a portal page address is
	// a short path.
	maxVippsPortalURLLen = 200
	// codeInvalidVippsPortalURL is the typed 400 the settings update answers
	// for an address that is not a Vipps portal address.
	codeInvalidVippsPortalURL = "invalid_vipps_portal_url"
)

// normalizeVippsPortalURL checks the address of the club's page in the Vipps
// portal and returns it trimmed. The portal path carries an identifier of the
// portal's own, so the address is typed in by the treasurer rather than built
// from config; it is rendered as a link, so it must be an https address on the
// portal's host and nothing else: no userinfo, port, fragment, whitespace,
// control or invisible format characters (a zero-width space or a right-to-left
// override makes an address read differently from what it is). An empty value
// is valid and means "no link".
func normalizeVippsPortalURL(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", true
	}
	if len(s) > maxVippsPortalURLLen || strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\\' || r == '#'
	}) {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", false
	}
	// Host is compared as typed, port included, so a port or a trailing dot
	// never matches.
	if u.Scheme != "https" || u.User != nil || u.Host != vippsPortalHost || u.Opaque != "" {
		return "", false
	}
	return s, true
}

// writeInvalidVippsPortalURL answers the typed 400 for a refused address.
func writeInvalidVippsPortalURL(w http.ResponseWriter) {
	JSON(w, http.StatusBadRequest, map[string]string{
		"error": "vipps_portal_url must be an https address on " + vippsPortalHost,
		"code":  codeInvalidVippsPortalURL,
	})
}
