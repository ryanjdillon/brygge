package stripe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testWebhookSecret = "whsec_test_fake"

func sign(secret string, ts int64, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "." + body))
	return hex.EncodeToString(mac.Sum(nil))
}

func webhookRequest(body, header string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/stripe/webhooks", strings.NewReader(body))
	if header != "" {
		r.Header.Set("Stripe-Signature", header)
	}
	return r
}

func TestSignatureGoldenVector(t *testing.T) {
	// Precomputed outside this package, so the scheme itself is pinned,
	// not only its agreement with sign().
	const (
		body = `{"id":"evt_test_1","type":"checkout.session.completed"}`
		sig  = "f2608413bebdf1a2816340cf65183d4bd42bb672ac85a419073dfd3a3cdc9d60"
	)
	r := webhookRequest(body, "t=1767225600,v1="+sig)
	evt, raw, err := parseAndVerifyAt(r, testWebhookSecret, time.Unix(1767225610, 0))
	if err != nil {
		t.Fatalf("golden vector rejected: %v", err)
	}
	if evt.ID != "evt_test_1" || evt.Type != EventCheckoutSessionCompleted || string(raw) != body {
		t.Errorf("event = %+v raw = %q", evt, raw)
	}
}

func TestParseAndVerify(t *testing.T) {
	now := time.Unix(1767225600, 0)
	ts := now.Unix()
	body := `{"id":"evt_test_2","type":"payment_intent.succeeded","data":{"object":{"id":"pi_test"}}}`
	good := sign(testWebhookSecret, ts, body)

	tests := []struct {
		name    string
		body    string
		header  string
		secret  string
		wantErr bool
	}{
		{"valid", body, "t=" + strconv.FormatInt(ts, 10) + ",v1=" + good, testWebhookSecret, false},
		{"any of several v1", body, "t=" + strconv.FormatInt(ts, 10) + ",v1=" + strings.Repeat("ab", 32) + ",v1=" + good, testWebhookSecret, false},
		{"v0 ignored", body, "t=" + strconv.FormatInt(ts, 10) + ",v0=" + good, testWebhookSecret, true},
		{"tampered body", strings.Replace(body, "evt_test_2", "evt_test_3", 1), "t=" + strconv.FormatInt(ts, 10) + ",v1=" + good, testWebhookSecret, true},
		{"wrong secret", body, "t=" + strconv.FormatInt(ts, 10) + ",v1=" + sign("whsec_other_fake", ts, body), testWebhookSecret, true},
		{"empty secret", body, "t=" + strconv.FormatInt(ts, 10) + ",v1=" + sign("", ts, body), "", true},
		{"stale timestamp", body, "t=" + strconv.FormatInt(ts-301, 10) + ",v1=" + sign(testWebhookSecret, ts-301, body), testWebhookSecret, true},
		{"future timestamp", body, "t=" + strconv.FormatInt(ts+301, 10) + ",v1=" + sign(testWebhookSecret, ts+301, body), testWebhookSecret, true},
		{"no header", body, "", testWebhookSecret, true},
		{"no timestamp", body, "v1=" + good, testWebhookSecret, true},
		{"signed but not an event", `{"hello":1}`, "t=" + strconv.FormatInt(ts, 10) + ",v1=" + sign(testWebhookSecret, ts, `{"hello":1}`), testWebhookSecret, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			evt, _, err := parseAndVerifyAt(webhookRequest(tc.body, tc.header), tc.secret, now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && evt.ID != "evt_test_2" {
				t.Errorf("event id = %q", evt.ID)
			}
		})
	}
}

func TestParseAndVerify_EmptySecretSentinel(t *testing.T) {
	_, _, err := ParseAndVerify(webhookRequest("{}", "t=1,v1=00"), "")
	if !errors.Is(err, ErrNoWebhookSecret) {
		t.Errorf("err = %v, want ErrNoWebhookSecret", err)
	}
}
