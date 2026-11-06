package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func newTestGDPRHandler(t *testing.T) *GDPRHandler {
	t.Helper()
	cfg := testConfig()
	log := zerolog.Nop()
	return NewGDPRHandler(nil, cfg, log)
}

func TestHandleDataExportUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodGet, "/members/me/data-export", h.HandleDataExport)

	req := httptest.NewRequest(http.MethodGet, "/members/me/data-export", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleRequestDeletionUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, "/members/me/delete-request", h.HandleRequestDeletion)

	req := httptest.NewRequest(http.MethodPost, "/members/me/delete-request", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleCancelDeletionUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodDelete, "/members/me/delete-request", h.HandleCancelDeletion)

	req := httptest.NewRequest(http.MethodDelete, "/members/me/delete-request", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleRecordConsentUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, "/members/me/consent", h.HandleRecordConsent)

	body := `{"consent_type":"privacy","version":"1.0"}`
	req := httptest.NewRequest(http.MethodPost, "/members/me/consent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleRecordConsentInvalidBody(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, "/members/me/consent", h.HandleRecordConsent)

	token := generateTestToken("user-123", "club-456", []string{"member"})

	req := httptest.NewRequest(http.MethodPost, "/members/me/consent", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleRecordConsentMissingFields(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, "/members/me/consent", h.HandleRecordConsent)

	token := generateTestToken("user-123", "club-456", []string{"member"})

	tests := []struct {
		name string
		body string
	}{
		{"missing consent_type", `{"version":"1.0"}`},
		{"missing version", `{"consent_type":"privacy"}`},
		{"both empty", `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/members/me/consent", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", authHeader(token))
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}

			var resp errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Error != "consent_type and version are required" {
				t.Errorf("expected error 'consent_type and version are required', got %q", resp.Error)
			}
		})
	}
}

// A type outside the vocabulary is answered before the database is read, so
// the nil db here is never touched.
func TestHandleGetLegalDocumentUnknownTypeIs404(t *testing.T) {
	h := newTestGDPRHandler(t)

	r := setupAuthenticatedRouter(http.MethodGet, "/legal/{docType}", h.HandleGetLegalDocument)

	req := httptest.NewRequest(http.MethodGet, "/legal/privacy_policy", nil)
	req.Header.Set("Authorization", authHeader(generateTestToken("user-123", "club-456", []string{"member"})))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

func TestHandleRecordConsentUnknownType(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, "/members/me/consent", h.HandleRecordConsent)

	token := generateTestToken("user-123", "club-456", []string{"member"})
	req := httptest.NewRequest(http.MethodPost, "/members/me/consent",
		strings.NewReader(`{"consent_type":"marketing","version":"1.0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleAdminCreateLegalDocumentUnknownType(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/legal", h.HandleAdminCreateLegalDocument, "admin")

	token := generateTestToken("user-123", "club-456", []string{"admin"})
	// newsletter is a consent type but has no document behind it.
	req := httptest.NewRequest(http.MethodPost, "/admin/gdpr/legal",
		strings.NewReader(`{"doc_type":"newsletter","version":"1.0","content":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleGetMyConsentsUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodGet, "/members/me/consents", h.HandleGetMyConsents)

	req := httptest.NewRequest(http.MethodGet, "/members/me/consents", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleGetDeletionStatusUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupAuthenticatedRouter(http.MethodGet, "/members/me/delete-request", h.HandleGetDeletionStatus)

	req := httptest.NewRequest(http.MethodGet, "/members/me/delete-request", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleListDeletionRequestsUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/gdpr/deletion-requests", h.HandleListDeletionRequests, "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/gdpr/deletion-requests", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleListDeletionRequestsForbidden(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/gdpr/deletion-requests", h.HandleListDeletionRequests, "admin")

	token := generateTestToken("user-123", "club-456", []string{"member"})

	req := httptest.NewRequest(http.MethodGet, "/admin/gdpr/deletion-requests", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleProcessDeletionUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/deletion-requests/{requestID}/process", h.HandleProcessDeletion, "admin")

	req := httptest.NewRequest(http.MethodPost, "/admin/gdpr/deletion-requests/some-id/process", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleProcessDeletionForbidden(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/deletion-requests/{requestID}/process", h.HandleProcessDeletion, "admin")

	token := generateTestToken("user-123", "club-456", []string{"member"})

	req := httptest.NewRequest(http.MethodPost, "/admin/gdpr/deletion-requests/some-id/process", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleAdminCreateLegalDocumentUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/legal", h.HandleAdminCreateLegalDocument, "admin")

	body := `{"doc_type":"privacy","version":"1.0","content":"test","publish":true}`
	req := httptest.NewRequest(http.MethodPost, "/admin/gdpr/legal", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleAdminCreateLegalDocumentInvalidBody(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/legal", h.HandleAdminCreateLegalDocument, "admin")

	token := generateTestToken("user-123", "club-456", []string{"admin"})

	req := httptest.NewRequest(http.MethodPost, "/admin/gdpr/legal", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleAdminCreateLegalDocumentMissingFields(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/legal", h.HandleAdminCreateLegalDocument, "admin")

	token := generateTestToken("user-123", "club-456", []string{"admin"})

	tests := []struct {
		name string
		body string
	}{
		{"missing doc_type", `{"version":"1.0","content":"test"}`},
		{"missing version", `{"doc_type":"privacy","content":"test"}`},
		{"missing content", `{"doc_type":"privacy","version":"1.0"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/gdpr/legal", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", authHeader(token))
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}

			var resp errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Error != "doc_type, version, and content are required" {
				t.Errorf("expected error 'doc_type, version, and content are required', got %q", resp.Error)
			}
		})
	}
}

func TestHandleAdminListLegalDocumentsUnauthenticated(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/gdpr/legal", h.HandleAdminListLegalDocuments, "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/gdpr/legal", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}
