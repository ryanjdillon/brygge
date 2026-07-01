// Command vipps-verify performs an end-to-end Vipps Access Token API
// handshake with the configured credentials and reports whether the test (or
// prod) merchant authenticates. It is the operator check for BRY-2: after
// pasting VIPPS_CLIENT_ID / VIPPS_CLIENT_SECRET / VIPPS_SUBSCRIPTION_KEY /
// VIPPS_MSN into /etc/brygge/env, run `just vipps-verify` to confirm Vipps
// accepts them before wiring up any payment flow.
//
// Exit status is 0 on a successful handshake and 1 on any failure (missing
// credentials, network error, or rejection by Vipps).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

func main() {
	cfg := config.Load()

	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
		With().
		Timestamp().
		Str("service", "vipps-verify").
		Logger()

	baseURL := cfg.VippsBaseURL()

	missing := make([]string, 0, 4)
	if cfg.VippsClientID == "" {
		missing = append(missing, "VIPPS_CLIENT_ID")
	}
	if cfg.VippsClientSecret == "" {
		missing = append(missing, "VIPPS_CLIENT_SECRET")
	}
	if cfg.VippsSubscriptionKey == "" {
		missing = append(missing, "VIPPS_SUBSCRIPTION_KEY")
	}
	if cfg.VippsMSN == "" {
		missing = append(missing, "VIPPS_MSN")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "vipps-verify: missing required credentials: %v\n", missing)
		fmt.Fprintf(os.Stderr, "set them in /etc/brygge/env (see deploy/.env.example) and retry\n")
		os.Exit(1)
	}

	client := vipps.New(baseURL, cfg.VippsClientID, cfg.VippsClientSecret,
		cfg.VippsSubscriptionKey, cfg.VippsMSN, log)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := client.Verify(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL — Vipps rejected the credentials (%s, MSN %s, test_mode=%t)\n",
			res.BaseURL, res.MSN, cfg.VippsTestMode)
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("OK — Vipps Access Token API handshake succeeded\n")
	fmt.Printf("  endpoint:    %s\n", res.BaseURL)
	fmt.Printf("  test_mode:   %t\n", cfg.VippsTestMode)
	fmt.Printf("  msn:         %s\n", res.MSN)
	fmt.Printf("  token:       %s\n", res.TokenMasked)
	fmt.Printf("  expires_at:  %s (in %s)\n",
		res.ExpiresAt.Format(time.RFC3339), time.Until(res.ExpiresAt).Round(time.Second))
}
