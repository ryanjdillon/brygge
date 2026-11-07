package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/roles"
)

// ErrMembershipNotCompleted is the error AuthenticateSession answers with
// (403) when a session holding only the applicant role asks for anything
// outside ApplicantRoutes. The SPA's API client matches on it to send the
// applicant to the page that finishes joining.
const ErrMembershipNotCompleted = "membership_not_completed"

// ApplicantRoute is one method and chi pattern an applicant-only session may
// reach. Method "*" matches every method.
type ApplicantRoute struct {
	Method  string
	Pattern string
}

// ApplicantRoutes is everything behind AuthenticateSession that a session
// holding only the applicant role may reach: the session itself, the
// applicant's own profile, email preferences and fakturas, consents, the
// GDPR export and deletion request, "Betal med bank", which bills the
// pending join as a KID faktura, and the join's own state (/join/me).
// Paying needs no entry: the pay and checkout pages are public routes
// addressed by token, outside AuthenticateSession. Every other session route
// answers 403 ErrMembershipNotCompleted, so a route added later stays closed
// to applicants until it is listed here; the route-table test in cmd/api
// walks the real router to hold that.
var ApplicantRoutes = []ApplicantRoute{
	{"*", "/api/v1/auth/*"},
	{http.MethodGet, "/api/v1/members/me"},
	{http.MethodPut, "/api/v1/members/me"},
	{http.MethodGet, "/api/v1/members/me/invoices"},
	{http.MethodGet, "/api/v1/members/me/invoices/updated-at"},
	{http.MethodGet, "/api/v1/members/me/invoices/{invoiceID}/pdf"},
	{http.MethodPost, "/api/v1/members/me/consent"},
	{http.MethodGet, "/api/v1/members/me/consents"},
	{http.MethodGet, "/api/v1/members/me/email-preferences"},
	{http.MethodPut, "/api/v1/members/me/email-preferences"},
	{http.MethodGet, "/api/v1/members/me/data-export"},
	{http.MethodGet, "/api/v1/members/me/delete-request"},
	{http.MethodPost, "/api/v1/members/me/delete-request"},
	{http.MethodDelete, "/api/v1/members/me/delete-request"},
	{http.MethodPost, "/api/v1/join/bank-invoice"},
	{http.MethodGet, "/api/v1/join/me"},
}

// applicantMux matches a request against ApplicantRoutes with chi's own
// router, so a pattern means exactly what it means on the real routes.
var applicantMux = func() *chi.Mux {
	mx := chi.NewRouter()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, rt := range ApplicantRoutes {
		if rt.Method == "*" {
			mx.Handle(rt.Pattern, noop)
			continue
		}
		mx.Method(rt.Method, rt.Pattern, noop)
	}
	return mx
}()

// ApplicantMayReach reports whether an applicant-only session may call
// method on path (the full request path, /api/v1 included).
func ApplicantMayReach(method, path string) bool {
	return applicantMux.Match(chi.NewRouteContext(), method, path)
}

// routingPath is the path chi routes on: the escaped form when the request
// has one, so an encoded "/" (%2F) splits segments for the gate exactly as it
// does for the router, and the two can never match different routes.
func routingPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	return r.URL.Path
}

// refuseApplicant writes the 403 and reports true when claims hold only the
// applicant role and the request is not on the allowlist.
func refuseApplicant(w http.ResponseWriter, r *http.Request, claims *auth.Claims) bool {
	if claims == nil || !roles.IsApplicantOnly(claims.Roles) || ApplicantMayReach(r.Method, routingPath(r)) {
		return false
	}
	writeError(w, http.StatusForbidden, ErrMembershipNotCompleted)
	return true
}
