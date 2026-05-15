// Command totp prints the current 6-digit TOTP code for the enrolled
// demo admin, reading the gitignored secret written by cmd/seed. Used by
// `just totp` and by automated screenshot/e2e runs to pass the real
// /admin/verify-totp gate.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
)

const seedTOTPSecretFile = ".seed-totp-secret"

func main() {
	b, err := os.ReadFile(seedTOTPSecretFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no %s — run `just seed` first\n", seedTOTPSecretFile)
		os.Exit(1)
	}
	secret := strings.TrimSpace(string(b))
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate code: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(code)
}
