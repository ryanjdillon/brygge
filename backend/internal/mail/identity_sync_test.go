package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
)

func TestSenderName(t *testing.T) {
	kasserar := MailboxSpec{Address: "kasserar@example.invalid", DisplayName: "Kasserar"}
	post := MailboxSpec{Address: "post@example.invalid", DisplayName: "Post", FromBrandOnly: true}
	noLabel := MailboxSpec{Address: "x@example.invalid"}

	cases := []struct {
		name     string
		club     string
		abbrev   string
		spec     MailboxSpec
		expected string
	}{
		{"brand then role", "Test Båtlag", "TBL", kasserar, "Test Båtlag – Kasserar"},
		{"brand only flag", "Test Båtlag", "TBL", post, "Test Båtlag"},
		{"no display name", "Test Båtlag", "TBL", noLabel, "Test Båtlag"},
		{"abbrev fallback", "", "TBL", kasserar, "TBL – Kasserar"},
		{"no brand at all", "", "", kasserar, "Kasserar"},
		{"nothing", "", "", noLabel, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SenderName(tc.club, tc.abbrev, tc.spec); got != tc.expected {
				t.Errorf("SenderName = %q, want %q", got, tc.expected)
			}
		})
	}
}

// fakeJMAP is a minimal JMAP endpoint serving Identity/get and
// Identity/set for one account, recording every Identity/set update.
type fakeJMAP struct {
	mu         sync.Mutex
	identities []Identity
	updates    map[string]string // identity id → new name
	setResp    func(id string) any
	getErr     bool
}

func (f *fakeJMAP) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Using       []string          `json:"using"`
			MethodCalls []json.RawMessage `json:"methodCalls"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		var call [3]json.RawMessage
		if err := json.Unmarshal(req.MethodCalls[0], &call); err != nil {
			t.Errorf("decode call: %v", err)
			return
		}
		var method string
		_ = json.Unmarshal(call[0], &method)

		f.mu.Lock()
		defer f.mu.Unlock()
		var out any
		switch method {
		case "Identity/get":
			if f.getErr {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			out = []any{"Identity/get", map[string]any{"list": f.identities}, "0"}
		case "Identity/set":
			if !containsString(req.Using, "urn:ietf:params:jmap:submission") {
				t.Errorf("Identity/set without submission capability: %v", req.Using)
			}
			var args struct {
				Update map[string]map[string]string `json:"update"`
			}
			_ = json.Unmarshal(call[1], &args)
			for id, patch := range args.Update {
				if f.setResp != nil {
					if resp := f.setResp(id); resp != nil {
						out = resp
						continue
					}
				}
				if f.updates == nil {
					f.updates = map[string]string{}
				}
				f.updates[id] = patch["name"]
				out = []any{"Identity/set", map[string]any{"updated": map[string]any{id: nil}}, "0"}
			}
		default:
			t.Errorf("unexpected method %s", method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"methodResponses": []any{out}})
	}
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestSetIdentityNameSendsUpdate(t *testing.T) {
	f := &fakeJMAP{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	c := NewJMAPClient(srv.URL, "post", "pw", nil)
	if err := c.SetIdentityName(context.Background(), "a", "id1", "Test Båtlag"); err != nil {
		t.Fatalf("SetIdentityName: %v", err)
	}
	if f.updates["id1"] != "Test Båtlag" {
		t.Errorf("update not sent: %+v", f.updates)
	}
}

func TestSetIdentityNameSurfacesNotUpdated(t *testing.T) {
	f := &fakeJMAP{setResp: func(id string) any {
		return []any{"Identity/set", map[string]any{"notUpdated": map[string]any{
			id: map[string]string{"type": "invalidProperties", "description": "name too long"},
		}}, "0"}
	}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	err := NewJMAPClient(srv.URL, "post", "pw", nil).SetIdentityName(context.Background(), "a", "id1", "x")
	if err == nil || !strings.Contains(err.Error(), "name too long") {
		t.Fatalf("expected notUpdated description in error, got %v", err)
	}
}

func TestSetIdentityNameSurfacesMethodError(t *testing.T) {
	f := &fakeJMAP{setResp: func(string) any {
		return []any{"error", map[string]string{"type": "unknownMethod"}, "0"}
	}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	err := NewJMAPClient(srv.URL, "post", "pw", nil).SetIdentityName(context.Background(), "a", "id1", "x")
	if err == nil || !strings.Contains(err.Error(), "unknownMethod") {
		t.Fatalf("expected method error, got %v", err)
	}
}

func newIdentityTestReconciler(dry bool) *Reconciler {
	return &Reconciler{dry: dry, log: zerolog.Nop(), clubName: "Test Båtlag", clubAbbrev: "TBL"}
}

func TestSyncIdentityNameRenamesOnMismatch(t *testing.T) {
	f := &fakeJMAP{identities: []Identity{
		{ID: "i1", Name: "Post (managed_by=brygge-tf)", Email: "post@example.invalid"},
		{ID: "i2", Name: "alias", Email: "other@example.invalid"},
	}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	spec := MailboxSpec{Address: "post@example.invalid", DisplayName: "Post", FromBrandOnly: true}
	newIdentityTestReconciler(false).syncIdentityName(context.Background(), NewJMAPClient(srv.URL, "post", "pw", nil), "a", spec)

	if f.updates["i1"] != "Test Båtlag" {
		t.Errorf("i1 not renamed: %+v", f.updates)
	}
	if _, ok := f.updates["i2"]; ok {
		t.Errorf("identity for another address must not be renamed")
	}
}

func TestSyncIdentityNameNoOpWhenMatching(t *testing.T) {
	f := &fakeJMAP{identities: []Identity{
		{ID: "i1", Name: "Test Båtlag – Kasserar", Email: "kasserar@example.invalid"},
	}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	spec := MailboxSpec{Address: "kasserar@example.invalid", DisplayName: "Kasserar"}
	newIdentityTestReconciler(false).syncIdentityName(context.Background(), NewJMAPClient(srv.URL, "k", "pw", nil), "a", spec)

	if len(f.updates) != 0 {
		t.Errorf("expected no Identity/set, got %+v", f.updates)
	}
}

func TestSyncIdentityNameDryRunDoesNotWrite(t *testing.T) {
	f := &fakeJMAP{identities: []Identity{
		{ID: "i1", Name: "stale", Email: "kasserar@example.invalid"},
	}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	spec := MailboxSpec{Address: "kasserar@example.invalid", DisplayName: "Kasserar"}
	newIdentityTestReconciler(true).syncIdentityName(context.Background(), NewJMAPClient(srv.URL, "k", "pw", nil), "a", spec)

	if len(f.updates) != 0 {
		t.Errorf("dry-run wrote: %+v", f.updates)
	}
}

func TestSyncIdentityNameErrorsAreIsolated(t *testing.T) {
	// A rejected rename of one identity must not stop the next one.
	f := &fakeJMAP{
		identities: []Identity{
			{ID: "bad", Name: "stale", Email: "kasserar@example.invalid"},
			{ID: "good", Name: "stale", Email: "kasserar@example.invalid"},
		},
		setResp: func(id string) any {
			if id != "bad" {
				return nil
			}
			return []any{"Identity/set", map[string]any{"notUpdated": map[string]any{
				id: map[string]string{"type": "forbidden"},
			}}, "0"}
		},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	spec := MailboxSpec{Address: "kasserar@example.invalid", DisplayName: "Kasserar"}
	r := newIdentityTestReconciler(false)
	r.syncIdentityName(context.Background(), NewJMAPClient(srv.URL, "k", "pw", nil), "a", spec)
	if f.updates["good"] != "Test Båtlag – Kasserar" {
		t.Errorf("second identity not renamed after first failed: %+v", f.updates)
	}

	// A failed Identity/get just returns; the caller carries on with ACLs.
	f.getErr = true
	r.syncIdentityName(context.Background(), NewJMAPClient(srv.URL, "k", "pw", nil), "a", spec)
}
