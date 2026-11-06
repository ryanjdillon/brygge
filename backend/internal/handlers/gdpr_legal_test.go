package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// legalClub is one club on a shared deployment database, with the handler
// that deployment would run for it (the public route reads ClubSlug).
type legalClub struct {
	id     string
	admin  string
	member string
	router chi.Router
}

func newLegalClub(t *testing.T, db *pgxpool.Pool) legalClub {
	t.Helper()
	clubID := testutil.SeedClub(t, db)
	var slug string
	if err := db.QueryRow(context.Background(), `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatalf("read club slug: %v", err)
	}
	adminID, _ := testutil.SeedUser(t, db, clubID, []string{"admin"})
	memberID, _ := testutil.SeedUser(t, db, clubID, []string{"member"})

	h := NewGDPRHandler(db, &config.Config{Port: 8080, ClubSlug: slug}, zerolog.Nop())
	r := chi.NewRouter()
	r.Get("/legal/{docType}", h.HandleGetLegalDocument)
	r.Group(func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Post("/members/me/consent", h.HandleRecordConsent)
		r.Get("/admin/gdpr/legal", h.HandleAdminListLegalDocuments)
		r.Post("/admin/gdpr/legal", h.HandleAdminCreateLegalDocument)
	})
	return legalClub{id: clubID, admin: adminID, member: memberID, router: r}
}

// do sends a request as userID with the given roles; an empty userID is an
// anonymous visitor.
func (c legalClub) do(t *testing.T, method, path, userID string, roles []string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("Authorization", authHeader(generateTestToken(userID, c.id, roles)))
	}
	rec := httptest.NewRecorder()
	c.router.ServeHTTP(rec, req)
	return rec
}

// seedLegalDoc inserts a document; publishedAgo is a Postgres interval before
// now, or "" for a draft.
func seedLegalDoc(t *testing.T, db *pgxpool.Pool, clubID, docType, version, publishedAgo string) {
	t.Helper()
	var published any
	if publishedAgo != "" {
		published = publishedAgo
	}
	if _, err := db.Exec(context.Background(),
		`INSERT INTO legal_documents (club_id, doc_type, version, content, published_at)
		 VALUES ($1, $2, $3, 'Synthetic text ' || $3,
		         CASE WHEN $4::interval IS NULL THEN NULL ELSE now() - $4::interval END)`,
		clubID, docType, version, published,
	); err != nil {
		t.Fatalf("seed legal document: %v", err)
	}
}

func decodeLegalBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func newLegalClubs(t *testing.T) (db *pgxpool.Pool, a, b legalClub) {
	t.Helper()
	testutil.SkipIfNoDB(t)
	db = testutil.SetupTestDB(t)
	a, b = newLegalClub(t, db), newLegalClub(t, db)

	seedLegalDoc(t, db, a.id, "privacy", "A-1", "2 days")
	seedLegalDoc(t, db, a.id, "privacy", "A-2", "1 day")
	seedLegalDoc(t, db, a.id, "privacy", "A-draft", "")
	// Club B published most recently of all, so an unscoped query serves B's.
	seedLegalDoc(t, db, b.id, "privacy", "B-1", "1 hour")
	seedLegalDoc(t, db, b.id, "statutes", "B-statutes", "1 hour")
	return db, a, b
}

func TestIntegration_LegalDocumentIsTheDeployClubs(t *testing.T) {
	_, a, b := newLegalClubs(t)

	for _, tc := range []struct {
		name    string
		club    legalClub
		docType string
		status  int
		version string
	}{
		{"club A gets its latest published", a, "privacy", http.StatusOK, "A-2"},
		{"club B gets its own", b, "privacy", http.StatusOK, "B-1"},
		{"club A never gets club B's statutes", a, "statutes", http.StatusNotFound, ""},
		{"club B's statutes", b, "statutes", http.StatusOK, "B-statutes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.club.do(t, http.MethodGet, "/legal/"+tc.docType, "", nil, "")
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status == http.StatusOK {
				if got := decodeLegalBody(t, rec)["version"]; got != tc.version {
					t.Errorf("version = %v, want %s", got, tc.version)
				}
			}
		})
	}
}

func TestIntegration_AdminLegalDocumentsAreClubScoped(t *testing.T) {
	db, a, b := newLegalClubs(t)
	admin := []string{"admin"}

	rec := a.do(t, http.MethodPost, "/admin/gdpr/legal", a.admin, admin,
		`{"doc_type":"terms","version":"A-terms","content":"Synthetic terms","publish":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var owner string
	if err := db.QueryRow(context.Background(),
		`SELECT club_id::text FROM legal_documents WHERE version = 'A-terms'`).Scan(&owner); err != nil {
		t.Fatalf("read created row: %v", err)
	}
	if owner != a.id {
		t.Errorf("created document belongs to club %s, want %s", owner, a.id)
	}

	versionsOf := func(c legalClub) []string {
		rec := c.do(t, http.MethodGet, "/admin/gdpr/legal", c.admin, admin, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list: status = %d; body: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Documents []struct {
				Version string `json:"version"`
			} `json:"documents"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		var out []string
		for _, d := range body.Documents {
			out = append(out, d.Version)
		}
		slices.Sort(out)
		return out
	}
	if got, want := versionsOf(a), []string{"A-1", "A-2", "A-draft", "A-terms"}; !slices.Equal(got, want) {
		t.Errorf("club A list = %v, want %v", got, want)
	}
	if got, want := versionsOf(b), []string{"B-1", "B-statutes"}; !slices.Equal(got, want) {
		t.Errorf("club B list = %v, want %v", got, want)
	}
}

func TestIntegration_LatestLegalVersions(t *testing.T) {
	db, a, b := newLegalClubs(t)
	ctx := context.Background()

	versions, missing, err := LatestLegalVersions(ctx, db, a.id, []string{"privacy", "statutes", "terms"})
	if err != nil {
		t.Fatalf("LatestLegalVersions: %v", err)
	}
	if len(versions) != 1 || versions["privacy"] != "A-2" {
		t.Errorf("club A versions = %v, want only privacy=A-2", versions)
	}
	if want := []string{"statutes", "terms"}; !slices.Equal(missing, want) {
		t.Errorf("club A missing = %v, want %v", missing, want)
	}

	versions, missing, err = LatestLegalVersions(ctx, db, b.id, []string{"statutes", "privacy"})
	if err != nil {
		t.Fatalf("LatestLegalVersions: %v", err)
	}
	if versions["privacy"] != "B-1" || versions["statutes"] != "B-statutes" || len(missing) != 0 {
		t.Errorf("club B = %v missing %v, want privacy=B-1 statutes=B-statutes, none missing", versions, missing)
	}

	if _, _, err := LatestLegalVersions(ctx, db, a.id, []string{"newsletter"}); err == nil {
		t.Error("a type with no document should be an error, not a missing document")
	}
}

func TestIntegration_RecordConsentStoresTheClubsVersion(t *testing.T) {
	db, a, _ := newLegalClubs(t)
	member := []string{"member"}

	for _, tc := range []struct {
		name        string
		consentType string
		body        string
		status      int
		version     string // the stored version, or the 409's code
	}{
		{"expected version is current", "privacy", `{"consent_type":"privacy","version":"A-2"}`, http.StatusCreated, "A-2"},
		{"no expected version", "privacy", `{"consent_type":"privacy"}`, http.StatusCreated, "A-2"},
		{"superseded version is stale", "privacy", `{"consent_type":"privacy","version":"A-1"}`, http.StatusConflict, codeLegalDocumentStale},
		{"another club's version is stale", "privacy", `{"consent_type":"privacy","version":"B-1"}`, http.StatusConflict, codeLegalDocumentStale},
		{"newsletter has no document", "newsletter", `{"consent_type":"newsletter","version":"9"}`, http.StatusCreated, ""},
		{"unpublished document is refused", "statutes", `{"consent_type":"statutes","version":"B-statutes"}`, http.StatusConflict, codeLegalDocumentUnpublished},
	} {
		t.Run(tc.name, func(t *testing.T) {
			consentType := tc.consentType
			before := gdprCount(t, db,
				`SELECT count(*) FROM user_consents WHERE user_id = $1 AND consent_type = $2`, a.member, consentType)

			rec := a.do(t, http.MethodPost, "/members/me/consent", a.member, member, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.status, rec.Body.String())
			}
			after := gdprCount(t, db,
				`SELECT count(*) FROM user_consents WHERE user_id = $1 AND consent_type = $2`, a.member, consentType)

			if tc.status == http.StatusConflict {
				body := decodeLegalBody(t, rec)
				if body["code"] != tc.version {
					t.Errorf("code = %v, want %s", body["code"], tc.version)
				}
				if tc.version == codeLegalDocumentStale && body["version"] != "A-2" {
					t.Errorf("stale answer names version %v, want the current A-2", body["version"])
				}
				if after != before {
					t.Errorf("a refused consent wrote %d rows", after-before)
				}
				return
			}

			if got := decodeLegalBody(t, rec)["version"]; got != tc.version {
				t.Errorf("response version = %v, want %q", got, tc.version)
			}
			if after != before+1 {
				t.Fatalf("consent rows %d -> %d, want one more", before, after)
			}
			var stored string
			if err := db.QueryRow(context.Background(),
				`SELECT version FROM user_consents WHERE user_id = $1 AND consent_type = $2
				 ORDER BY granted_at DESC, id DESC LIMIT 1`, a.member, consentType,
			).Scan(&stored); err != nil {
				t.Fatalf("read stored consent: %v", err)
			}
			if stored != tc.version {
				t.Errorf("stored version = %q, want %q", stored, tc.version)
			}
		})
	}
}

func TestIntegration_LegalDocumentUnknownSlugIs404(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	a := newLegalClub(t, db)
	seedLegalDoc(t, db, a.id, "privacy", "A-1", "1 hour")

	// A deploy whose slug names no club must not fall back to any club's text.
	h := NewGDPRHandler(db, &config.Config{Port: 8080, ClubSlug: "no-such-club-" + testutil.RandomHex(4)}, zerolog.Nop())
	r := chi.NewRouter()
	r.Get("/legal/{docType}", h.HandleGetLegalDocument)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/legal/privacy", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_LegalVersionTieBreak(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	c := newLegalClub(t, db)
	ctx := context.Background()

	// privacy: same published_at, the later created_at wins.
	// statutes: same published_at and created_at, the greater id wins.
	for _, row := range []struct{ id, docType, version, createdAt string }{
		{"00000000-0000-4000-8000-0000000000a1", "privacy", "older-created", "2026-01-01T10:00:00Z"},
		{"00000000-0000-4000-8000-0000000000a0", "privacy", "newer-created", "2026-01-01T11:00:00Z"},
		{"00000000-0000-4000-8000-0000000000b2", "statutes", "greater-id", "2026-01-01T10:00:00Z"},
		{"00000000-0000-4000-8000-0000000000b1", "statutes", "lesser-id", "2026-01-01T10:00:00Z"},
	} {
		if _, err := db.Exec(ctx,
			`INSERT INTO legal_documents (id, club_id, doc_type, version, content, published_at, created_at)
			 VALUES ($1, $2, $3, $4, 'Synthetic text', '2026-02-01T12:00:00Z', $5)`,
			row.id, c.id, row.docType, row.version, row.createdAt,
		); err != nil {
			t.Fatalf("seed %s: %v", row.version, err)
		}
	}

	versions, missing, err := LatestLegalVersions(ctx, db, c.id, []string{"privacy", "statutes"})
	if err != nil {
		t.Fatalf("LatestLegalVersions: %v", err)
	}
	if versions["privacy"] != "newer-created" || versions["statutes"] != "greater-id" || len(missing) != 0 {
		t.Errorf("versions = %v missing %v, want privacy=newer-created statutes=greater-id", versions, missing)
	}

	// The public read must pick the same rows the consent records.
	for docType, want := range versions {
		rec := c.do(t, http.MethodGet, "/legal/"+docType, "", nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d; body: %s", docType, rec.Code, rec.Body.String())
		}
		if got := decodeLegalBody(t, rec)["version"]; got != want {
			t.Errorf("GET %s version = %v, want %s (same as LatestLegalVersions)", docType, got, want)
		}
	}
}
