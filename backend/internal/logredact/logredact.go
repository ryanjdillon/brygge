// Package logredact blanks bearer tokens that ride in a URL path so a log line
// never holds a credential (invariant: no bearer token in a URL is written to
// any log). Anything that logs a request path goes through Path.
package logredact

import "regexp"

// pathTokenRoute matches the token segment of every token-addressed URL, with
// or without the /api/v1 prefix (the SPA pages share the shape). It mirrors
// pathTokenRoutes in nix/caddy-log.nix; keep the two in step. `betal/bruker`
// comes before `betal` so the payables token, not the word "bruker", is the
// segment that is redacted.
var pathTokenRoute = regexp.MustCompile(`(/(?:api/v1/)?(?:betal/bruker|betal|checkout|qr|join/confirm)/)[^/?&#]+`)

// Path returns path with every token segment replaced by REDACTED.
func Path(path string) string {
	return pathTokenRoute.ReplaceAllString(path, "${1}REDACTED")
}
