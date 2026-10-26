package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{
			name:   "untrusted peer ignores every forwarding header",
			remote: "203.0.113.7:51000",
			headers: map[string]string{
				"X-Forwarded-For": "198.51.100.1",
				"X-Real-IP":       "198.51.100.2",
				"True-Client-IP":  "198.51.100.3",
			},
			want: "203.0.113.7",
		},
		{
			name:    "trusted peer ignores True-Client-IP and X-Real-IP",
			remote:  "127.0.0.1:40000",
			headers: map[string]string{"X-Real-IP": "198.51.100.2", "True-Client-IP": "198.51.100.3"},
			want:    "127.0.0.1",
		},
		{
			name:    "trusted peer takes the client from X-Forwarded-For",
			remote:  "127.0.0.1:40000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			name:    "client-prepended entries are skipped",
			remote:  "127.0.0.1:40000",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.9, 203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			name:    "trusted hops on the right are skipped",
			remote:  "127.0.0.1:40000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7, 127.0.0.2"},
			want:    "203.0.113.7",
		},
		{
			name:    "malformed hop falls back to the peer",
			remote:  "127.0.0.1:40000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7, not-an-ip"},
			want:    "127.0.0.1",
		},
		{
			name:   "no header gives the peer without its port",
			remote: "127.0.0.1:40000",
			want:   "127.0.0.1",
		},
		{
			name:    "empty segments are skipped",
			remote:  "127.0.0.1:40000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7,,127.0.0.2,"},
			want:    "203.0.113.7",
		},
		{
			name:    "host:port entry",
			remote:  "127.0.0.1:40000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7:6000"},
			want:    "203.0.113.7",
		},
		{
			name:    "only trusted hops falls back to the peer",
			remote:  "127.0.0.1:40000",
			headers: map[string]string{"X-Forwarded-For": "127.0.0.2, ::1"},
			want:    "127.0.0.1",
		},
		{
			name:    "IPv4-mapped loopback peer is trusted",
			remote:  "[::ffff:127.0.0.1]:40000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			name:    "zone is dropped from a header hop",
			remote:  "[::1]:40000",
			headers: map[string]string{"X-Forwarded-For": "fe80::1%eth0"},
			want:    "fe80::1",
		},
		{
			name:    "IPv6 peer and client",
			remote:  "[::1]:40000",
			headers: map[string]string{"X-Forwarded-For": "2001:db8::7"},
			want:    "2001:db8::7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			var got string
			ClientIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			})).ServeHTTP(httptest.NewRecorder(), req)
			if got != tt.want {
				t.Errorf("RemoteAddr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientIPMultipleHeaderLines(t *testing.T) {
	trusted, _ := ParseTrustedProxies("")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Add("X-Forwarded-For", "198.51.100.9")
	req.Header.Add("X-Forwarded-For", "203.0.113.7, 127.0.0.2")
	var got string
	ClientIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	})).ServeHTTP(httptest.NewRecorder(), req)
	if got != "203.0.113.7" {
		t.Errorf("RemoteAddr = %q, want 203.0.113.7", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies(" 172.16.0.0/12 , 10.0.0.0/8 ")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v; want 2 prefixes", got, err)
	}
	if _, err := ParseTrustedProxies("172.16.0.0/12,bogus"); err == nil {
		t.Error("want error for malformed CIDR")
	}
	if _, err := ParseTrustedProxies("::ffff:127.0.0.0/104"); err == nil {
		t.Error("want error for an IPv4-mapped prefix")
	}
	if got, err := ParseTrustedProxies(" , ,"); err != nil || len(got) != 2 {
		t.Errorf("only separators: got %v, %v; want the loopback default", got, err)
	}
}
