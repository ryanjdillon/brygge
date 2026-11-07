package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/handlers"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// inertRouter builds the API router with every feature on and zero-value
// handlers. Requests that get past the middleware panic inside the handler
// and come back 500 from the recoverer: these tests only look at what the
// middleware decided. Sessions are real (pool); the rate limiters point at a
// closed port and fail open.
func inertRouter(t *testing.T, sessions *auth.SessionService, wrapOptional func(func(http.Handler) http.Handler) func(http.Handler) http.Handler) *chi.Mux {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond, DialerRetries: 1, DialerRetryTimeout: time.Millisecond,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	return inertRouterWithRedis(t, sessions, wrapOptional, rdb)
}

// inertRouterWithRedis is inertRouter with the rate limiters on rdb, for the
// tests that need a limiter to count.
func inertRouterWithRedis(t *testing.T, sessions *auth.SessionService, wrapOptional func(func(http.Handler) http.Handler) func(http.Handler) http.Handler, rdb *redis.Client) *chi.Mux {
	t.Helper()
	cfg := &config.Config{
		FrontendURL:           "https://club.example.test",
		DevQueryTokensEnabled: true,
		Features: config.Features{
			Bookings: true, Projects: true, Calendar: true, Commerce: true, Accounting: true, DemoAuth: true,
		},
	}
	return newRouter(routeDeps{
		wrapOptionalSession:     wrapOptional,
		cfg:                     cfg,
		log:                     zerolog.Nop(),
		rdb:                     rdb,
		sessionService:          sessions,
		accountingHandler:       &handlers.AccountingHandler{},
		adminDocumentsHandler:   &handlers.AdminDocumentsHandler{},
		adminSlipsHandler:       &handlers.AdminSlipsHandler{},
		adminUsersHandler:       &handlers.AdminUsersHandler{},
		aiDocumentsHandler:      &handlers.AIDocumentsHandler{},
		auditHandler:            &handlers.AuditHandler{},
		authHandler:             &handlers.AuthHandler{},
		bankAccountsHandler:     &handlers.BankAccountsHandler{},
		bankRowsHandler:         &handlers.BankRowsHandler{},
		boatModelsHandler:       &handlers.BoatModelsHandler{},
		bookingsHandler:         &handlers.BookingsHandler{},
		broadcastsHandler:       &handlers.BroadcastsHandler{},
		calendarHandler:         &handlers.CalendarHandler{},
		clubSettingsHandler:     &handlers.ClubSettingsHandler{},
		contactHandler:          &handlers.ContactHandler{},
		contentDocumentsHandler: &handlers.ContentDocumentsHandler{},
		demoAuthHandler:         &handlers.DemoAuthHandler{},
		devQueryHandler:         &handlers.DevQueryHandler{},
		devTokenHandler:         &handlers.DevTokenHandler{},
		emailPrefsHandler:       &handlers.EmailPrefsHandler{},
		faqsHandler:             &handlers.FaqsHandler{},
		featureRequestsHandler:  &handlers.FeatureRequestsHandler{},
		featuresHandler:         &handlers.FeaturesHandler{},
		feedbackHandler:         &handlers.FeedbackHandler{},
		financialsHandler:       &handlers.FinancialsHandler{},
		gdprHandler:             &handlers.GDPRHandler{},
		harborHandler:           &handlers.HarborHandler{},
		healthHandler:           &handlers.HealthHandler{},
		invoiceHandler:          &handlers.InvoiceHandler{},
		joinHandler:             &handlers.JoinHandler{},
		magicLinkHandler:        &handlers.MagicLinkHandler{},
		mapHandler:              &handlers.MapHandler{},
		membersHandler:          &handlers.MembersHandler{},
		notificationsHandler:    &handlers.NotificationsHandler{},
		orderRefundHandler:      &handlers.OrderRefundHandler{},
		passkeyHandler:          &handlers.PasskeyHandler{},
		paymentFilterHandler:    &handlers.PaymentFilterHandler{},
		priceItemsHandler:       &handlers.PriceItemsHandler{},
		productsHandler:         &handlers.ProductsHandler{},
		projectsHandler:         &handlers.ProjectsHandler{},
		shoppingListsHandler:    &handlers.ShoppingListsHandler{},
		slipSharesHandler:       &handlers.SlipSharesHandler{},
		stripeWebhookHandler:    &handlers.StripeWebhookHandler{},
		totpHandler:             &handlers.TOTPHandler{},
		vippsWebhookHandler:     &handlers.VippsWebhookHandler{},
		volunteerHandler:        &handlers.VolunteerHandler{},
		waitingListHandler:      &handlers.WaitingListHandler{},
		weatherHandler:          &handlers.WeatherHandler{},
	})
}

var routeParam = regexp.MustCompile(`\{[^}]+\}`)

// concretePath turns a chi pattern into a path that matches it.
func concretePath(pattern string) string {
	p := routeParam.ReplaceAllString(pattern, "11111111-1111-1111-1111-111111111111")
	if strings.HasSuffix(p, "/*") {
		p = strings.TrimSuffix(p, "*") + "x"
	}
	return p
}

func serveAs(r http.Handler, method, path, sessionID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if sessionID != "" {
		req.AddCookie(&http.Cookie{Name: "brygge_session", Value: sessionID})
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func isApplicantRefusal(rec *httptest.ResponseRecorder) bool {
	if rec.Code != http.StatusForbidden {
		return false
	}
	var body map[string]any
	return json.Unmarshal(rec.Body.Bytes(), &body) == nil && body["error"] == middleware.ErrMembershipNotCompleted
}

// Every route of the real router is either on the applicant allowlist and
// reachable by an applicant-only session, or refused to it, unless it needs
// no session at all. A new session route is therefore closed to applicants
// until it is listed in middleware.ApplicantRoutes, and a stale allowlist
// entry fails here too.
func TestApplicantRouteTable(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	applicant, _ := testutil.SeedUser(t, pool, clubID, []string{"applicant"})
	member, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})

	sessions := auth.NewSessionService(pool)
	applicantSID, err := sessions.CreateSession(ctx, applicant, clubID, "198.51.100.4:5555", "test-agent")
	if err != nil {
		t.Fatalf("create applicant session: %v", err)
	}
	memberSID, err := sessions.CreateSession(ctx, member, clubID, "198.51.100.5:5555", "test-agent")
	if err != nil {
		t.Fatalf("create member session: %v", err)
	}

	// Optional-session routes answer with or without a session, so the walk
	// below cannot tell from the status whether a session was honoured. The
	// probe sits just inside the middleware and records, per request, whether
	// the handler would be given claims.
	type probeKey struct{ method, path string }
	optionalClaims := map[probeKey]bool{}
	probe := func(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				// Sticky: the walk also sends each route without a session,
				// which must not overwrite a leak seen first.
				k := probeKey{req.Method, req.URL.Path}
				optionalClaims[k] = optionalClaims[k] || middleware.GetClaims(req.Context()) != nil
				next.ServeHTTP(w, req)
			}))
		}
	}
	r := inertRouter(t, sessions, probe)

	type route struct{ method, pattern string }
	var routes []route
	if err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(pattern, "/api/v1/") {
			routes = append(routes, route{method, pattern})
		}
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(routes) < 100 {
		t.Fatalf("walked %d API routes; the router was not built in full", len(routes))
	}

	walked := map[route]bool{}
	allowed, refused := 0, 0
	for _, rt := range routes {
		walked[rt] = true
		path := concretePath(rt.pattern)
		rec := serveAs(r, rt.method, path, applicantSID)
		if middleware.ApplicantMayReach(rt.method, path) {
			allowed++
			if isApplicantRefusal(rec) || rec.Code == http.StatusUnauthorized {
				t.Errorf("%s %s is on the applicant allowlist but answered %d %s", rt.method, rt.pattern, rec.Code, rec.Body.String())
			}
			continue
		}
		if isApplicantRefusal(rec) {
			refused++
			continue
		}
		// Not refused: only acceptable when the session opened nothing,
		// i.e. the route answers the same way without one, or not at all
		// to the applicant's session (a bearer-token route).
		if rec.Code == http.StatusUnauthorized {
			continue
		}
		if anon := serveAs(r, rt.method, path, ""); anon.Code == http.StatusUnauthorized {
			t.Errorf("%s %s needs a session but let an applicant-only session through (%d); refuse it or list it in middleware.ApplicantRoutes",
				rt.method, rt.pattern, rec.Code)
		}
	}
	// Every optional-session route the walk reached was handed no claims for
	// the applicant-only session: it is served as an anonymous visitor.
	if len(optionalClaims) == 0 {
		t.Fatal("no request passed the optional-session middleware; the probe is not wired")
	}
	for k, sawClaims := range optionalClaims {
		if sawClaims {
			t.Errorf("%s %s: an optional-session route was given the applicant's claims", k.method, k.path)
		}
	}
	for _, k := range []probeKey{
		{http.MethodGet, "/api/v1/harbor/layout"},
		{http.MethodPost, "/api/v1/bookings/"},
	} {
		if _, ok := optionalClaims[k]; !ok {
			t.Errorf("%s %s did not pass the optional-session middleware", k.method, k.path)
		}
	}
	// A member keeps their claims there.
	clear(optionalClaims)
	serveAs(r, http.MethodGet, "/api/v1/harbor/layout", memberSID)
	if !optionalClaims[probeKey{http.MethodGet, "/api/v1/harbor/layout"}] {
		t.Error("GET /api/v1/harbor/layout: a member session was not given its claims")
	}

	if allowed == 0 || refused == 0 {
		t.Fatalf("allowed %d, refused %d routes; the walk did not exercise the gate", allowed, refused)
	}

	// No stale entries: every listed route exists on the router.
	for _, a := range middleware.ApplicantRoutes {
		if a.Method == "*" {
			prefix := strings.TrimSuffix(a.Pattern, "*")
			found := false
			for rt := range walked {
				if strings.HasPrefix(rt.pattern, prefix) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("allowlist entry %s %s matches no route", a.Method, a.Pattern)
			}
			continue
		}
		if !walked[route{a.Method, a.Pattern}] {
			t.Errorf("allowlist entry %s %s is not a route", a.Method, a.Pattern)
		}
	}

	// The routes that exposed member data to any session are closed to an
	// applicant, and still open to a member.
	for _, c := range []route{
		{http.MethodGet, "/api/v1/members/directory"},
		{http.MethodGet, "/api/v1/documents"},
		{http.MethodPost, "/api/v1/waiting-list/join"},
		{http.MethodGet, "/api/v1/portal/slip-shares"},
	} {
		if rec := serveAs(r, c.method, c.pattern, applicantSID); !isApplicantRefusal(rec) {
			t.Errorf("%s %s as applicant: %d %s, want 403 %s", c.method, c.pattern, rec.Code, rec.Body.String(), middleware.ErrMembershipNotCompleted)
		}
		if rec := serveAs(r, c.method, c.pattern, memberSID); isApplicantRefusal(rec) || rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s as member: %d, want the handler reached", c.method, c.pattern, rec.Code)
		}
	}
}
