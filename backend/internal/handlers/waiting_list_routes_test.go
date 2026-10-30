package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// Session states the route walk drives, named by what the step-up gates see.
const (
	sessNone    = "none"   // no second factor verified yet
	sessLapsed  = "lapsed" // verified, but outside the admin step-up window
	sessStepUp  = "stepup" // inside the admin window, outside the fresh window
	sessFresh   = "fresh"  // verified a moment ago
	headerState = "X-Test-Session"
)

// panicAsTeapot answers 418 when the handler behind it panics on the test's
// nil db: reaching the handler means every gate in front of it let the
// request through.
func panicAsTeapot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if recover() != nil {
				w.WriteHeader(http.StatusTeapot)
			}
		}()
		next.ServeHTTP(w, req)
	})
}

// testSessionState plants the session info a route walk asks for through the
// X-Test-Session header, in place of the session middleware.
func testSessionState(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		info := &auth.SessionInfo{TOTPEnabled: true}
		var verified time.Duration
		switch req.Header.Get(headerState) {
		case sessLapsed:
			verified = 13 * time.Hour
		case sessStepUp:
			verified = time.Hour
		case sessFresh:
			verified = time.Minute
		}
		if verified > 0 {
			at := time.Now().Add(-verified)
			info.TOTPVerifiedAt = &at
		}
		next.ServeHTTP(w, req.WithContext(middleware.WithSessionInfo(req.Context(), info)))
	})
}

// waitingListAdminRouter composes the gates the way cmd/api does: session
// auth, the /admin group's RequireAdminTOTP, then the waiting-list mount with
// its role and fresh-TOTP gates.
func waitingListAdminRouter(h *WaitingListHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/admin", func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Use(testSessionState)
		r.Use(middleware.RequireAdminTOTP(nil))
		r.Use(panicAsTeapot)
		h.MountAdminRoutes(r,
			middleware.RequireRoleOrBoardRead("board"),
			middleware.RequireFreshTOTPDefault(),
		)
	})

	// The member routes as cmd/api mounts them, to prove the admin mount does
	// not shadow or duplicate them.
	r.Route("/waiting-list", func(r chi.Router) {
		r.Use(testAuthMiddleware)
		r.Use(panicAsTeapot)
		r.Post("/join", h.HandleJoinWaitingList)
		r.Get("/me", h.HandleGetMyPosition)
		r.Post("/{entryID}/accept", h.HandleAcceptOffer)
		r.Post("/{entryID}/decline", h.HandleDeclineOffer)
	})
	return r
}

type routeCall struct {
	method, path string
	fresh        bool // behind the fresh-TOTP gate as well as the step-up
}

var waitingListAdminCalls = []routeCall{
	{http.MethodGet, "/admin/waiting-list/", false},
	{http.MethodPost, "/admin/waiting-list/enroll", true},
	{http.MethodPost, "/admin/waiting-list/entry-1/offer", true},
	{http.MethodPut, "/admin/waiting-list/entry-1/position", true},
}

func serveWaitingList(r http.Handler, c routeCall, roles []string, session string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	if roles != nil {
		req.Header.Set("Authorization", authHeader(generateTestToken("user-1", "club-1", roles)))
	}
	if session != "" {
		req.Header.Set(headerState, session)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// The queue carries every member's name, email and phone and decides who is
// offered a slip, so the list needs the admin step-up and offer, enroll and
// reorder a fresh TOTP on top (BRY-477).
func TestWaitingListAdminRouteGates(t *testing.T) {
	r := waitingListAdminRouter(newTestWaitingListHandler(t))

	// Every mounted admin route is covered by waitingListAdminCalls, so a new
	// route cannot be added without choosing its gate here.
	walked := 0
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/admin/waiting-list") {
			walked++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if walked != len(waitingListAdminCalls) {
		t.Fatalf("mounted %d admin waiting-list routes, test covers %d", walked, len(waitingListAdminCalls))
	}

	reached := func(code int) bool { return code != http.StatusUnauthorized && code != http.StatusForbidden }
	body := func(rec *httptest.ResponseRecorder) string { return rec.Body.String() }

	for _, c := range waitingListAdminCalls {
		name := c.method + " " + c.path

		if rec := serveWaitingList(r, c, nil, sessFresh); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without auth: status %d, want 401", name, rec.Code)
		}

		// The admin step-up comes first: a board member with no verified
		// factor, or one outside the 12-hour window, is told to step up.
		for _, s := range []string{sessNone, sessLapsed} {
			rec := serveWaitingList(r, c, []string{"board"}, s)
			if rec.Code != http.StatusForbidden || !strings.Contains(body(rec), `"totp_required"`) {
				t.Errorf("%s as board, session %s: %d %s, want 403 totp_required", name, s, rec.Code, body(rec))
			}
		}

		// A stepped-up member is still kept out by the role gate.
		rec := serveWaitingList(r, c, []string{"member"}, sessFresh)
		if rec.Code != http.StatusForbidden || !strings.Contains(body(rec), "insufficient permissions") {
			t.Errorf("%s as member: %d %s, want 403 insufficient permissions", name, rec.Code, body(rec))
		}

		// Inside the admin window but past the fresh window: reads pass,
		// queue changes ask for a fresh TOTP.
		rec = serveWaitingList(r, c, []string{"board"}, sessStepUp)
		if c.fresh {
			if rec.Code != http.StatusForbidden || !strings.Contains(body(rec), `"totp_fresh_required"`) {
				t.Errorf("%s as board, stale fresh TOTP: %d %s, want 403 totp_fresh_required", name, rec.Code, body(rec))
			}
		} else if !reached(rec.Code) {
			t.Errorf("%s as board with step-up: %d %s, want handler reached", name, rec.Code, body(rec))
		}

		if rec := serveWaitingList(r, c, []string{"board"}, sessFresh); !reached(rec.Code) {
			t.Errorf("%s as board, fresh TOTP: %d %s, want handler reached", name, rec.Code, body(rec))
		}
	}

	// Any admin viewer may read the list inside the step-up window; the
	// write role stays "board", as before the move.
	list := waitingListAdminCalls[0]
	for _, role := range []string{"treasurer", "admin"} {
		if rec := serveWaitingList(r, list, []string{role}, sessStepUp); !reached(rec.Code) {
			t.Errorf("GET list as %s with step-up: %d, want handler reached", role, rec.Code)
		}
		if rec := serveWaitingList(r, list, []string{role}, sessLapsed); rec.Code != http.StatusForbidden {
			t.Errorf("GET list as %s with lapsed step-up: %d, want 403", role, rec.Code)
		}
	}
	offer := waitingListAdminCalls[2]
	if rec := serveWaitingList(r, offer, []string{"treasurer"}, sessFresh); rec.Code != http.StatusForbidden {
		t.Errorf("POST offer as treasurer: %d, want 403 (write role unchanged)", rec.Code)
	}
}

// Moving the admin routes must leave the member routes where they were, and
// the old ungated admin paths must be gone rather than still answering.
func TestWaitingListMemberRoutesUnchangedAfterAdminMove(t *testing.T) {
	r := waitingListAdminRouter(newTestWaitingListHandler(t))

	for _, c := range []routeCall{
		{http.MethodPost, "/waiting-list/join", false},
		{http.MethodGet, "/waiting-list/me", false},
		{http.MethodPost, "/waiting-list/entry-1/accept", false},
		{http.MethodPost, "/waiting-list/entry-1/decline", false},
	} {
		if rec := serveWaitingList(r, c, nil, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without auth: %d, want 401", c.method, c.path, rec.Code)
		}
		// A plain member, with no step-up at all, gets past the member gate.
		rec := serveWaitingList(r, c, []string{"member"}, "")
		switch rec.Code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed:
			t.Errorf("%s %s as member: %d, want the member handler reached", c.method, c.path, rec.Code)
		}
	}

	for _, c := range []routeCall{
		{http.MethodGet, "/waiting-list/", false},
		{http.MethodPost, "/waiting-list/enroll", false},
		{http.MethodPost, "/waiting-list/entry-1/offer", false},
		{http.MethodPut, "/waiting-list/entry-1/position", false},
	} {
		rec := serveWaitingList(r, c, []string{"board"}, sessFresh)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d, want 404/405 (admin routes live under /admin)", c.method, c.path, rec.Code)
		}
	}
}
