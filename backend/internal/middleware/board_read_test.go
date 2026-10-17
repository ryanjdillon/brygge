package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/auth"
)

// boardReadRouter composes the gates the way cmd/api does for the admin
// area: a group admitting board reads, writes on the group's role set, a
// money route on treasurer/admin, and an admin-only developer route.
func boardReadRouter() http.Handler {
	r := chi.NewRouter()
	r.Route("/accounting", func(r chi.Router) {
		r.Use(RequireRoleOrBoardRead("treasurer", "board", "admin"))
		r.Get("/journal", noopHandler().ServeHTTP)
		r.Post("/rules", noopHandler().ServeHTTP)
		r.With(RequireRole("treasurer", "admin")).Post("/journal/{id}/post", noopHandler().ServeHTTP)
	})
	r.Route("/pricing", func(r chi.Router) {
		r.Use(RequireRoleOrBoardRead("admin", "treasurer"))
		r.Get("/", noopHandler().ServeHTTP)
		r.Post("/", noopHandler().ServeHTTP)
	})
	r.With(RequireRole("admin")).Get("/dev/tokens", noopHandler().ServeHTTP)
	return r
}

func TestRequireRoleOrBoardReadRouteWalk(t *testing.T) {
	type call struct {
		method, path string
	}
	journal := call{http.MethodGet, "/accounting/journal"}
	rules := call{http.MethodPost, "/accounting/rules"}
	post := call{http.MethodPost, "/accounting/journal/x/post"}
	pricing := call{http.MethodGet, "/pricing/"}
	pricingWrite := call{http.MethodPost, "/pricing/"}
	devTokens := call{http.MethodGet, "/dev/tokens"}

	cases := []struct {
		roles []string
		want  map[call]int
	}{
		{[]string{"member", "chair"}, map[call]int{
			journal: 200, pricing: 200,
			rules: 403, post: 403, pricingWrite: 403, devTokens: 403,
		}},
		{[]string{"deputy"}, map[call]int{
			journal: 200, pricing: 200, rules: 403, post: 403, pricingWrite: 403, devTokens: 403,
		}},
		{[]string{"board"}, map[call]int{
			journal: 200, pricing: 200, rules: 200, post: 403, pricingWrite: 403, devTokens: 403,
		}},
		{[]string{"treasurer"}, map[call]int{
			journal: 200, pricing: 200, rules: 200, post: 200, pricingWrite: 200, devTokens: 403,
		}},
		{[]string{"admin"}, map[call]int{
			journal: 200, pricing: 200, rules: 200, post: 200, pricingWrite: 200, devTokens: 200,
		}},
		{[]string{"member", "slip_holder"}, map[call]int{
			journal: 403, pricing: 403, rules: 403, post: 403, devTokens: 403,
		}},
	}

	h := boardReadRouter()
	for _, c := range cases {
		for k, want := range c.want {
			req := httptest.NewRequest(k.method, k.path, nil)
			req = req.WithContext(WithClaims(req.Context(), &auth.Claims{UserID: "u-1", ClubID: "c-1", Roles: c.roles}))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != want {
				t.Errorf("%v %s %s = %d, want %d", c.roles, k.method, k.path, rr.Code, want)
			}
		}
	}
}

func TestRequireRoleOrBoardReadNeedsClaims(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		rr := httptest.NewRecorder()
		RequireRoleOrBoardRead("admin")(noopHandler()).ServeHTTP(rr, httptest.NewRequest(m, "/", nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s without claims = %d, want 401", m, rr.Code)
		}
	}
}

// HEAD is a safe method too: a board viewer passes, a member does not.
func TestRequireRoleOrBoardReadHead(t *testing.T) {
	for roles, want := range map[string]int{"secretary": 200, "member": 403} {
		req := httptest.NewRequest(http.MethodHead, "/", nil)
		req = req.WithContext(WithClaims(req.Context(), &auth.Claims{Roles: []string{roles}}))
		rr := httptest.NewRecorder()
		RequireRoleOrBoardRead("admin")(noopHandler()).ServeHTTP(rr, req)
		if rr.Code != want {
			t.Errorf("HEAD as %s = %d, want %d", roles, rr.Code, want)
		}
	}
}
