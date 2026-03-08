package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
)

type ContactHandler struct {
	config *config.Config
	log    zerolog.Logger
}

func NewContactHandler(
	cfg *config.Config,
	log zerolog.Logger,
) *ContactHandler {
	return &ContactHandler{
		config: cfg,
		log:    log.With().Str("handler", "contact").Logger(),
	}
}

type contactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Subject string `json:"subject"`
	Message string `json:"message"`
}

func (h *ContactHandler) HandleContactForm(w http.ResponseWriter, r *http.Request) {
	var req contactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" || req.Email == "" || req.Subject == "" || req.Message == "" {
		Error(w, http.StatusBadRequest, "name, email, subject, and message are required")
		return
	}

	if !isValidEmail(req.Email) {
		Error(w, http.StatusBadRequest, "invalid email address")
		return
	}

	if len(strings.TrimSpace(req.Message)) < 10 {
		Error(w, http.StatusBadRequest, "message must be at least 10 characters")
		return
	}

	h.log.Info().
		Str("name", req.Name).
		Str("email", req.Email).
		Str("subject", req.Subject).
		Msg("contact form submission received")

	JSON(w, http.StatusOK, map[string]string{"status": "received"})
}

func isValidEmail(email string) bool {
	at := strings.Index(email, "@")
	if at < 1 {
		return false
	}
	domain := email[at+1:]
	dot := strings.LastIndex(domain, ".")
	return dot > 0 && dot < len(domain)-1
}
