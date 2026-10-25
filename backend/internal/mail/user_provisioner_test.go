package mail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// fakeAdmin serves GET/PATCH/POST on /api/principal for one principal
// and records request bodies.
type fakeAdmin struct {
	principal map[string]any // nil → {"data": null}
	patches   [][]map[string]any
	posts     []map[string]any
}

func (f *fakeAdmin) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": f.principal})
		case http.MethodPatch:
			var ops []map[string]any
			if err := json.Unmarshal(body, &ops); err != nil {
				t.Errorf("decode patch: %v", err)
			}
			f.patches = append(f.patches, ops)
			_, _ = w.Write([]byte(`{"data":null}`))
		case http.MethodPost:
			var p map[string]any
			if err := json.Unmarshal(body, &p); err != nil {
				t.Errorf("decode post: %v", err)
			}
			f.posts = append(f.posts, p)
			_, _ = w.Write([]byte(`{"data":1}`))
		}
	}))
}

func newTestProvisioner(url string) *UserProvisioner {
	admin := NewAdminClient(url, "admin", "pw", "", zerolog.Nop())
	return &UserProvisioner{admin: admin, clubDomain: "example.invalid", log: zerolog.Nop()}
}

const testUserID = "00000000-0000-4000-8000-000000000001"

func TestCreatePrincipalKeepsMarkerOutOfDescription(t *testing.T) {
	f := &fakeAdmin{}
	srv := f.server(t)
	defer srv.Close()

	if err := newTestProvisioner(srv.URL).createPrincipal(context.Background(), "bu000000000000", testUserID, "secret"); err != nil {
		t.Fatalf("createPrincipal: %v", err)
	}
	p := f.posts[0]
	if p["description"] != "Brygge user" {
		t.Errorf("description = %v, want plain label", p["description"])
	}
	urls, _ := p["urls"].([]any)
	if len(urls) != 2 || urls[0] != "urn:brygge:managed-by:brygge-user" || urls[1] != "urn:brygge:user:"+testUserID {
		t.Errorf("urls = %v", p["urls"])
	}
}

func TestConvergePrincipalPatchesLegacyShape(t *testing.T) {
	f := &fakeAdmin{principal: map[string]any{
		"name":        "bu000000000000",
		"description": "Brygge user someone@example.invalid (managed_by=brygge-user)",
	}}
	srv := f.server(t)
	defer srv.Close()

	changed, err := newTestProvisioner(srv.URL).convergePrincipal(context.Background(), "bu000000000000", testUserID)
	if err != nil || !changed {
		t.Fatalf("convergePrincipal = %v, %v; want changed", changed, err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(f.patches))
	}
	got := map[string]any{}
	for _, op := range f.patches[0] {
		got[op["field"].(string)] = op["value"]
	}
	if got["description"] != "Brygge user" {
		t.Errorf("description patch = %v", got["description"])
	}
	if urls, _ := got["urls"].([]any); len(urls) != 2 {
		t.Errorf("urls patch = %v", got["urls"])
	}
}

func TestConvergePrincipalIsIdempotent(t *testing.T) {
	f := &fakeAdmin{principal: map[string]any{
		"name":        "bu000000000000",
		"description": "Brygge user",
		// Order differs from userPrincipalURLs; still converged.
		"urls": []string{"urn:brygge:user:" + testUserID, "urn:brygge:managed-by:brygge-user"},
	}}
	srv := f.server(t)
	defer srv.Close()

	changed, err := newTestProvisioner(srv.URL).convergePrincipal(context.Background(), "bu000000000000", testUserID)
	if err != nil || changed || len(f.patches) != 0 {
		t.Fatalf("converged principal patched: changed=%v err=%v patches=%v", changed, err, f.patches)
	}
}

func TestConvergePrincipalSkipsMissingPrincipal(t *testing.T) {
	f := &fakeAdmin{}
	srv := f.server(t)
	defer srv.Close()

	changed, err := newTestProvisioner(srv.URL).convergePrincipal(context.Background(), "bu000000000000", testUserID)
	if err != nil || changed || len(f.patches) != 0 {
		t.Fatalf("missing principal: changed=%v err=%v patches=%v", changed, err, f.patches)
	}
}

func TestPrincipalSlugIsStable(t *testing.T) {
	id := "ed5aa164-9977-41e9-b63f-f0a5c832e435"
	want := "buedy5aa1649977"
	got := principalSlug(id)
	if got != "bu"+"ed5aa1649977" {
		t.Errorf("principalSlug(%q) = %q, want %q", id, got, want)
	}
}

func TestPrincipalSlugIsAlphanumericOnly(t *testing.T) {
	got := principalSlug("ed5aa164-9977-41e9-b63f-f0a5c832e435")
	for _, c := range got {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !ok {
			t.Errorf("slug contains non-alphanumeric: %q", c)
		}
	}
}

func TestPrincipalSlugHandlesShortInput(t *testing.T) {
	if got := principalSlug("abc"); got != "buabc" {
		t.Errorf("short input: got %q", got)
	}
}

func TestPrincipalSlugIsLowercase(t *testing.T) {
	got := principalSlug("ABCDEF12-3456")
	if got != strings.ToLower(got) {
		t.Errorf("slug not lowercased: %q", got)
	}
}

func TestGenerateServicePasswordIsNonEmptyAndURLSafe(t *testing.T) {
	a, err := generateServicePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 30 {
		t.Errorf("password too short: %d", len(a))
	}
	for _, c := range a {
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_'
		if !ok {
			t.Errorf("non-URL-safe char in password: %q", c)
		}
	}
	b, _ := generateServicePassword()
	if a == b {
		t.Errorf("two consecutive passwords identical — RNG broken?")
	}
}
