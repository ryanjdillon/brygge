package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/passkey"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func passkeyTestCredential(id string) *webauthn.Credential {
	return &webauthn.Credential{
		ID:              []byte(id),
		PublicKey:       []byte("cose-key"),
		AttestationType: "none",
		Transport:       []protocol.AuthenticatorTransport{protocol.Internal},
		Flags:           webauthn.CredentialFlags{UserPresent: true, UserVerified: true},
		Authenticator:   webauthn.Authenticator{SignCount: 0},
	}
}

// authedReq builds a request carrying claims, a session id, and an {id} route
// param, the way AuthenticateSession would.
func authedReq(method, target string, body []byte, userID, clubID, sessionID, idParam string) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	ctx := middleware.WithClaims(r.Context(), &auth.Claims{UserID: userID, ClubID: clubID, Roles: []string{"board"}})
	ctx = middleware.WithSessionID(ctx, sessionID)
	rctx := chi.NewRouteContext()
	if idParam != "" {
		rctx.URLParams.Add("id", idParam)
	}
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return r.WithContext(ctx)
}

func TestPasskeyRegisterBeginReturnsOptions(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	wa, err := passkey.New(&config.Config{FrontendURL: "https://example.org", ClubName: "Test Club"})
	if err != nil {
		t.Fatalf("passkey.New: %v", err)
	}
	h := NewPasskeyHandler(wa, passkey.NewStore(pool), passkey.NewChallengeStore(rdb), &config.Config{}, zerolog.Nop())

	rec := httptest.NewRecorder()
	h.HandleRegisterBegin(rec, authedReq(http.MethodPost, "/api/v1/auth/passkeys/register/begin", nil, userID, clubID, "sess-1", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var opts struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &opts); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	if opts.Challenge == "" {
		t.Error("empty challenge in creation options")
	}
	if opts.RP.ID != "example.org" {
		t.Errorf("rp.id = %q, want example.org", opts.RP.ID)
	}
	if opts.User.ID == "" {
		t.Error("empty user handle in creation options")
	}

	// The ceremony state was stashed for finish.
	if got, err := passkey.NewChallengeStore(rdb).Take(context.Background(), "reg:sess-1"); err != nil || got == nil {
		t.Errorf("challenge not stored: %v", err)
	}
}

func TestPasskeyManagementRoundTrip(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	store := passkey.NewStore(pool)
	if _, err := store.LoadUser(context.Background(), userID); err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if err := store.SaveCredential(context.Background(), userID, clubID, passkeyTestCredential("k1"), ""); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}

	wa, _ := passkey.New(&config.Config{FrontendURL: "https://example.org"})
	h := NewPasskeyHandler(wa, store, passkey.NewChallengeStore(rdb), &config.Config{}, zerolog.Nop())

	// List
	rec := httptest.NewRecorder()
	h.HandleList(rec, authedReq(http.MethodGet, "/api/v1/auth/passkeys", nil, userID, clubID, "sess-1", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var listResp struct {
		Passkeys []passkey.CredentialInfo `json:"passkeys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Passkeys) != 1 {
		t.Fatalf("got %d passkeys, want 1", len(listResp.Passkeys))
	}
	credID := listResp.Passkeys[0].ID

	// Rename
	rec = httptest.NewRecorder()
	h.HandleRename(rec, authedReq(http.MethodPatch, "/api/v1/auth/passkeys/"+credID, []byte(`{"name":"My Key"}`), userID, clubID, "sess-1", credID))
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d; body=%s", rec.Code, rec.Body.String())
	}
	relist, _ := store.ListCredentials(context.Background(), userID)
	if relist[0].Name != "My Key" {
		t.Errorf("name = %q, want My Key", relist[0].Name)
	}

	// Delete
	rec = httptest.NewRecorder()
	h.HandleDelete(rec, authedReq(http.MethodDelete, "/api/v1/auth/passkeys/"+credID, nil, userID, clubID, "sess-1", credID))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if n, _ := store.CountForUser(context.Background(), userID); n != 0 {
		t.Errorf("count after delete = %d, want 0", n)
	}
}
