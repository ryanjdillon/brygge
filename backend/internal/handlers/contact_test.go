package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/email"
)

// fakeClubRow answers the recipient query with fixed addresses, or an error.
type fakeClubRow struct {
	secretary, chairman string
	err                 error
}

func (f fakeClubRow) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row { return f }

func (f fakeClubRow) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	*(dest[0].(*string)) = f.secretary
	*(dest[1].(*string)) = f.chairman
	return nil
}

const (
	testSecretary = "secretary@club.example"
	testChairman  = "chair@club.example"
)

type contactFixture struct {
	h      *ContactHandler
	mailer *email.MockSender
	logs   *bytes.Buffer
}

func newContactFixture(t *testing.T, row fakeClubRow) *contactFixture {
	t.Helper()
	mailer := &email.MockSender{}
	logs := &bytes.Buffer{}
	h := NewContactHandler(row, testConfig(), mailer, zerolog.New(logs))
	return &contactFixture{h: h, mailer: mailer, logs: logs}
}

func newTestContactHandler(t *testing.T) *ContactHandler {
	t.Helper()
	return newContactFixture(t, fakeClubRow{secretary: testSecretary}).h
}

func postContact(h *ContactHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleContactForm(rec, req)
	return rec
}

const validContactBody = `{"name":"Ola Nordmann","email":"ola@example.com","subject":"Test","message":"Hello there, friends"}`

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp.Error
}

func TestHandleContactFormDelivers(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{secretary: testSecretary, chairman: testChairman})

	rec := postContact(f.h, validContactBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "received" {
		t.Errorf("expected status 'received', got %q", resp["status"])
	}

	if len(f.mailer.Calls) != 1 {
		t.Fatalf("expected 1 email, got %d", len(f.mailer.Calls))
	}
	c := f.mailer.Calls[0]
	if c.To != testSecretary {
		t.Errorf("To = %q, want the secretary %q", c.To, testSecretary)
	}
	if c.Subject != "[Kontaktskjema] Test" {
		t.Errorf("Subject = %q", c.Subject)
	}
	// Only Reply-To is visitor-derived; From is the sender's own and is never
	// passed in.
	if got := c.Headers["Reply-To"]; got != "ola@example.com" {
		t.Errorf("Reply-To = %q, want the submitter", got)
	}
	if len(c.Headers) != 1 {
		t.Errorf("expected only Reply-To as an extra header, got %v", c.Headers)
	}
	for _, want := range []string{"Ola Nordmann", "ola@example.com", "Test", "Hello there, friends"} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("body missing %q: %s", want, c.HTML)
		}
	}
}

func TestHandleContactFormLogsNoPII(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{secretary: testSecretary})
	if rec := postContact(f.h, validContactBody); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	assertNoContactPII(t, f.logs.String())
}

func assertNoContactPII(t *testing.T, logs string) {
	t.Helper()
	for _, secret := range []string{"Ola Nordmann", "ola@example.com", "Hello there"} {
		if strings.Contains(logs, secret) {
			t.Errorf("log leaks %q: %s", secret, logs)
		}
	}
}

func TestHandleContactFormFallsBackToChairman(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{chairman: testChairman})

	rec := postContact(f.h, validContactBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if len(f.mailer.Calls) != 1 || f.mailer.Calls[0].To != testChairman {
		t.Fatalf("expected one email to the chairman, got %+v", f.mailer.Calls)
	}
}

func TestPickContactRecipient(t *testing.T) {
	tests := []struct {
		name, secretary, chairman, want string
	}{
		{"secretary wins", "s@club.example", "c@club.example", "s@club.example"},
		{"chairman fallback", "", "c@club.example", "c@club.example"},
		{"whitespace trimmed", "  s@club.example ", "", "s@club.example"},
		{"invalid secretary falls back", "not-an-address", "c@club.example", "c@club.example"},
		{"display name rejected", "Sec <s@club.example>", "", ""},
		{"header injection rejected", "s@club.example\r\nBcc: x@y.example", "", ""},
		{"none", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickContactRecipient(tt.secretary, tt.chairman); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleContactFormNoRecipient(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{})

	rec := postContact(f.h, validContactBody)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "contact_recipient_not_configured" {
		t.Errorf("error = %q", code)
	}
	if len(f.mailer.Calls) != 0 {
		t.Errorf("nothing should be sent, got %d", len(f.mailer.Calls))
	}
	assertNoContactPII(t, f.logs.String())
}

func TestHandleContactFormNoClubRow(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{err: pgx.ErrNoRows})

	rec := postContact(f.h, validContactBody)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleContactFormRecipientLookupFails(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{err: errors.New("connection refused")})

	rec := postContact(f.h, validContactBody)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if len(f.mailer.Calls) != 0 {
		t.Errorf("nothing should be sent")
	}
	assertNoContactPII(t, f.logs.String())
}

func TestHandleContactFormNoMailer(t *testing.T) {
	logs := &bytes.Buffer{}
	h := NewContactHandler(fakeClubRow{secretary: testSecretary}, testConfig(), nil, zerolog.New(logs))

	rec := postContact(h, validContactBody)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "mail_not_configured" {
		t.Errorf("error = %q", code)
	}
	assertNoContactPII(t, logs.String())
}

func TestHandleContactFormDeliveryFailure(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{secretary: testSecretary})
	f.mailer.Err = errors.New("smtp dial: connection refused")

	rec := postContact(f.h, validContactBody)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "received") {
		t.Errorf("a failed delivery must not claim success: %s", rec.Body.String())
	}
	logs := f.logs.String()
	if !strings.Contains(logs, `"level":"error"`) {
		t.Errorf("expected an error-level log, got %s", logs)
	}
	assertNoContactPII(t, logs)
}

func TestHandleContactFormStripsHeaderInjectionFromSubject(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{secretary: testSecretary})

	body := `{"name":"Ola\r\nBcc: evil@example.com","email":"ola@example.com",` +
		`"subject":"Hi\r\nBcc: evil@example.com\u0000 x","message":"Hello there, friends"}`
	rec := postContact(f.h, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	c := f.mailer.Calls[0]
	for _, r := range c.Subject {
		if r < 0x20 || r == 0x7f || r == ' ' {
			t.Fatalf("control character %q in subject %q", r, c.Subject)
		}
	}
	if c.Subject != "[Kontaktskjema] Hi Bcc: evil@example.com x" {
		t.Errorf("Subject = %q", c.Subject)
	}
}

func TestHandleContactFormRejectsInjectedEmail(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{secretary: testSecretary})

	body := `{"name":"Ola","email":"ola@example.com\r\nBcc: evil@example.com","subject":"Hi","message":"Hello there, friends"}`
	rec := postContact(f.h, body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if len(f.mailer.Calls) != 0 {
		t.Errorf("nothing should be sent")
	}
}

func TestHandleContactFormEscapesHTML(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{secretary: testSecretary})

	body := `{"name":"<b>Ola</b>","email":"ola@example.com","subject":"<i>x</i>","message":"<script>alert(1)</script>\nline two"}`
	rec := postContact(f.h, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	html := f.mailer.Calls[0].HTML
	if strings.Contains(html, "<script>") || strings.Contains(html, "<b>Ola") {
		t.Errorf("visitor markup not escaped: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "<br>") {
		t.Errorf("expected escaped script and a line break: %s", html)
	}
}

func TestHandleContactFormHoneypot(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{secretary: testSecretary})

	body := `{"name":"Ola Nordmann","email":"ola@example.com","subject":"Test","message":"Hello there, friends","website":"http://spam.example"}`
	rec := postContact(f.h, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "received") {
		t.Errorf("honeypot must look like success: %s", rec.Body.String())
	}
	if len(f.mailer.Calls) != 0 {
		t.Errorf("honeypot submission must not send, got %d", len(f.mailer.Calls))
	}
	if f.logs.Len() != 0 {
		t.Errorf("honeypot submission must log nothing, got %s", f.logs.String())
	}
}

func TestHandleContactFormMissingFields(t *testing.T) {
	h := newTestContactHandler(t)

	tests := []struct {
		name string
		body string
	}{
		{"missing name", `{"email":"a@b.com","subject":"Hi","message":"Hello"}`},
		{"missing email", `{"name":"Ola","subject":"Hi","message":"Hello"}`},
		{"missing subject", `{"name":"Ola","email":"a@b.com","message":"Hello"}`},
		{"missing message", `{"name":"Ola","email":"a@b.com","subject":"Hi"}`},
		{"blank subject after cleaning", `{"name":"Ola","email":"a@b.com","subject":"\r\n","message":"Hello there, friends"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postContact(h, tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}
			if errorCode(t, rec) == "" {
				t.Error("expected error message in response")
			}
		})
	}
}

func TestHandleContactFormEmptyBody(t *testing.T) {
	if rec := postContact(newTestContactHandler(t), ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestHandleContactFormInvalidJSON(t *testing.T) {
	if rec := postContact(newTestContactHandler(t), "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestHandleContactFormInvalidEmail(t *testing.T) {
	h := newTestContactHandler(t)

	tests := []struct {
		name  string
		email string
	}{
		{"no at sign", "invalid"},
		{"no domain", "user@"},
		{"no tld", "user@domain"},
		{"no local part", "@domain.com"},
		{"display name", "Ola <ola@example.com>"},
		{"two addresses", "a@example.com,b@example.com"},
		{"inner space", "ola @example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"name":"Test","email":"` + tt.email + `","subject":"Hi","message":"This is a valid message."}`
			rec := postContact(h, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(errorCode(t, rec), "email") {
				t.Errorf("expected error about email")
			}
		})
	}
}

func TestHandleContactFormMessageTooShort(t *testing.T) {
	body := `{"name":"Test","email":"user@example.com","subject":"Hi","message":"short"}`
	rec := postContact(newTestContactHandler(t), body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(errorCode(t, rec), "10 characters") {
		t.Errorf("expected error about 10 characters")
	}
}

func TestHandleContactFormFieldLengthCaps(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	tests := []struct {
		name, body string
		wantOK     bool
	}{
		{"name at cap", `{"name":"` + long(contactMaxName) + `","email":"a@b.com","subject":"Hi","message":"Hello there, friends"}`, true},
		{"name over cap", `{"name":"` + long(contactMaxName+1) + `","email":"a@b.com","subject":"Hi","message":"Hello there, friends"}`, false},
		{"email over cap", `{"name":"Ola","email":"` + long(contactMaxEmail) + `@b.com","subject":"Hi","message":"Hello there, friends"}`, false},
		{"subject over cap", `{"name":"Ola","email":"a@b.com","subject":"` + long(contactMaxSubject+1) + `","message":"Hello there, friends"}`, false},
		{"message at cap", `{"name":"Ola","email":"a@b.com","subject":"Hi","message":"` + long(contactMaxMessage) + `"}`, true},
		{"message over cap", `{"name":"Ola","email":"a@b.com","subject":"Hi","message":"` + long(contactMaxMessage+1) + `"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postContact(newTestContactHandler(t), tt.body)
			want := http.StatusBadRequest
			if tt.wantOK {
				want = http.StatusOK
			}
			if rec.Code != want {
				t.Fatalf("expected %d, got %d; body: %s", want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleContactFormBodyTooLarge(t *testing.T) {
	f := newContactFixture(t, fakeClubRow{secretary: testSecretary})
	body := `{"name":"Ola","email":"a@b.com","subject":"Hi","message":"` + strings.Repeat("a", contactMaxBody) + `"}`

	rec := postContact(f.h, body)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
	if len(f.mailer.Calls) != 0 {
		t.Errorf("nothing should be sent")
	}
}

func TestIsValidEmail(t *testing.T) {
	tests := []struct {
		email string
		valid bool
	}{
		{"user@example.com", true},
		{"a@b.no", true},
		{"name+tag@domain.co.uk", true},
		{"invalid", false},
		{"@domain.com", false},
		{"user@", false},
		{"user@domain", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			got := isValidEmail(tt.email)
			if got != tt.valid {
				t.Errorf("isValidEmail(%q) = %v, want %v", tt.email, got, tt.valid)
			}
		})
	}
}
