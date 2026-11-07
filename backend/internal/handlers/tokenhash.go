package handlers

import (
	"crypto/sha256"
	"encoding/hex"
)

// sha256Hex is the lowercase hex SHA-256 of s. Emailed single-use tokens
// (login links, join confirmations) are stored only in this form, and
// migrations that backfill such a column compute the same value with
// encode(sha256(convert_to(s, 'UTF8')), 'hex').
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
