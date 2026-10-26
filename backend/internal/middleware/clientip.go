package middleware

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultTrustedProxies covers a reverse proxy on the same host, which is how
// the API is deployed behind Caddy.
const DefaultTrustedProxies = "127.0.0.0/8,::1/128"

// ParseTrustedProxies parses a comma-separated CIDR list. A list with no
// entries falls back to DefaultTrustedProxies; a malformed entry is an
// error so a typo can't silently stop the API recognising its proxy.
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", part, err)
		}
		// Addresses are unmapped before matching, so an IPv4-mapped
		// prefix would never match anything.
		if p.Addr().Is4In6() {
			return nil, fmt.Errorf("trusted proxy %q: write IPv4 ranges in IPv4 form", part)
		}
		out = append(out, p.Masked())
	}
	if len(out) == 0 {
		return ParseTrustedProxies(DefaultTrustedProxies)
	}
	return out, nil
}

// ClientIP sets r.RemoteAddr to the client's bare IP. Behind a trusted
// proxy that IP comes from X-Forwarded-For, walked from the right and
// skipping trusted hops, because entries to the left of the proxy's own are
// whatever the client sent. Otherwise it is the peer's IP with the port
// dropped, so rate-limit keys stay per-IP rather than per-connection.
// True-Client-IP and X-Real-IP are ignored; proxies pass them through from
// the client unchanged, so they can't be trusted.
func ClientIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip, ok := clientAddr(r, trusted); ok {
				r.RemoteAddr = ip.String()
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientAddr(r *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	peer, ok := parseAddr(r.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	if !isTrusted(peer, trusted) {
		return peer, true
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		raw := strings.TrimSpace(hops[i])
		if raw == "" {
			continue
		}
		hop, ok := parseAddr(raw)
		if !ok {
			// A garbled chain can't be walked safely; fall back to the proxy.
			return peer, true
		}
		hop = hop.WithZone("")
		if !isTrusted(hop, trusted) {
			return hop, true
		}
	}
	return peer, true
}

// parseAddr accepts a bare IP or host:port and unmaps IPv4-in-IPv6.
func parseAddr(s string) (netip.Addr, bool) {
	if s == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
