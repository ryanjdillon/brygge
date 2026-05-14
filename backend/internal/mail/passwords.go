package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// PrincipalPasswords maps shared-mailbox addresses to the service
// password set on the corresponding Stalwart principal. Generated and
// rotated by `stalwart-mailbox-config.service`; the file lives at
// /etc/stalwart/board-mailbox-passwords.json (root-owned, group-
// readable by `brygge`) so the backend can authenticate as each
// shared principal when applying RFC 8621 shareWith.
//
// JSON shape:
//
//	{
//	  "leiar@klokkarvikbaatlag.no": "<random-service-password>",
//	  "kasserar@klokkarvikbaatlag.no": "..."
//	}
//
// Lookups are case-insensitive on the address local-part since
// Stalwart normalises and the spec carries the canonical form.
type PrincipalPasswords map[string]string

// LoadPasswordMap reads the JSON file at path. Returns an empty map
// (not an error) when the file is absent — that's the supported
// "feature off" state on dev hosts. Missing-or-empty values for a
// given address are kept out of the map.
func LoadPasswordMap(path string) (PrincipalPasswords, error) {
	if path == "" {
		return PrincipalPasswords{}, nil
	}
	b, err := os.ReadFile(path) // #nosec G304 -- operator-controlled env
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PrincipalPasswords{}, nil
		}
		return nil, fmt.Errorf("read password map %s: %w", path, err)
	}
	raw := map[string]string{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse password map %s: %w", path, err)
	}
	out := make(PrincipalPasswords, len(raw))
	for addr, pw := range raw {
		if pw == "" {
			continue
		}
		out[strings.ToLower(addr)] = pw
	}
	return out, nil
}

// Get returns the password for `address`, or "" if absent.
func (p PrincipalPasswords) Get(address string) string {
	if p == nil {
		return ""
	}
	return p[strings.ToLower(address)]
}
