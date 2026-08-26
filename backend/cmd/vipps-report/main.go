// Command vipps-report probes the Vipps Report API (settlement/ledger) with the
// configured credentials and prints the raw JSON. Operator/discovery tool for
// BRY-4: confirm Report API access is enabled, find the ledgerId, and capture a
// real response shape before the ingest mapper is built.
//
// Usage:
//
//	vipps-report ledgers                    # list ledgers (also probes access)
//	vipps-report funds <ledgerId> <date>    # settled funds for a date (YYYY-MM-DD)
//	vipps-report fees  <ledgerId> <date>    # fees for a date
//
// Run with the Vipps credentials loaded (VIPPS_TEST_MODE=false + prod creds for
// prod ledgers). Exit status 0 on success, 1 on any failure.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cfg := config.Load()
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
		With().Timestamp().Str("service", "vipps-report").Logger()

	if cfg.VippsMSN == "" || cfg.VippsClientID == "" {
		fmt.Fprintln(os.Stderr, "vipps-report: missing Vipps credentials (VIPPS_MSN / VIPPS_CLIENT_ID ...)")
		os.Exit(1)
	}
	client := vipps.New(cfg.VippsBaseURL(), cfg.VippsClientID, cfg.VippsClientSecret,
		cfg.VippsSubscriptionKey, cfg.VippsMSN, log)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		raw json.RawMessage
		err error
	)
	switch os.Args[1] {
	case "ledgers":
		raw, err = client.ListLedgers(ctx)
	case "funds":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "funds: needs <ledgerId> <date>")
			os.Exit(1)
		}
		raw, err = client.LedgerFunds(ctx, os.Args[2], os.Args[3])
	case "fees":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "fees: needs <ledgerId> <date>")
			os.Exit(1)
		}
		raw, err = client.LedgerFees(ctx, os.Args[2], os.Args[3])
	case "payment":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "payment: needs <reference>")
			os.Exit(1)
		}
		raw, err = client.GetPaymentRaw(ctx, os.Args[2])
	default:
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL (%s, MSN %s, test_mode=%t): %v\n",
			cfg.VippsBaseURL(), cfg.VippsMSN, cfg.VippsTestMode, err)
		os.Exit(1)
	}

	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(raw))
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: vipps-report ledgers | funds <ledgerId> <date> | fees <ledgerId> <date> | payment <reference>")
}
