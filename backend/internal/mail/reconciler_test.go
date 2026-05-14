package mail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashACLsCanonicalisesOrderAndRights(t *testing.T) {
	a := []MailboxACL{
		{PrincipalID: "u2", Rights: []string{"append", "read"}},
		{PrincipalID: "u1", Rights: []string{"read", "append", "send_as"}},
	}
	b := []MailboxACL{
		{PrincipalID: "u1", Rights: []string{"send_as", "append", "read"}},
		{PrincipalID: "u2", Rights: []string{"read", "append"}},
	}
	if hashACLs(a) != hashACLs(b) {
		t.Fatalf("expected equal hashes for reordered ACLs")
	}
}

func TestHashACLsDetectsChange(t *testing.T) {
	base := []MailboxACL{{PrincipalID: "u1", Rights: []string{"read"}}}
	extra := append(base, MailboxACL{PrincipalID: "u2", Rights: []string{"read"}})
	if hashACLs(base) == hashACLs(extra) {
		t.Fatalf("expected different hashes when membership changes")
	}
}

func TestLoadSpecAbsentFileReturnsEmpty(t *testing.T) {
	specs, err := LoadSpec(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("absent file should be a no-op, got: %v", err)
	}
	if len(specs) != 0 {
		t.Fatalf("expected empty spec, got %d entries", len(specs))
	}
}

func TestLoadSpecParsesBoardMailboxes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mailboxes.json")
	body := `[
	  {"address":"kasserar@example.no","role":"treasurer","display_name":"Kasserar","type":"shared","send_as":true},
	  {"address":"info@example.no","role":"board","display_name":"Info","type":"list","managed":false}
	]`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	specs, err := LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(specs))
	}
	if !specs[0].SendAs || specs[0].Type != "shared" {
		t.Errorf("first entry parsed wrong: %+v", specs[0])
	}
	if specs[1].managed() {
		t.Errorf("info@ entry has managed=false; managed() should be false")
	}
}
