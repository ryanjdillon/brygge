package openapi

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

// Config holds options for API setup.
type Config struct {
	DocsEnabled bool
}

// NewAPI creates a huma API instance wrapping the given chi router.
func NewAPI(router chi.Router, cfg Config) huma.API {
	humaConfig := huma.DefaultConfig("Brygge API", "1.0.0")
	humaConfig.Info.Description = "Marina and club management platform API"

	humaConfig.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"sessionCookie": {
			Type:        "apiKey",
			In:          "cookie",
			Name:        "brygge_session",
			Description: "Session cookie set by /api/v1/auth/verify after a successful magic-link click.",
		},
	}

	if !cfg.DocsEnabled {
		config := humaConfig
		config.DocsPath = ""
		return humachi.New(router, config)
	}

	return humachi.New(router, humaConfig)
}

// BearerSecurity is the security requirement for authenticated endpoints.
// Despite the historical name, the SPA authenticates via the
// `brygge_session` cookie issued by /api/v1/auth/verify; the value here
// references the sessionCookie security scheme defined above.
var BearerSecurity = []map[string][]string{{"sessionCookie": {}}}

// RoleSecurity returns a security requirement with role documentation.
func RoleSecurity(roles ...string) []map[string][]string {
	return []map[string][]string{{"bearer": {}}}
}
