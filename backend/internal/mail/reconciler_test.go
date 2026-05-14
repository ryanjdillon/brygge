package mail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashShareWithIsKeyOrderIndependent(t *testing.T) {
	rights := ShareRights{MayReadItems: true, MaySetSeen: true, MayAddItems: true}
	a := map[string]ShareRights{"u2": rights, "u1": rights}
	b := map[string]ShareRights{"u1": rights, "u2": rights}
	if hashShareWith(a) != hashShareWith(b) {
		t.Fatalf("expected equal hashes regardless of map insertion order")
	}
}

func TestHashShareWithDetectsMembershipChange(t *testing.T) {
	rights := ShareRights{MayReadItems: true}
	base := map[string]ShareRights{"u1": rights}
	extra := map[string]ShareRights{"u1": rights, "u2": rights}
	if hashShareWith(base) == hashShareWith(extra) {
		t.Fatalf("expected different hashes when membership changes")
	}
}

func TestHashShareWithDetectsRightsChange(t *testing.T) {
	r1 := map[string]ShareRights{"u1": {MayReadItems: true}}
	r2 := map[string]ShareRights{"u1": {MayReadItems: true, MayAddItems: true}}
	if hashShareWith(r1) == hashShareWith(r2) {
		t.Fatalf("expected different hashes when rights change")
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
