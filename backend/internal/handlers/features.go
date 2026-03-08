package handlers

import (
	"net/http"

	"github.com/brygge-klubb/brygge/internal/config"
)

type FeaturesHandler struct {
	config *config.Config
}

func NewFeaturesHandler(cfg *config.Config) *FeaturesHandler {
	return &FeaturesHandler{config: cfg}
}

func (h *FeaturesHandler) HandleGetFeatures(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]bool{
		"bookings":       h.config.Features.Bookings,
		"projects":       h.config.Features.Projects,
		"calendar":       h.config.Features.Calendar,
		"commerce":       h.config.Features.Commerce,
		"communications": h.config.Features.Communications,
	})
}
