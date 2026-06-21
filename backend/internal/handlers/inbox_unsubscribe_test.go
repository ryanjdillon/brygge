package handlers

import (
	"bytes"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/unsubscribe"
)

func TestUnsubscribeURL(t *testing.T) {
	secret := bytes.Repeat([]byte{0x42}, 32)
	h := &InboxHandler{frontendURL: "https://club.example", unsubscribeSecret: secret}
	uid := "user-123"

	got := h.unsubscribeURL(&uid)
	const prefix = "https://club.example/api/v1/unsubscribe?token="
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("url = %q, want prefix %q", got, prefix)
	}

	// The token must verify back to the member + broadcast category.
	tok := strings.TrimPrefix(got, prefix)
	gotUID, cat, err := unsubscribe.VerifyToken(tok, secret)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if gotUID != uid || cat != "broadcast" {
		t.Errorf("token decoded to %s/%s, want %s/broadcast", gotUID, cat, uid)
	}

	// Ad-hoc recipients (no member id) get no link.
	if h.unsubscribeURL(nil) != "" {
		t.Error("expected empty url for nil userID")
	}

	// Unconfigured handler (no secret / frontend URL) gets no link.
	if (&InboxHandler{}).unsubscribeURL(&uid) != "" {
		t.Error("expected empty url when unsubscribe not configured")
	}
}
