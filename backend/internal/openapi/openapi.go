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
		"bearer": {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
			Description:  "JWT access token from /api/v1/auth/login or /api/v1/auth/vipps/callback",
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
var BearerSecurity = []map[string][]string{{"bearer": {}}}

// RoleSecurity returns a security requirement with role documentation.
func RoleSecurity(roles ...string) []map[string][]string {
	return []map[string][]string{{"bearer": {}}}
}
