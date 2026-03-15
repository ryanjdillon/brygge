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

	body := `{"consent_type":"privacy_policy","version":"1.0"}`
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
		{"missing version", `{"consent_type":"privacy_policy"}`},
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

func TestHandleGetLegalDocumentNotFound(t *testing.T) {
	h := newTestGDPRHandler(t)

	r := setupAuthenticatedRouter(http.MethodGet, "/legal/{docType}", h.HandleGetLegalDocument)

	req := httptest.NewRequest(http.MethodGet, "/legal/privacy_policy", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	// Handler will fail because db is nil, which results in a panic recovered by chi
	// or an internal error. Since db is nil, we expect a non-200 status.
	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-200 status for nil db, got %d", rec.Code)
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
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/gdpr/deletion-requests", h.HandleListDeletionRequests, "board", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/gdpr/deletion-requests", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleListDeletionRequestsForbidden(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/gdpr/deletion-requests", h.HandleListDeletionRequests, "board", "admin")

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
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/deletion-requests/{requestID}/process", h.HandleProcessDeletion, "board", "admin")

	req := httptest.NewRequest(http.MethodPost, "/admin/gdpr/deletion-requests/some-id/process", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleProcessDeletionForbidden(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/deletion-requests/{requestID}/process", h.HandleProcessDeletion, "board", "admin")

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
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/legal", h.HandleAdminCreateLegalDocument, "board", "admin")

	body := `{"doc_type":"privacy_policy","version":"1.0","content":"test","publish":true}`
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
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/legal", h.HandleAdminCreateLegalDocument, "board", "admin")

	token := generateTestToken("user-123", "club-456", []string{"board"})

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
	r := setupRoleProtectedRouter(http.MethodPost, "/admin/gdpr/legal", h.HandleAdminCreateLegalDocument, "board", "admin")

	token := generateTestToken("user-123", "club-456", []string{"board"})

	tests := []struct {
		name string
		body string
	}{
		{"missing doc_type", `{"version":"1.0","content":"test"}`},
		{"missing version", `{"doc_type":"privacy_policy","content":"test"}`},
		{"missing content", `{"doc_type":"privacy_policy","version":"1.0"}`},
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
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/gdpr/legal", h.HandleAdminListLegalDocuments, "board", "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/gdpr/legal", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}
