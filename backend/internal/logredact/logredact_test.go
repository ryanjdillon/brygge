package logredact

import "testing"

func TestPath(t *testing.T) {
	cases := map[string]string{
		"/api/v1/betal/abc123":             "/api/v1/betal/REDACTED",
		"/api/v1/betal/bruker/abc123":      "/api/v1/betal/bruker/REDACTED",
		"/betal/abc123/kvittering":         "/betal/REDACTED/kvittering",
		"/api/v1/checkout/abc123/lines/42": "/api/v1/checkout/REDACTED/lines/42",
		"/api/v1/checkout/abc123/pay":      "/api/v1/checkout/REDACTED/pay",
		"/api/v1/qr/abc123/pay":            "/api/v1/qr/REDACTED/pay",
		"/qr/abc123":                       "/qr/REDACTED",
		"/api/v1/join/confirm/abc123":      "/api/v1/join/confirm/REDACTED",
		"/join/confirm":                    "/join/confirm",
		"/api/v1/auth/verify":              "/api/v1/auth/verify",
		"/api/v1/members":                  "/api/v1/members",
		"/":                                "/",
	}
	for in, want := range cases {
		if got := Path(in); got != want {
			t.Errorf("Path(%q) = %q, want %q", in, got, want)
		}
	}
}
