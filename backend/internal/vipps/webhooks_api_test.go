package vipps_test

import (
	"context"
	"strings"
	"testing"
)

func TestRegisterWebhook(t *testing.T) {
	srv, _ := mockVippsServer(t)
	c := newTestClient(srv.URL)

	res, err := c.RegisterWebhook(context.Background(), "https://example.com/api/v1/vipps/webhooks")
	if err != nil {
		t.Fatalf("RegisterWebhook: %v", err)
	}
	if res.Secret != "generated-secret" {
		t.Errorf("secret = %q, want generated-secret", res.Secret)
	}
	if !strings.HasSuffix(res.ID, "/api/v1/vipps/webhooks") {
		t.Errorf("id = %q, expected to echo the registered URL", res.ID)
	}
}

func TestRegisterWebhook_EmptyURL(t *testing.T) {
	c := newTestClient("http://unused")
	if _, err := c.RegisterWebhook(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty callback URL, got nil")
	}
}

func TestListWebhooks(t *testing.T) {
	srv, _ := mockVippsServer(t)
	c := newTestClient(srv.URL)

	hooks, err := c.ListWebhooks(context.Background())
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].ID != "wh-1" {
		t.Fatalf("unexpected webhooks: %+v", hooks)
	}
}

func TestDeleteWebhook(t *testing.T) {
	srv, _ := mockVippsServer(t)
	c := newTestClient(srv.URL)

	if err := c.DeleteWebhook(context.Background(), "wh-1"); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
	if err := c.DeleteWebhook(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty id, got nil")
	}
}
