package main

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/handlers"
)

// codeCrossSiteRequest is the error a refused cross-site request gets.
const codeCrossSiteRequest = "cross_site_request"

// crossOriginBypass lists the /api/v1 routes that other sites or servers
// call on purpose, so crossOriginGuard lets them through. Each has its own
// authentication: a provider signature, the per-member unsubscribe token,
// or a developer bearer token. Only a caller that sends a browser-like Origin
// or Sec-Fetch-Site needs an entry (a server that sends neither passes
// anyway); see docs/developer/checklists/add-route.md. Never list a route
// that a session cookie authenticates: for the same-site sibling subdomains
// (webmail, matrix) the SameSite=Lax cookie is sent, and this guard is the
// only thing that stops them.
var crossOriginBypass = []string{
	// Payment provider webhooks, verified by their HMAC signatures.
	"POST /api/v1/vipps/webhooks",
	"POST /api/v1/stripe/webhooks",
	// RFC 8058 one-click unsubscribe, POSTed by mail providers.
	"POST /api/v1/unsubscribe",
	// Developer bearer-token query (BRY-115); cookies are ignored there.
	"/api/v1/dev/query",
}

// crossOriginGuard is the first layer against cross-site request forgery on
// /api/v1 (BRY-660): Go's http.CrossOriginProtection. It refuses a POST, PUT,
// PATCH or DELETE that a browser marks as coming from another origin
// (Sec-Fetch-Site cross-site or same-site, or for browsers without that
// header an Origin whose host is not the request's), unless the Origin is
// cfg.FrontendURL or the route is in crossOriginBypass. Requests carrying
// neither header (servers, curl, old clients) and GET, HEAD and OPTIONS pass.
// A sibling subdomain (webmail, matrix) is same-site, not same-origin, and is
// refused too. The refusal is a JSON 403 {"error":"cross_site_request"}.
func crossOriginGuard(cfg *config.Config, log zerolog.Logger) func(http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	if origin := originOf(cfg.FrontendURL); origin != "" {
		if err := cop.AddTrustedOrigin(origin); err != nil {
			log.Warn().Err(err).Str("origin", origin).Msg("cross-origin guard: frontend origin not trusted")
		}
	}
	for _, pattern := range crossOriginBypass {
		cop.AddInsecureBypassPattern(pattern)
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Warn().Str("method", r.Method).Str("path", r.URL.Path).Msg("cross-site request refused")
		handlers.Error(w, http.StatusForbidden, codeCrossSiteRequest)
	}))
	return cop.Handler
}

// originOf returns rawURL's origin (scheme://host[:port]), or "" when it has
// none. FRONTEND_URL is configured as an origin, but a trailing slash or path
// must not make AddTrustedOrigin reject it. A default port (:443 for https,
// :80 for http) is dropped, since a browser's Origin header never carries
// one and CrossOriginProtection compares the strings exactly.
func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := u.Host
	if port := u.Port(); (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		host = strings.TrimSuffix(host, ":"+port)
	}
	return scheme + "://" + host
}
