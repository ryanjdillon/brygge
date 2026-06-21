package handlers

import (
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/config"
)

type FeaturesHandler struct {
	config *config.Config
	db     *pgxpool.Pool
}

func NewFeaturesHandler(cfg *config.Config, db *pgxpool.Pool) *FeaturesHandler {
	return &FeaturesHandler{config: cfg, db: db}
}

// HandleGetFeatures returns the resolved per-club module flags. The
// clubs row is the source of truth (admins flip switches from Site
// settings); env-var defaults from cfg.Features act as fallback for
// deploys whose row hasn't been backfilled. demo_auth is env-only —
// it gates dev-time bypass code and must not be operator-toggleable
// at runtime.
func (h *FeaturesHandler) HandleGetFeatures(w http.ResponseWriter, r *http.Request) {
	bookings := h.config.Features.Bookings
	projects := h.config.Features.Projects
	calendar := h.config.Features.Calendar
	commerce := h.config.Features.Commerce
	accounting := h.config.Features.Accounting
	feedback := false

	if h.db != nil {
		var b, p, c, co, a, fb *bool
		err := h.db.QueryRow(r.Context(),
			`SELECT feature_bookings, feature_projects, feature_calendar,
			        feature_commerce, feature_accounting,
			        feature_feedback
			   FROM clubs WHERE slug = $1`,
			h.config.ClubSlug,
		).Scan(&b, &p, &c, &co, &a, &fb)
		if err == nil {
			if b != nil {
				bookings = *b
			}
			if p != nil {
				projects = *p
			}
			if c != nil {
				calendar = *c
			}
			if co != nil {
				commerce = *co
			}
			if a != nil {
				accounting = *a
			}
			if fb != nil {
				feedback = *fb
			}
		} else if err != pgx.ErrNoRows {
			// Don't fail the public features endpoint on a transient DB
			// blip — fall through to env defaults so the SPA renders.
		}
	}

	JSON(w, http.StatusOK, map[string]bool{
		"bookings":   bookings,
		"projects":   projects,
		"calendar":   calendar,
		"commerce":   commerce,
		"accounting": accounting,
		"feedback":   feedback,
		"demo_auth":  h.config.Features.DemoAuth,
	})
}
