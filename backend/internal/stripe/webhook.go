package stripe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxWebhookBody caps how much of a webhook body is buffered for hashing.
const maxWebhookBody = 1 << 20

// signatureTolerance is how old a signed timestamp may be; it bounds how long
// a captured delivery can be replayed.
const signatureTolerance = 300 * time.Second

// ErrNoWebhookSecret rejects every webhook while no signing secret is
// configured, so an unconfigured endpoint can never accept an event.
var ErrNoWebhookSecret = errors.New("stripe webhook secret not configured")

// ParseAndVerify reads the request body, verifies the Stripe-Signature header
// against the endpoint's signing secret, and only then decodes the event.
//
// The header is `t=<unix>,v1=<hex>[,v1=<hex>…]`; the signature is
// HMAC-SHA256(secret, "<t>.<raw body>") in hex. Several v1 entries appear
// while a secret is being rolled, and any one matching is enough. See
// https://docs.stripe.com/webhooks#verify-manually
func ParseAndVerify(r *http.Request, secret string) (*Event, []byte, error) {
	return parseAndVerifyAt(r, secret, time.Now())
}

func parseAndVerifyAt(r *http.Request, secret string, now time.Time) (*Event, []byte, error) {
	if secret == "" {
		return nil, nil, ErrNoWebhookSecret
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		return nil, nil, fmt.Errorf("reading webhook body: %w", err)
	}
	if err := verifySignature(r.Header.Get("Stripe-Signature"), body, secret, now); err != nil {
		return nil, body, err
	}
	var evt Event
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, body, fmt.Errorf("decoding webhook event: %w", err)
	}
	if evt.ID == "" || evt.Type == "" {
		return nil, body, errors.New("webhook event without id or type")
	}
	return &evt, body, nil
}

func verifySignature(header string, body []byte, secret string, now time.Time) error {
	ts, sigs, err := parseSignatureHeader(header)
	if err != nil {
		return err
	}
	if age := now.Sub(time.Unix(ts, 0)); age > signatureTolerance || age < -signatureTolerance {
		return errors.New("webhook signature timestamp outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := mac.Sum(nil)
	for _, s := range sigs {
		if hmac.Equal(s, expected) {
			return nil
		}
	}
	return errors.New("webhook signature mismatch")
}

// parseSignatureHeader returns the timestamp and every v1 signature. Other
// schemes (v0, the test-mode scheme) are ignored.
func parseSignatureHeader(header string) (int64, [][]byte, error) {
	var (
		ts    int64
		haveT bool
		sigs  [][]byte
	)
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return 0, nil, errors.New("webhook signature: bad timestamp")
			}
			ts, haveT = n, true
		case "v1":
			if b, err := hex.DecodeString(v); err == nil {
				sigs = append(sigs, b)
			}
		}
	}
	if !haveT {
		return 0, nil, errors.New("webhook signature: no timestamp")
	}
	if len(sigs) == 0 {
		return 0, nil, errors.New("webhook signature: no v1 signature")
	}
	return ts, sigs, nil
}
