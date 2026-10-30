package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

// gdprRouter composes the gates the way cmd/api does: session auth, the
// /admin group's RequireAdminTOTP, then the GDPR mount behind the admin-only
// role gate, plus the member's own GDPR routes under /members/me.
func gdprRouter(h *GDPRHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/admin", func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Use(testSessionState)
		r.Use(middleware.RequireAdminTOTP(nil))
		r.Use(panicAsTeapot)
		h.MountAdminRoutes(r, middleware.RequireRole("admin"))
	})

	r.Route("/members", func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Use(panicAsTeapot)
		r.Get("/me/data-export", h.HandleDataExport)
		r.Post("/me/delete-request", h.HandleRequestDeletion)
		r.Delete("/me/delete-request", h.HandleCancelDeletion)
		r.Get("/me/delete-request", h.HandleGetDeletionStatus)
		r.Post("/me/consent", h.HandleRecordConsent)
		r.Get("/me/consents", h.HandleGetMyConsents)
	})
	return r
}

// handlerReached reports whether a request got past the routing and gate
// layers: the handler answered 400 on the empty body, or panicked on the
// test's nil db (418).
func handlerReached(code int) bool {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed:
		return false
	}
	return true
}

var gdprAdminCalls = []routeCall{
	{http.MethodGet, "/admin/gdpr/deletion-requests", false},
	{http.MethodPost, "/admin/gdpr/deletion-requests/req-1/process", false},
	{http.MethodGet, "/admin/gdpr/legal", false},
	{http.MethodPost, "/admin/gdpr/legal", false},
}

// GDPR is one function held by the site admin, and the SPA page is
// admin-only, so a board member must be refused by the API even with a valid
// step-up (BRY-481).
func TestGDPRAdminRouteGates(t *testing.T) {
	h := newTestGDPRHandler(t)
	r := gdprRouter(h)

	// A new admin GDPR route must be added to gdprAdminCalls, choosing its
	// gate there.
	walked := 0
	err := chi.Walk(r, func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/admin/gdpr") {
			walked++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if walked != len(gdprAdminCalls) {
		t.Fatalf("mounted %d admin GDPR routes, test covers %d", walked, len(gdprAdminCalls))
	}

	for _, c := range gdprAdminCalls {
		name := c.method + " " + c.path

		if rec := serveWaitingList(r, c, nil, sessFresh); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without auth: status %d, want 401", name, rec.Code)
		}

		// The admin step-up still comes first.
		for _, s := range []string{sessNone, sessLapsed} {
			rec := serveWaitingList(r, c, []string{"admin"}, s)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"totp_required"`) {
				t.Errorf("%s as admin, session %s: %d %s, want 403 totp_required", name, s, rec.Code, rec.Body.String())
			}
		}

		// Every role except admin is refused, board included, even stepped up.
		for _, role := range []string{"board", "treasurer", "harbor_master", "member"} {
			rec := serveWaitingList(r, c, []string{role}, sessFresh)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient permissions") {
				t.Errorf("%s as %s: %d %s, want 403 insufficient permissions", name, role, rec.Code, rec.Body.String())
			}
		}

		for _, s := range []string{sessStepUp, sessFresh} {
			if rec := serveWaitingList(r, c, []string{"admin"}, s); !handlerReached(rec.Code) {
				t.Errorf("%s as admin, session %s: %d, want handler reached", name, s, rec.Code)
			}
		}
	}
}

// Narrowing the admin routes must leave a member's own export, deletion
// request and consent routes open to a plain member with no step-up.
func TestGDPRMemberRoutesUnchanged(t *testing.T) {
	r := gdprRouter(newTestGDPRHandler(t))

	for _, c := range []routeCall{
		{http.MethodGet, "/members/me/data-export", false},
		{http.MethodPost, "/members/me/delete-request", false},
		{http.MethodDelete, "/members/me/delete-request", false},
		{http.MethodGet, "/members/me/delete-request", false},
		{http.MethodPost, "/members/me/consent", false},
		{http.MethodGet, "/members/me/consents", false},
	} {
		if rec := serveWaitingList(r, c, nil, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without auth: %d, want 401", c.method, c.path, rec.Code)
		}
		if rec := serveWaitingList(r, c, []string{"member"}, ""); !handlerReached(rec.Code) {
			t.Errorf("%s %s as member: %d, want the member handler reached", c.method, c.path, rec.Code)
		}
	}
}
