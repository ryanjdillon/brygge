package handlers

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// recordingPushClient is the push service as webpush-go's HTTPClient: it
// records the request and answers with a fixed status and body.
type recordingPushClient struct {
	req    *http.Request
	status int
	body   string
}

func (c *recordingPushClient) Do(req *http.Request) (*http.Response, error) {
	c.req = req
	return &http.Response{
		StatusCode: c.status,
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Header:     http.Header{},
	}, nil
}

// testSubscription is a browser-shaped subscription with a real P-256 key, so
// webpush-go can encrypt to it.
func testSubscription(t *testing.T, endpoint string) pushSubscriptionRow {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("auth secret: %v", err)
	}
	return pushSubscriptionRow{
		Endpoint: endpoint,
		P256dh:   base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

// vapidClaims decodes the claims of the VAPID JWT in an Authorization header
// ("vapid t=<jwt>, k=<key>").
func vapidClaims(t *testing.T, header string) map[string]any {
	t.Helper()
	const prefix = "vapid t="
	if !strings.HasPrefix(header, prefix) {
		t.Fatalf("authorization header %q is not VAPID", header)
	}
	token, _, _ := strings.Cut(strings.TrimPrefix(header, prefix), ",")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decoding claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("parsing claims: %v", err)
	}
	return claims
}

func TestWebpushSenderVAPIDSubAndRefusalBody(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generating VAPID keys: %v", err)
	}
	cfg := testConfig()
	cfg.VAPIDPublicKey = pub
	cfg.VAPIDPrivateKey = priv
	cfg.FrontendURL = "https://club.example.invalid"

	long := `{"reason":"BadJwtToken"}` + strings.Repeat("x", 500)
	client := &recordingPushClient{status: http.StatusForbidden, body: long}
	s := webpushSender{config: cfg, client: client}

	out, err := s.Send(context.Background(), testSubscription(t, "https://web.push.apple.com/QGabc"), []byte(`{"title":"t"}`))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Status != http.StatusForbidden {
		t.Errorf("status = %d", out.Status)
	}
	if len(out.Body) != pushResponseLogLimit || !strings.HasPrefix(out.Body, `{"reason":"BadJwtToken"}`) {
		t.Errorf("body not capped to the first %d bytes: %d %q", pushResponseLogLimit, len(out.Body), out.Body[:30])
	}

	claims := vapidClaims(t, client.req.Header.Get("Authorization"))
	if claims["sub"] != "https://club.example.invalid" {
		t.Errorf("sub = %v", claims["sub"])
	}
	if claims["aud"] != "https://web.push.apple.com" {
		t.Errorf("aud = %v", claims["aud"])
	}

	// Without an https site the contact is a single mailto: (webpush-go adds it).
	cfg.FrontendURL = "http://localhost:5173"
	cfg.Domain = "club.example.invalid"
	client.status, client.body = http.StatusCreated, ""
	out, err = s.Send(context.Background(), testSubscription(t, "https://fcm.googleapis.com/fcm/send/abc"), []byte(`{}`))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Status != http.StatusCreated || out.Body != "" {
		t.Errorf("success outcome = %+v", out)
	}
	if sub := vapidClaims(t, client.req.Header.Get("Authorization"))["sub"]; sub != "mailto:admin@club.example.invalid" {
		t.Errorf("sub = %v", sub)
	}
}

func TestPushServiceHostDropsThePath(t *testing.T) {
	if got := pushServiceHost("https://web.push.apple.com/QGsecret-token"); got != "web.push.apple.com" {
		t.Errorf("host = %q", got)
	}
}

func TestTestPushPayloadFallbacks(t *testing.T) {
	var p map[string]string
	if err := json.Unmarshal(testPushPayload("xx", ""), &p); err != nil {
		t.Fatal(err)
	}
	if p["title"] != "Testvarsel fra Brygge" {
		t.Errorf("unknown locale and no club name: title = %q", p["title"])
	}
	for locale := range testPushCopy {
		if err := json.Unmarshal(testPushPayload(locale, "Båtlaget"), &p); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(p["title"], "Båtlaget") || p["body"] == "" {
			t.Errorf("%s: %+v", locale, p)
		}
	}
}
