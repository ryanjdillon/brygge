package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestNormaliseShortCode(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"  Gjest ", "gjest", true},
		{"dock-a", "dock-a", true},
		{"ab", "ab", true},
		{"a", "a", false},
		{strings.Repeat("a", 25), strings.Repeat("a", 25), false},
		{"gjest havn", "gjest havn", false},
		{"bryggé", "bryggé", false},
		{"q/x", "q/x", false},
	}
	for _, c := range cases {
		got, ok := normaliseShortCode(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("normaliseShortCode(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestGenShortCode(t *testing.T) {
	for i := 0; i < 200; i++ {
		code, err := genShortCode()
		if err != nil {
			t.Fatalf("genShortCode: %v", err)
		}
		if len(code) != shortCodeGenLength || !shortCodePattern.MatchString(code) {
			t.Fatalf("genShortCode() = %q, not a valid 5-char code", code)
		}
		if strings.ContainsAny(code, "0o1li") {
			t.Fatalf("genShortCode() = %q, contains a look-alike character", code)
		}
	}
}

func TestInsertWithShortCodeRetriesGeneratedCodesOnly(t *testing.T) {
	clash := &pgconn.PgError{Code: "23505", ConstraintName: "idx_harbor_targets_club_short_code"}

	var tried []string
	id, code, err := insertWithShortCode("", func(c string) (string, error) {
		tried = append(tried, c)
		if len(tried) < 3 {
			return "", clash
		}
		return "id-1", nil
	})
	if err != nil || id != "id-1" || len(tried) != 3 || code != tried[2] {
		t.Fatalf("generated: id=%q code=%q err=%v tried=%v", id, code, err, tried)
	}

	tried = nil
	_, code, err = insertWithShortCode("gjest", func(c string) (string, error) {
		tried = append(tried, c)
		return "", clash
	})
	if !isShortCodeClash(err) || len(tried) != 1 || code != "gjest" {
		t.Fatalf("custom: code=%q err=%v tried=%v", code, err, tried)
	}
}

func TestHarborTargetShortCode(t *testing.T) {
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	slug := "s-" + testutil.RandomHex(4)
	clubID := "22222222-2222-2222-2222-" + testutil.RandomHex(6)
	mustExec(t, db, ctx, "INSERT INTO clubs (id, slug, name) VALUES ($1, $2, 'Synthetic Club')", clubID, slug)
	var priceItemID string
	if err := db.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount)
		 VALUES ($1, 'guest_harbor', 'Synthetic guest night', 200) RETURNING id`, clubID,
	).Scan(&priceItemID); err != nil {
		t.Fatalf("insert price item: %v", err)
	}

	cfg := testConfig()
	cfg.ClubSlug = slug
	cfg.FrontendURL = "https://example.test/"
	h := NewHarborHandler(db, cfg, zerolog.Nop())

	r := chi.NewRouter()
	r.Post("/harbor/payment-targets", h.HandleCreateTarget)
	r.Patch("/harbor/payment-targets/{id}", h.HandleUpdateTarget)
	r.Post("/harbor/payment-targets/{id}/archive", h.HandleArchiveTarget)
	r.Get("/harbor/payment-targets", h.HandleListTargets)
	r.Get("/q/{code}", h.HandleShortCodeRedirect)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	type created struct {
		ID        string `json:"id"`
		Token     string `json:"token"`
		ShortCode string `json:"short_code"`
		ShortURL  string `json:"short_url"`
	}
	create := func(label, shortCode string) (*httptest.ResponseRecorder, created) {
		body := `{"label":"` + label + `","price_item_id":"` + priceItemID + `"`
		if shortCode != "" {
			body += `,"short_code":"` + shortCode + `"`
		}
		body += `}`
		rec := do(http.MethodPost, "/harbor/payment-targets", body)
		var c created
		if rec.Code == http.StatusCreated {
			if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
				t.Fatalf("decode create: %v", err)
			}
		}
		return rec, c
	}
	errCode := func(rec *httptest.ResponseRecorder) string {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Code
	}
	location := func(rec *httptest.ResponseRecorder) string {
		if rec.Code != http.StatusFound {
			t.Fatalf("resolve: status %d, want 302", rec.Code)
		}
		return rec.Header().Get("Location")
	}

	t.Run("blank code on create gets a generated one", func(t *testing.T) {
		rec, c := create("Dock A", "")
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: status %d (%s)", rec.Code, rec.Body.String())
		}
		if len(c.ShortCode) != shortCodeGenLength || strings.ContainsAny(c.ShortCode, "0o1li") {
			t.Fatalf("generated code %q is not a 5-char unambiguous code", c.ShortCode)
		}
		if c.ShortURL != "https://example.test/q/"+c.ShortCode {
			t.Fatalf("short_url = %q", c.ShortURL)
		}
	})

	var custom created
	t.Run("custom code is trimmed and lowercased", func(t *testing.T) {
		rec, c := create("Dock B", "  Dock-B ")
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: status %d (%s)", rec.Code, rec.Body.String())
		}
		if c.ShortCode != "dock-b" {
			t.Fatalf("short_code = %q, want dock-b", c.ShortCode)
		}
		custom = c
	})

	t.Run("duplicate code returns 409", func(t *testing.T) {
		rec, _ := create("Dock C", "DOCK-B")
		if rec.Code != http.StatusConflict || errCode(rec) != shortCodeTakenCode {
			t.Fatalf("status %d code %q, want 409 %s", rec.Code, errCode(rec), shortCodeTakenCode)
		}
	})

	t.Run("invalid code returns 400", func(t *testing.T) {
		for _, bad := range []string{"x", "dock a", "dock/a", strings.Repeat("a", 25)} {
			rec, _ := create("Dock D", bad)
			if rec.Code != http.StatusBadRequest || errCode(rec) != shortCodeInvalidCode {
				t.Errorf("%q: status %d code %q, want 400 %s", bad, rec.Code, errCode(rec), shortCodeInvalidCode)
			}
		}
	})

	t.Run("list includes code and short url", func(t *testing.T) {
		rec := do(http.MethodGet, "/harbor/payment-targets", "")
		var list []harborTargetResp
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		var found bool
		for _, tg := range list {
			if tg.ID == custom.ID {
				found = tg.ShortCode == "dock-b" && tg.ShortURL == "https://example.test/q/dock-b"
			}
		}
		if !found {
			t.Fatalf("list missing short code/url for %s: %s", custom.ID, rec.Body.String())
		}
	})

	t.Run("resolver is case-insensitive", func(t *testing.T) {
		if got := location(do(http.MethodGet, "/q/DoCk-B", "")); got != "/qr/"+custom.Token {
			t.Fatalf("Location = %q, want /qr/%s", got, custom.Token)
		}
	})

	t.Run("unknown and malformed codes resolve to not-found", func(t *testing.T) {
		for _, code := range []string{"nope", "x"} {
			if got := location(do(http.MethodGet, "/q/"+code, "")); got != "/qr/not-found" {
				t.Errorf("%q: Location = %q", code, got)
			}
		}
	})

	t.Run("PATCH changes the code", func(t *testing.T) {
		body := `{"label":"Dock B","price_item_id":"` + priceItemID + `","short_code":"Gjest"}`
		if rec := do(http.MethodPatch, "/harbor/payment-targets/"+custom.ID, body); rec.Code != http.StatusOK {
			t.Fatalf("patch: status %d (%s)", rec.Code, rec.Body.String())
		}
		if got := location(do(http.MethodGet, "/q/gjest", "")); got != "/qr/"+custom.Token {
			t.Fatalf("new code Location = %q", got)
		}
		if got := location(do(http.MethodGet, "/q/dock-b", "")); got != "/qr/not-found" {
			t.Fatalf("old code Location = %q, want not-found", got)
		}
	})

	t.Run("PATCH without a code keeps it", func(t *testing.T) {
		body := `{"label":"Dock B renamed","price_item_id":"` + priceItemID + `"}`
		if rec := do(http.MethodPatch, "/harbor/payment-targets/"+custom.ID, body); rec.Code != http.StatusOK {
			t.Fatalf("patch: status %d (%s)", rec.Code, rec.Body.String())
		}
		var code string
		if err := db.QueryRow(ctx, `SELECT short_code FROM harbor_payment_targets WHERE id = $1`, custom.ID).Scan(&code); err != nil || code != "gjest" {
			t.Fatalf("short_code = %q (err %v), want gjest", code, err)
		}
	})

	t.Run("PATCH to a taken or invalid code is rejected", func(t *testing.T) {
		_, other := create("Dock E", "dock-e")
		body := `{"label":"Dock E","price_item_id":"` + priceItemID + `","short_code":"GJEST"}`
		rec := do(http.MethodPatch, "/harbor/payment-targets/"+other.ID, body)
		if rec.Code != http.StatusConflict || errCode(rec) != shortCodeTakenCode {
			t.Fatalf("taken: status %d code %q", rec.Code, errCode(rec))
		}
		body = `{"label":"Dock E","price_item_id":"` + priceItemID + `","short_code":"dock e"}`
		rec = do(http.MethodPatch, "/harbor/payment-targets/"+other.ID, body)
		if rec.Code != http.StatusBadRequest || errCode(rec) != shortCodeInvalidCode {
			t.Fatalf("invalid: status %d code %q", rec.Code, errCode(rec))
		}
	})

	t.Run("archived target resolves to not-found", func(t *testing.T) {
		_, c := create("Dock F", "dock-f")
		if rec := do(http.MethodPost, "/harbor/payment-targets/"+c.ID+"/archive", ""); rec.Code != http.StatusOK {
			t.Fatalf("archive: status %d", rec.Code)
		}
		if got := location(do(http.MethodGet, "/q/dock-f", "")); got != "/qr/not-found" {
			t.Fatalf("Location = %q, want not-found", got)
		}
	})

	t.Run("inactive price item resolves to not-found", func(t *testing.T) {
		var inactiveID string
		if err := db.QueryRow(ctx,
			`INSERT INTO price_items (club_id, category, name, amount, is_active)
			 VALUES ($1, 'guest_harbor', 'Synthetic retired night', 150, false) RETURNING id`, clubID,
		).Scan(&inactiveID); err != nil {
			t.Fatalf("insert inactive price item: %v", err)
		}
		mustExec(t, db, ctx,
			`INSERT INTO harbor_payment_targets (club_id, token, label, price_item_id, short_code)
			 VALUES ($1, $2, 'Dock G', $3, 'dock-g')`, clubID, genCheckoutToken(), inactiveID)
		if got := location(do(http.MethodGet, "/q/dock-g", "")); got != "/qr/not-found" {
			t.Fatalf("Location = %q, want not-found", got)
		}
	})

	t.Run("another club's code does not resolve", func(t *testing.T) {
		otherClub := "33333333-3333-3333-3333-" + testutil.RandomHex(6)
		mustExec(t, db, ctx, "INSERT INTO clubs (id, slug, name) VALUES ($1, $2, 'Other Synthetic Club')", otherClub, "o-"+testutil.RandomHex(4))
		var otherItem string
		if err := db.QueryRow(ctx,
			`INSERT INTO price_items (club_id, category, name, amount)
			 VALUES ($1, 'guest_harbor', 'Other night', 100) RETURNING id`, otherClub,
		).Scan(&otherItem); err != nil {
			t.Fatalf("insert other price item: %v", err)
		}
		mustExec(t, db, ctx,
			`INSERT INTO harbor_payment_targets (club_id, token, label, price_item_id, short_code)
			 VALUES ($1, $2, 'Other dock', $3, 'other')`, otherClub, genCheckoutToken(), otherItem)
		if got := location(do(http.MethodGet, "/q/other", "")); got != "/qr/not-found" {
			t.Fatalf("Location = %q, want not-found", got)
		}
	})
}
