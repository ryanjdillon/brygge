package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		addr    string
		private bool
	}{
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2001:4860:4860::8888", false},
		{"::ffff:8.8.8.8", false},

		{"127.0.0.1", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"::ffff:192.168.1.1", true},
		{"169.254.169.254", true},
		{"fe80::1", true},
		{"fe80::1%eth0", true},
		{"fd00::1", true},
		{"100.64.0.1", true},
		{"100.127.255.254", true},
		{"0.0.0.0", true},
		{"0.1.2.3", true},
		{"::", true},
		{"192.0.0.8", true},
		{"198.18.0.1", true},
		{"198.19.255.255", true},
		{"240.0.0.1", true},
		{"255.255.255.255", true},
		{"224.0.0.1", true},
		{"ff02::1", true},
		{"64:ff9b::7f00:1", true},
		{"64:ff9b::808:808", true},

		{"", true},
		{"localhost", true},
		{"example.com", true},
		{"not-an-ip", true},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := isPrivateIP(tt.addr); got != tt.private {
				t.Errorf("isPrivateIP(%q) = %v, want %v", tt.addr, got, tt.private)
			}
		})
	}
}

// A hostname that passes no up-front check must still be refused once it
// resolves to loopback: the guard has to act on the dialled IP.
func TestImageProxyClientRefusesLoopbackByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = "localhost:" + u.Port()

	resp, err := newImageProxyClient().Get(u.String())
	if err == nil {
		resp.Body.Close()
		t.Fatalf("GET %s succeeded; want dial refused", u)
	}
	if !errors.Is(err, errForbiddenDial) {
		t.Fatalf("GET %s error = %v; want errForbiddenDial", u, err)
	}
}

// Control sees resolved ip:port pairs, including those of redirect hops
// and Happy-Eyeballs fallbacks, so checking the hook directly covers them.
func TestRefuseNonPublicDial(t *testing.T) {
	tests := []struct {
		address string
		refused bool
	}{
		{"127.0.0.1:80", true},
		{"[::1]:443", true},
		{"[::ffff:10.0.0.1]:80", true},
		{"169.254.169.254:80", true},
		{"no-port", true},
		{"93.184.216.34:443", false},
		{"[2606:2800:220:1:248:1893:25c8:1946]:443", false},
	}
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			err := refuseNonPublicDial("tcp", tt.address, nil)
			if got := errors.Is(err, errForbiddenDial); got != tt.refused {
				t.Errorf("refuseNonPublicDial(%q) = %v; refused=%v, want %v", tt.address, err, got, tt.refused)
			}
		})
	}
}
