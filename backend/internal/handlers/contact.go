package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
)

// Field caps for the public contact form. They bound what a visitor can push
// into the club's inbox; the message cap is generous for prose but far from
// an attachment-sized payload.
const (
	contactMaxName    = 100
	contactMaxEmail   = 254 // RFC 5321 path limit
	contactMaxSubject = 200
	contactMaxMessage = 5000
	// contactMaxBody bounds the request before it is decoded: the caps above
	// in 4-byte runes, with room for JSON escaping.
	contactMaxBody = 64 << 10
	// contactSendTimeout bounds the synchronous send so a stuck relay cannot
	// hold the request open past what the visitor will wait for.
	contactSendTimeout = 20 * time.Second
)

// ContactHandler takes the public contact form and mails it to the club.
type ContactHandler struct {
	db     pgxQuerier
	config *config.Config
	// mailer is nil without SMTP; the form then answers 503 instead of
	// accepting a message nobody will receive.
	mailer email.Sender
	log    zerolog.Logger
}

func NewContactHandler(
	db pgxQuerier,
	cfg *config.Config,
	mailer email.Sender,
	log zerolog.Logger,
) *ContactHandler {
	return &ContactHandler{
		db:     db,
		config: cfg,
		mailer: mailer,
		log:    log.With().Str("handler", "contact").Logger(),
	}
}

type contactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Subject string `json:"subject"`
	Message string `json:"message"`
	// Website is a honeypot: the form renders it hidden, so only a script
	// that fills every input sets it.
	Website string `json:"website"`
}

// contactRecipient is the one place that decides which club address a contact
// message goes to: the club's secretary, else its chairman. Empty means none
// is configured. The recipient is meant to become a board-config function
// allocation (BRY-482); swap the lookup here and nothing else changes.
func contactRecipient(ctx context.Context, db pgxQuerier, clubSlug string) (string, error) {
	var secretary, chairman string
	err := db.QueryRow(ctx,
		`SELECT COALESCE(secretary_email, ''), COALESCE(chairman_email, '')
		   FROM clubs WHERE slug = $1`,
		clubSlug,
	).Scan(&secretary, &chairman)
	if err != nil {
		// A fresh deploy may not have the clubs row yet; that is "no
		// recipient", not a failure.
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return pickContactRecipient(secretary, chairman), nil
}

// pickContactRecipient prefers the secretary and falls back to the chairman.
// An address that is not a bare, valid mailbox counts as unset, so a typo in
// the settings cannot become a header-injection or misdelivery.
func pickContactRecipient(secretary, chairman string) string {
	for _, candidate := range []string{secretary, chairman} {
		if addr, ok := parseBareEmail(candidate); ok {
			return addr
		}
	}
	return ""
}

func (h *ContactHandler) HandleContactForm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, contactMaxBody)
	var req contactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			Error(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// A filled honeypot gets the same answer as a real submission so the bot
	// learns nothing, and nothing identifying is logged.
	if strings.TrimSpace(req.Website) != "" {
		JSON(w, http.StatusOK, map[string]string{"status": "received"})
		return
	}

	name := cleanSingleLine(req.Name)
	subject := cleanSingleLine(req.Subject)
	message := cleanMessage(req.Message)

	if name == "" || strings.TrimSpace(req.Email) == "" || subject == "" || message == "" {
		Error(w, http.StatusBadRequest, "name, email, subject, and message are required")
		return
	}

	if utf8.RuneCountInString(name) > contactMaxName ||
		len(req.Email) > contactMaxEmail ||
		utf8.RuneCountInString(subject) > contactMaxSubject ||
		utf8.RuneCountInString(message) > contactMaxMessage {
		Error(w, http.StatusBadRequest, "a field is too long")
		return
	}

	replyTo, ok := parseBareEmail(req.Email)
	if !ok {
		Error(w, http.StatusBadRequest, "invalid email address")
		return
	}

	if utf8.RuneCountInString(message) < 10 {
		Error(w, http.StatusBadRequest, "message must be at least 10 characters")
		return
	}

	if h.mailer == nil {
		h.log.Error().Msg("contact form: no mail transport configured, message not delivered")
		Error(w, http.StatusServiceUnavailable, "mail_not_configured")
		return
	}

	to, err := contactRecipient(r.Context(), h.db, h.config.ClubSlug)
	if err != nil {
		h.log.Error().Err(err).Msg("contact form: reading the recipient failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if to == "" {
		h.log.Error().Msg("contact form: club has no secretary or chairman email, message not delivered")
		Error(w, http.StatusServiceUnavailable, "contact_recipient_not_configured")
		return
	}

	// Detached from the request so a visitor closing the tab mid-send does
	// not abort a message the relay is already accepting.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), contactSendTimeout)
	defer cancel()
	// From stays the app's configured sender. The visitor's address only
	// goes into Reply-To, so a forged submitter cannot spoof the club's
	// domain or trip DMARC.
	err = h.mailer.SendWithHeaders(
		ctx,
		to,
		email.ContactSubject(subject),
		email.ContactBody(email.ContactMessage{Name: name, Email: replyTo, Subject: subject, Message: message}),
		map[string]string{"Reply-To": replyTo},
	)
	if err != nil {
		// No name, email, subject or message here: the log must not become a
		// second copy of what the visitor typed.
		h.log.Error().Err(err).Msg("contact form: delivery failed")
		Error(w, http.StatusBadGateway, "contact_delivery_failed")
		return
	}

	h.log.Info().Int("message_len", utf8.RuneCountInString(message)).Msg("contact form delivered")
	JSON(w, http.StatusOK, map[string]string{"status": "received"})
}

// cleanSingleLine turns a visitor-supplied string into one printable line:
// every control character (CR and LF included, so it cannot start a new mail
// header) and line/paragraph separator becomes a space, runs of whitespace
// collapse, and the ends are trimmed.
func cleanSingleLine(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(mapped), " ")
}

// cleanMessage keeps the message's line breaks and tabs but drops other
// control characters, and trims the ends.
func cleanMessage(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}

// parseBareEmail accepts a single mailbox with no display name, comments or
// whitespace, and a dotted domain. It returns the address as parsed.
func parseBareEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsFunc(s, unicode.IsSpace) || strings.ContainsFunc(s, unicode.IsControl) {
		return "", false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || !isValidEmail(addr.Address) {
		return "", false
	}
	return addr.Address, true
}

func isValidEmail(addr string) bool {
	at := strings.Index(addr, "@")
	if at < 1 {
		return false
	}
	domain := addr[at+1:]
	dot := strings.LastIndex(domain, ".")
	return dot > 0 && dot < len(domain)-1
}
