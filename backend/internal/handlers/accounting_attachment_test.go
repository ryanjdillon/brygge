package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// Synthetic file heads, enough for content sniffing.
const (
	pdfBody  = "%PDF-1.4\n% synthetic voucher\n"
	pngBody  = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR synthetic"
	heicBody = "\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic synthetic"
	svgBody  = `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
)

// memStore is an in-memory attachmentStore that records every call.
type memStore struct {
	mu       sync.Mutex
	disabled bool
	objects  map[string][]byte
	uploads  []string
	deletes  []string
	// deleteCtxErr is the context error each Delete saw.
	deleteCtxErr []error
	presigned    []url.Values
	// onUpload runs after an object is stored, to change the entry (or the
	// request) while the upload is in flight.
	onUpload func()
}

func newMemStore() *memStore { return &memStore{objects: map[string][]byte{}} }

func (m *memStore) IsConfigured() bool { return !m.disabled }

func (m *memStore) Upload(_ context.Context, key string, r interface {
	Read([]byte) (int, error)
}, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if _, ok := m.objects[key]; ok {
		m.mu.Unlock()
		return fmt.Errorf("memStore: %s overwritten", key)
	}
	m.objects[key] = b
	m.uploads = append(m.uploads, key)
	hook := m.onUpload
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (m *memStore) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	m.deletes = append(m.deletes, key)
	m.deleteCtxErr = append(m.deleteCtxErr, ctx.Err())
	return nil
}

func (m *memStore) PresignedURLWithParams(_ context.Context, key string, _ time.Duration, params url.Values) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.presigned = append(m.presigned, params)
	return "https://s3.invalid/" + key + "?" + params.Encode(), nil
}

func attachmentRequest(t *testing.T, path, filename, contentType, body string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", contentType)
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(body))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// The attachment upload checks the entry is the caller's club's before it
// stores anything, replaces only a draft's voucher on the ungated route, and
// on the gated route adds a posted bilag's first voucher but never replaces
// one. No object is ever overwritten, and every change is audited (BRY-675).
func TestJournalAttachmentUpload(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool)
	other := fin.NewClub(t, pool)

	store := newMemStore()
	h := NewAccountingHandler(svc, audit.NewService(pool, zerolog.Nop()), nil, nil, nil, zerolog.Nop())
	h.s3 = store

	// The gates are the router's (TestLedgerRoutesGateMoneyMovesForBoard);
	// this mounts without them to test the handlers' own refusals.
	r := chi.NewRouter()
	h.MountLedgerRoutes(r)
	claims := &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	uploadAs := func(path, filename, contentType, body string) *httptest.ResponseRecorder {
		return serve(attachmentRequest(t, path, filename, contentType, body))
	}
	upload := func(path, filename string) *httptest.ResponseRecorder {
		return uploadAs(path, filename, "application/pdf", pdfBody)
	}

	entryNumber := 0
	newEntry := func(c *fin.Club, status, attachment string) string {
		t.Helper()
		entryNumber++
		var att *string
		if attachment != "" {
			att = &attachment
		}
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, status, attachment_url, created_by)
			 VALUES ($1, $2, $3, make_date($4, 3, 1), 'Synthetic bilag', $5::journal_status, $6, $7) RETURNING id`,
			c.ID, c.PeriodID, entryNumber, c.Year, status, att, c.Treasurer.ID,
		).Scan(&id); err != nil {
			t.Fatalf("entry: %v", err)
		}
		return id
	}
	attachmentOf := func(id string) string {
		t.Helper()
		var key *string
		if err := pool.QueryRow(ctx, `SELECT attachment_url FROM journal_entries WHERE id = $1`, id).Scan(&key); err != nil {
			t.Fatalf("read attachment: %v", err)
		}
		if key == nil {
			return ""
		}
		return *key
	}
	auditRows := func(id string) []map[string]any {
		t.Helper()
		rows, err := pool.Query(ctx,
			`SELECT details::text FROM audit_log
			  WHERE action = $1 AND resource = 'journal_entry' AND resource_id = $2
			  ORDER BY created_at`, audit.ActionJournalAttachmentUploaded, id)
		if err != nil {
			t.Fatalf("audit: %v", err)
		}
		defer rows.Close()
		var out []map[string]any
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			m := map[string]any{}
			_ = json.Unmarshal([]byte(s), &m)
			out = append(out, m)
		}
		return out
	}
	code := func(rec *httptest.ResponseRecorder) string {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Code
	}
	path := func(id string) string { return "/journal/" + id + "/attachment" }
	gated := func(id string) string { return "/journal/" + id + "/attachment/posted" }
	base := func(key string) string { return key[strings.LastIndex(key, "/")+1:] }

	t.Run("foreign, unknown or malformed entry uploads nothing", func(t *testing.T) {
		foreign := newEntry(other, "draft", "")
		for _, p := range []string{
			path(foreign), gated(foreign),
			path("00000000-0000-0000-0000-000000000001"),
			path("not-a-uuid"),
		} {
			if rec := upload(p, "kvittering.pdf"); rec.Code != http.StatusNotFound {
				t.Errorf("%s: %d %s, want 404", p, rec.Code, rec.Body)
			}
		}
		if len(store.uploads) != 0 {
			t.Errorf("uploaded %v before the club check", store.uploads)
		}
		if got := attachmentOf(foreign); got != "" {
			t.Errorf("foreign entry's attachment = %q", got)
		}
	})

	t.Run("draft voucher is replaced under a new key", func(t *testing.T) {
		id := newEntry(club, "draft", "")
		first := upload(path(id), "kvittering.pdf")
		second := upload(path(id), "kvittering.pdf")
		if first.Code != http.StatusOK || second.Code != http.StatusOK {
			t.Fatalf("uploads: %d %s / %d %s", first.Code, first.Body, second.Code, second.Body)
		}
		var a, b struct {
			Key string `json:"attachment_url"`
		}
		_ = json.Unmarshal(first.Body.Bytes(), &a)
		_ = json.Unmarshal(second.Body.Bytes(), &b)
		prefix := fmt.Sprintf("clubs/%s/receipts/%s/", club.ID, id)
		if a.Key == b.Key || !strings.HasPrefix(a.Key, prefix) || !strings.HasSuffix(b.Key, "-kvittering.pdf") {
			t.Errorf("keys %q, %q: want two distinct keys under %s", a.Key, b.Key, prefix)
		}
		if got := attachmentOf(id); got != b.Key {
			t.Errorf("attachment = %q, want %q", got, b.Key)
		}
		if _, ok := store.objects[a.Key]; !ok {
			t.Errorf("replaced draft voucher %q was removed", a.Key)
		}
		rows := auditRows(id)
		if len(rows) != 2 {
			t.Fatalf("audit rows = %v, want 2", rows)
		}
		if rows[0]["previous_object"] != nil || rows[0]["filename"] != "kvittering.pdf" ||
			rows[0]["object_id"] != strings.TrimSuffix(base(a.Key), "-kvittering.pdf") || rows[0]["entry_status"] != "draft" {
			t.Errorf("first audit = %v", rows[0])
		}
		if rows[1]["previous_object"] != base(a.Key) {
			t.Errorf("second audit = %v", rows[1])
		}
		for _, row := range rows {
			if _, ok := row["key"]; ok {
				t.Errorf("audit logs the full key: %v", row)
			}
		}
	})

	t.Run("posted or voided bilag is refused on the ungated route", func(t *testing.T) {
		before := len(store.uploads)
		for _, status := range []string{"posted", "voided"} {
			id := newEntry(club, status, "")
			rec := upload(path(id), "kvittering.pdf")
			if rec.Code != http.StatusConflict || code(rec) != "entry_posted_use_gated" {
				t.Errorf("%s: %d %s, want 409 entry_posted_use_gated", status, rec.Code, rec.Body)
			}
			if got := attachmentOf(id); got != "" || len(auditRows(id)) != 0 {
				t.Errorf("%s: attachment %q set or audited", status, got)
			}
		}
		if len(store.uploads) != before {
			t.Errorf("a refused upload reached the store: %v", store.uploads[before:])
		}
	})

	t.Run("gated route adds a posted bilag's first voucher", func(t *testing.T) {
		id := newEntry(club, "posted", "")
		rec := upload(gated(id), "faktura.pdf")
		if rec.Code != http.StatusOK {
			t.Fatalf("attach: %d %s", rec.Code, rec.Body)
		}
		key := attachmentOf(id)
		if _, ok := store.objects[key]; !ok || !strings.HasSuffix(key, "-faktura.pdf") {
			t.Errorf("attachment %q not stored", key)
		}
		rows := auditRows(id)
		if len(rows) != 1 || rows[0]["entry_status"] != "posted" || rows[0]["filename"] != "faktura.pdf" ||
			rows[0]["previous_object"] != nil || rows[0]["money_gated"] != true {
			t.Errorf("audit = %v", rows)
		}
	})

	t.Run("gated route never replaces a posted bilag's voucher", func(t *testing.T) {
		original := fmt.Sprintf("clubs/%s/receipts/original.pdf", club.ID)
		store.objects[original] = []byte("%PDF-original")
		before := len(store.uploads)
		for _, status := range []string{"posted", "voided"} {
			id := newEntry(club, status, original)
			rec := upload(gated(id), "kvittering.pdf")
			if rec.Code != http.StatusConflict || code(rec) != "entry_has_voucher" {
				t.Errorf("%s: %d %s, want 409 entry_has_voucher", status, rec.Code, rec.Body)
			}
			if got := attachmentOf(id); got != original {
				t.Errorf("%s: attachment = %q, want the original %q", status, got, original)
			}
			if len(auditRows(id)) != 0 {
				t.Errorf("%s: a refusal was audited", status)
			}
		}
		if len(store.uploads) != before {
			t.Errorf("a refused upload reached the store: %v", store.uploads[before:])
		}
		if _, ok := store.objects[original]; !ok {
			t.Error("the original voucher object is gone")
		}
	})

	t.Run("file type is checked against the allowlist and the file's bytes", func(t *testing.T) {
		id := newEntry(club, "draft", "")
		before := len(store.uploads)
		for _, tc := range []struct{ name, ct, body string }{
			{"svg", "image/svg+xml", svgBody},
			{"svg claimed as png", "image/png", svgBody},
			{"pdf claimed as png", "image/png", pdfBody},
			{"png claimed as pdf", "application/pdf", pngBody},
			{"pdf claimed as heic", "image/heic", pdfBody},
			{"html", "text/html", "<html></html>"},
			{"gif", "image/gif", "GIF89a synthetic"},
		} {
			rec := uploadAs(path(id), "kvittering", tc.ct, tc.body)
			if rec.Code != http.StatusBadRequest || code(rec) != "unsupported_file_type" {
				t.Errorf("%s: %d %s, want 400 unsupported_file_type", tc.name, rec.Code, rec.Body)
			}
		}
		if len(store.uploads) != before || attachmentOf(id) != "" {
			t.Errorf("a refused file was stored: %v", store.uploads[before:])
		}

		for _, tc := range []struct{ ct, body, filename, suffix string }{
			{"image/png", pngBody, "bilde.png", "-bilde.png"},
			{"image/heic", heicBody, "IMG 0001.HEIC", "-IMG0001.HEIC"},
			{"application/pdf; charset=binary", pdfBody, "skann.png", "-skann.pdf"},
			{"image/png", pngBody, "../../Faktura (kopi)", "-Fakturakopi.png"},
		} {
			rec := uploadAs(path(id), tc.filename, tc.ct, tc.body)
			if rec.Code != http.StatusOK {
				t.Errorf("%s: %d %s, want 200", tc.ct, rec.Code, rec.Body)
				continue
			}
			if key := attachmentOf(id); !strings.HasSuffix(key, tc.suffix) {
				t.Errorf("%s %q: key %q, want suffix %q", tc.ct, tc.filename, key, tc.suffix)
			}
		}
	})

	t.Run("entry posted while the file uploads keeps no link to it", func(t *testing.T) {
		id := newEntry(club, "draft", "")
		store.onUpload = func() {
			if _, err := pool.Exec(ctx, `UPDATE journal_entries SET status = 'posted' WHERE id = $1`, id); err != nil {
				t.Errorf("post: %v", err)
			}
		}
		defer func() { store.onUpload = nil }()
		before := len(store.uploads)
		rec := upload(path(id), "kvittering.pdf")
		if rec.Code != http.StatusConflict || code(rec) != "attachment_conflict" {
			t.Fatalf("%d %s, want 409 attachment_conflict", rec.Code, rec.Body)
		}
		if got := attachmentOf(id); got != "" {
			t.Errorf("attachment = %q, want none", got)
		}
		if len(store.uploads) != before+1 {
			t.Fatalf("uploads %v", store.uploads[before:])
		}
		orphan := store.uploads[before]
		if _, ok := store.objects[orphan]; ok {
			t.Errorf("unlinked upload %q left in the store", orphan)
		}
		if len(auditRows(id)) != 0 {
			t.Error("an unlinked upload was audited")
		}
	})

	t.Run("a failed link removes the upload", func(t *testing.T) {
		id := newEntry(club, "draft", "")
		// A trigger makes the link UPDATE fail after the object is stored.
		store.onUpload = func() {
			if _, err := pool.Exec(ctx, `
				CREATE OR REPLACE FUNCTION bry675_fail() RETURNS trigger LANGUAGE plpgsql AS
				$$ BEGIN RAISE EXCEPTION 'synthetic link failure'; END $$;
				CREATE TRIGGER bry675_fail BEFORE UPDATE ON journal_entries
				FOR EACH ROW EXECUTE FUNCTION bry675_fail();`); err != nil {
				t.Errorf("trigger: %v", err)
			}
		}
		defer func() {
			store.onUpload = nil
			_, _ = pool.Exec(ctx, `DROP TRIGGER IF EXISTS bry675_fail ON journal_entries; DROP FUNCTION IF EXISTS bry675_fail()`)
		}()
		before := len(store.uploads)
		rec := upload(path(id), "kvittering.pdf")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%d %s, want 500", rec.Code, rec.Body)
		}
		orphan := store.uploads[before]
		if _, ok := store.objects[orphan]; ok {
			t.Errorf("unlinked upload %q left in the store", orphan)
		}
		if attachmentOf(id) != "" || len(auditRows(id)) != 0 {
			t.Error("a failed link was saved or audited")
		}
	})

	t.Run("cleanup runs after the request is cancelled", func(t *testing.T) {
		id := newEntry(club, "draft", "")
		reqCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store.onUpload = cancel
		defer func() { store.onUpload = nil }()
		before, beforeDel := len(store.uploads), len(store.deletes)
		req := attachmentRequest(t, path(id), "kvittering.pdf", "application/pdf", pdfBody).WithContext(reqCtx)
		rec := serve(req)
		if rec.Code == http.StatusOK {
			t.Fatalf("cancelled request linked the voucher: %s", rec.Body)
		}
		orphan := store.uploads[before]
		if _, ok := store.objects[orphan]; ok {
			t.Errorf("unlinked upload %q left in the store", orphan)
		}
		if len(store.deleteCtxErr) != beforeDel+1 || store.deleteCtxErr[beforeDel] != nil {
			t.Errorf("cleanup ran on a cancelled context: %v", store.deleteCtxErr[beforeDel:])
		}
	})

	t.Run("a first voucher is added in any year but a reference year", func(t *testing.T) {
		period := func(year int, status string, reference bool) string {
			t.Helper()
			var id string
			if err := pool.QueryRow(ctx,
				`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status, reference_only)
				 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), $3::fiscal_period_status, $4) RETURNING id`,
				club.ID, year, status, reference,
			).Scan(&id); err != nil {
				t.Fatalf("period %d: %v", year, err)
			}
			return id
		}
		entryIn := func(periodID string, year int, status string) string {
			t.Helper()
			entryNumber++
			var id string
			if err := pool.QueryRow(ctx,
				`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, status, created_by)
				 VALUES ($1, $2, $3, make_date($4, 6, 1), 'Synthetic bilag', $5::journal_status, $6) RETURNING id`,
				club.ID, periodID, entryNumber, year, status, club.Treasurer.ID,
			).Scan(&id); err != nil {
				t.Fatalf("entry: %v", err)
			}
			return id
		}
		closed := period(2025, "closed", false)
		locked := period(2024, "locked", false)
		reference := period(2023, "closed", true)
		openRef := period(2022, "open", true)

		for name, id := range map[string]string{
			"posted in a closed year": entryIn(closed, 2025, "posted"),
			"voided in a closed year": entryIn(closed, 2025, "voided"),
			"posted in a locked year": entryIn(locked, 2024, "posted"),
			"voided in an open year":  entryIn(club.PeriodID, club.Year, "voided"),
		} {
			if rec := upload(gated(id), "kvittering.pdf"); rec.Code != http.StatusOK {
				t.Errorf("%s: %d %s, want 200", name, rec.Code, rec.Body)
			}
			if attachmentOf(id) == "" {
				t.Errorf("%s: no voucher linked", name)
			}
		}

		before := len(store.uploads)
		for name, id := range map[string]string{
			"posted in a reference year":       entryIn(reference, 2023, "posted"),
			"voided in an open reference year": entryIn(openRef, 2022, "voided"),
			"dated in a reference year":        entryIn(club.PeriodID, 2023, "posted"),
		} {
			rec := upload(gated(id), "kvittering.pdf")
			if rec.Code != http.StatusConflict || code(rec) != "reference_period" {
				t.Errorf("%s: %d %s, want 409 reference_period", name, rec.Code, rec.Body)
			}
			if attachmentOf(id) != "" || len(auditRows(id)) != 0 {
				t.Errorf("%s: voucher linked or audited", name)
			}
		}
		if len(store.uploads) != before {
			t.Errorf("a refused upload reached the store: %v", store.uploads[before:])
		}

		// A draft in a reference year still takes its voucher.
		if rec := upload(path(entryIn(reference, 2023, "draft")), "kvittering.pdf"); rec.Code != http.StatusOK {
			t.Errorf("draft in a reference year: %d %s, want 200", rec.Code, rec.Body)
		}
	})

	t.Run("gated route replaces a draft's voucher", func(t *testing.T) {
		prev := fmt.Sprintf("clubs/%s/receipts/draft-prev.pdf", club.ID)
		store.objects[prev] = []byte("%PDF-prev")
		id := newEntry(club, "draft", prev)
		if rec := upload(gated(id), "kvittering.pdf"); rec.Code != http.StatusOK {
			t.Fatalf("attach: %d %s", rec.Code, rec.Body)
		}
		if got := attachmentOf(id); got == prev {
			t.Error("draft voucher not replaced")
		}
		if _, ok := store.objects[prev]; !ok {
			t.Error("replaced draft object removed")
		}
		if rows := auditRows(id); len(rows) != 1 || rows[0]["previous_object"] != "draft-prev.pdf" {
			t.Errorf("audit = %v", rows)
		}
	})

	t.Run("get serves only the entry's own folder, inline with a fixed type", func(t *testing.T) {
		get := func(id string) *httptest.ResponseRecorder {
			return serve(httptest.NewRequest(http.MethodGet, path(id), nil))
		}
		own := newEntry(club, "posted", "")
		key := fmt.Sprintf("clubs/%s/receipts/%s/%s-kvittering.pdf", club.ID, own, "6f1c2a3e-0000-4000-8000-000000000001")
		if _, err := pool.Exec(ctx, `UPDATE journal_entries SET attachment_url = $1 WHERE id = $2`, key, own); err != nil {
			t.Fatal(err)
		}
		rec := get(own)
		if rec.Code != http.StatusOK {
			t.Fatalf("own voucher: %d %s", rec.Code, rec.Body)
		}
		p := store.presigned[len(store.presigned)-1]
		if p.Get("response-content-type") != "application/pdf" ||
			p.Get("response-content-disposition") != `inline; filename="kvittering.pdf"` {
			t.Errorf("presign params = %v", p)
		}

		// A legacy key with no voucher extension is served as a download.
		odd := newEntry(club, "draft", "")
		if _, err := pool.Exec(ctx, `UPDATE journal_entries SET attachment_url = $1 WHERE id = $2`,
			fmt.Sprintf("clubs/%s/receipts/%s/notat.txt", club.ID, odd), odd); err != nil {
			t.Fatal(err)
		}
		if rec := get(odd); rec.Code != http.StatusOK {
			t.Errorf("legacy key: %d", rec.Code)
		} else if p := store.presigned[len(store.presigned)-1]; p.Get("response-content-type") != "application/octet-stream" ||
			!strings.HasPrefix(p.Get("response-content-disposition"), "attachment;") {
			t.Errorf("legacy presign params = %v", p)
		}

		// A key outside the entry's folder (another club's object, another
		// entry's folder) is not served.
		otherClub := newEntry(club, "posted", fmt.Sprintf("clubs/%s/receipts/%s/x-kvittering.pdf", other.ID, own))
		otherEntry := newEntry(club, "posted", key)
		for name, id := range map[string]string{"other club's key": otherClub, "other entry's key": otherEntry} {
			if rec := get(id); rec.Code != http.StatusNotFound {
				t.Errorf("%s: %d, want 404", name, rec.Code)
			}
		}

		foreign := newEntry(other, "posted", fmt.Sprintf("clubs/%s/receipts/x/y.pdf", other.ID))
		for name, id := range map[string]string{"foreign entry": foreign, "malformed id": "not-a-uuid", "unknown": "00000000-0000-0000-0000-000000000001"} {
			if rec := get(id); rec.Code != http.StatusNotFound {
				t.Errorf("%s: %d, want 404", name, rec.Code)
			}
		}

		store.disabled = true
		defer func() { store.disabled = false }()
		if rec := get(own); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("disabled store: %d, want 503", rec.Code)
		}
		h.s3 = nil
		defer func() { h.s3 = store }()
		if rec := get(own); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("no store: %d, want 503", rec.Code)
		}
	})

	t.Run("create refuses a client-sent attachment_url", func(t *testing.T) {
		body := `{"fiscal_period_id":"` + club.PeriodID + `","entry_date":"2026-03-01","description":"Synthetic",
			"attachment_url":"clubs/` + other.ID + `/receipts/x/y.pdf",
			"lines":[{"account_code":"1920","debit":1},{"account_code":"3000","credit":1}]}`
		req := httptest.NewRequest(http.MethodPost, "/journal", strings.NewReader(body))
		rec := serve(req)
		if rec.Code != http.StatusBadRequest || code(rec) != "attachment_url_not_accepted" {
			t.Errorf("%d %s, want 400 attachment_url_not_accepted", rec.Code, rec.Body)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND description = 'Synthetic'`, club.ID).Scan(&n); err != nil || n != 0 {
			t.Errorf("entries created: %d (%v)", n, err)
		}
	})
}

func TestVoucherFilename(t *testing.T) {
	long := strings.Repeat("a", 300) + ".pdf"
	tests := []struct {
		name, filename, ct, want string
	}{
		{"plain", "kvittering.pdf", "application/pdf", "kvittering.pdf"},
		{"path stripped", "../../other/kvittering.pdf", "application/pdf", "kvittering.pdf"},
		{"windows path", `C:\Users\x\scan.PDF`, "application/pdf", "scan.PDF"},
		{"unsafe characters dropped", "fak tura/(1)ø.pdf", "application/pdf", "1.pdf"},
		{"empty", "", "application/pdf", "attachment.pdf"},
		{"dot", ".", "image/png", "attachment.png"},
		{"hidden", ".htaccess", "image/png", "htaccess.png"},
		{"extension follows the type", "bilde.png", "application/pdf", "bilde.pdf"},
		{"jpeg keeps jpg", "foto.JPG", "image/jpeg", "foto.JPG"},
		{"capped", long, "application/pdf", strings.Repeat("a", 96) + ".pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := voucherFilename(tt.filename, tt.ct)
			if got != tt.want {
				t.Errorf("voucherFilename(%q) = %q, want %q", tt.filename, got, tt.want)
			}
			if len(got) > maxVoucherName {
				t.Errorf("%q is longer than %d bytes", got, maxVoucherName)
			}
		})
	}
}

func TestVerifyVoucherType(t *testing.T) {
	tests := []struct {
		name, claimed, body string
		ok                  bool
	}{
		{"pdf", "application/pdf", pdfBody, true},
		{"png", "image/png", pngBody, true},
		{"jpeg", "image/jpeg", "\xff\xd8\xff\xe0\x00\x10JFIF\x00", true},
		{"webp", "image/webp", "RIFF\x00\x00\x00\x00WEBPVP8 ", true},
		{"heic", "image/heic", heicBody, true},
		{"heic mif1 brand", "image/heic", "\x00\x00\x00\x18ftypmif1\x00\x00\x00\x00", true},
		{"mp4 claimed as heic", "image/heic", "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00", false},
		{"svg", "image/svg+xml", svgBody, false},
		{"mismatch", "image/png", pdfBody, false},
		{"empty file", "application/pdf", "", false},
		{"no claim", "", pdfBody, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := strings.NewReader(tt.body)
			_, ok := verifyVoucherType(f, tt.claimed)
			if ok != tt.ok {
				t.Errorf("ok = %v, want %v", ok, tt.ok)
			}
			if pos, _ := f.Seek(0, io.SeekCurrent); ok && pos != 0 {
				t.Errorf("file not rewound: at %d", pos)
			}
		})
	}
}

func TestAttachmentRefusal(t *testing.T) {
	tests := []struct {
		status     string
		hasVoucher bool
		gated      bool
		reference  bool
		want       string
	}{
		{"draft", false, false, false, ""},
		{"draft", true, false, false, ""},
		{"draft", true, true, false, ""},
		{"draft", false, true, true, ""},
		{"draft", true, false, true, ""},
		{"posted", false, false, false, "entry_posted_use_gated"},
		{"voided", false, false, false, "entry_posted_use_gated"},
		{"posted", false, false, true, "entry_posted_use_gated"},
		{"posted", false, true, false, ""},
		{"voided", false, true, false, ""},
		{"posted", false, true, true, "reference_period"},
		{"voided", true, true, true, "reference_period"},
		{"posted", true, true, false, "entry_has_voucher"},
		{"voided", true, true, false, "entry_has_voucher"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/voucher=%v/gated=%v/reference=%v", tt.status, tt.hasVoucher, tt.gated, tt.reference), func(t *testing.T) {
			if got := attachmentRefusal(tt.status, tt.hasVoucher, tt.gated, tt.reference); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if tt.want != "" && tt.want != referencePeriodCode && attachmentRefusalMessage[tt.want] == "" {
				t.Errorf("no message for %q", tt.want)
			}
		})
	}
}
