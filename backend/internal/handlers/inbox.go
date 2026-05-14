package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/mail"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// InboxHandler exposes the read-only surface for the role-gated
// shared inbox (DIL-277). Mounted under /api/v1/admin/inbox with
// TOTP-gated admin auth applied at the router level. The handler
// re-checks the caller's role against the address spec on every
// request — defence-in-depth in case Stalwart's ACLs ever drift.
type InboxHandler struct {
	db    *pgxpool.Pool
	admin *mail.AdminClient
	jmap  *mail.JMAPClient
	audit *audit.Service
	spec  []mail.MailboxSpec
	log   zerolog.Logger
}

func NewInboxHandler(db *pgxpool.Pool, admin *mail.AdminClient, jmap *mail.JMAPClient, auditSvc *audit.Service, spec []mail.MailboxSpec, log zerolog.Logger) *InboxHandler {
	return &InboxHandler{
		db:    db,
		admin: admin,
		jmap:  jmap,
		audit: auditSvc,
		spec:  spec,
		log:   log.With().Str("handler", "inbox").Logger(),
	}
}

// MailboxView is the projection returned by GET /mailboxes. Only the
// fields the SPA needs — JMAP IDs stay server-side so a stale token
// in the browser can't be replayed against Stalwart.
type MailboxView struct {
	Address     string `json:"address"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
	Unread      int    `json:"unread"`
	Total       int    `json:"total"`
	CanSendAs   bool   `json:"can_send_as"`
}

// HandleListMailboxes returns mailboxes the caller can read, with
// unread counts. Empty list (not 403) is the right answer for a user
// with no board mailbox role — the SPA hides the sidebar entry in
// that case.
func (h *InboxHandler) HandleListMailboxes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	views := make([]MailboxView, 0)
	for _, s := range h.spec {
		if !strings.EqualFold(s.Type, "shared") {
			continue
		}
		if !hasInboxRole(claims.Roles, s.Role) {
			continue
		}
		mv := MailboxView{
			Address:     s.Address,
			Role:        s.Role,
			DisplayName: s.DisplayName,
			CanSendAs:   s.SendAs,
		}
		if total, unread, err := h.mailboxCounts(ctx, s.Address); err == nil {
			mv.Total = total
			mv.Unread = unread
		} else {
			h.log.Debug().Err(err).Str("address", s.Address).Msg("mailbox counts unavailable")
		}
		views = append(views, mv)
	}
	JSON(w, http.StatusOK, map[string]any{"mailboxes": views})
}

// HandleListThreads paginates the inbox/archive thread list for a
// single shared mailbox.
//
//	GET /inbox/:address/threads?cursor=&q=&limit=
func (h *InboxHandler) HandleListThreads(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	spec, ok := h.authorize(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	cursor, _ := strconv.Atoi(q.Get("cursor"))
	if cursor < 0 {
		cursor = 0
	}
	text := strings.TrimSpace(q.Get("q"))

	accountID, inboxID, err := h.resolveInbox(ctx, spec.Address)
	if err != nil {
		h.log.Warn().Err(err).Str("address", spec.Address).Msg("inbox resolve failed")
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}

	ids, total, err := h.jmap.QueryEmails(ctx, accountID, inboxID, text, cursor, limit)
	if err != nil {
		h.log.Warn().Err(err).Msg("Email/query failed")
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}
	summaries, err := h.jmap.GetEmailSummaries(ctx, accountID, ids)
	if err != nil {
		h.log.Warn().Err(err).Msg("Email/get failed")
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}

	// Reduce to one row per thread: keep the newest message we saw
	// in the page, mark unread if any of its emails lack $seen.
	type threadRow struct {
		ThreadID   string                `json:"thread_id"`
		Subject    string                `json:"subject"`
		From       []mail.EmailAddress   `json:"from"`
		Preview    string                `json:"preview"`
		ReceivedAt string                `json:"received_at"`
		Unread     bool                  `json:"unread"`
		HasAttach  bool                  `json:"has_attachment"`
	}
	threads := make([]threadRow, 0, len(summaries))
	byThread := map[string]int{}
	for _, s := range summaries {
		_, seen := s.Keywords["$seen"]
		row := threadRow{
			ThreadID:   s.ThreadID,
			Subject:    s.Subject,
			From:       s.From,
			Preview:    s.Preview,
			ReceivedAt: s.ReceivedAt,
			Unread:     !seen,
			HasAttach:  s.HasAttach,
		}
		if idx, ok := byThread[s.ThreadID]; ok {
			threads[idx].Unread = threads[idx].Unread || row.Unread
			continue
		}
		byThread[s.ThreadID] = len(threads)
		threads = append(threads, row)
	}

	JSON(w, http.StatusOK, map[string]any{
		"threads":     threads,
		"total":       total,
		"next_cursor": cursor + len(ids),
	})
}

// HandleGetThread returns the full message list for a single thread.
//
//	GET /inbox/:address/threads/:thread_id
func (h *InboxHandler) HandleGetThread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	spec, ok := h.authorize(w, r)
	if !ok {
		return
	}
	threadID := chi.URLParam(r, "thread_id")
	if threadID == "" {
		Error(w, http.StatusBadRequest, "thread_id required")
		return
	}

	accountID, _, err := h.resolveInbox(ctx, spec.Address)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}
	ids, err := h.jmap.GetThreadEmailIDs(ctx, accountID, threadID)
	if err != nil {
		Error(w, http.StatusNotFound, "thread not found")
		return
	}
	emails, err := h.jmap.GetEmailsFull(ctx, accountID, ids)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}

	// HTML rendering and sanitisation happen in the SPA via
	// DOMPurify. We expose only the raw body string + the part list
	// so the client controls rendering; this also keeps the backend
	// dependency-free of an HTML parser.
	h.auditMailboxAction(ctx, audit.ActionInboxThreadViewed, spec.Address, threadID)
	JSON(w, http.StatusOK, map[string]any{
		"thread_id": threadID,
		"emails":    emails,
	})
}

// HandleMarkRead toggles $seen on every email in a thread.
//
//	POST /inbox/:address/threads/:thread_id/mark_read?read=true|false
func (h *InboxHandler) HandleMarkRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	spec, ok := h.authorize(w, r)
	if !ok {
		return
	}
	threadID := chi.URLParam(r, "thread_id")
	read := r.URL.Query().Get("read") != "false" // default true

	accountID, _, err := h.resolveInbox(ctx, spec.Address)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}
	n, err := h.jmap.SetKeywordOnThread(ctx, accountID, threadID, "$seen", read)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}
	h.auditMailboxAction(ctx, audit.ActionInboxMarkRead, spec.Address, threadID)
	JSON(w, http.StatusOK, map[string]any{"updated": n, "read": read})
}

// HandleArchiveThread moves a thread from Inbox to Archive.
//
//	POST /inbox/:address/threads/:thread_id/archive
func (h *InboxHandler) HandleArchiveThread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	spec, ok := h.authorize(w, r)
	if !ok {
		return
	}
	threadID := chi.URLParam(r, "thread_id")

	accountID, archiveID, err := h.resolveArchive(ctx, spec.Address)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}
	n, err := h.jmap.MoveThreadToMailbox(ctx, accountID, threadID, archiveID)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}
	h.auditMailboxAction(ctx, audit.ActionInboxThreadArchived, spec.Address, threadID)
	JSON(w, http.StatusOK, map[string]any{"moved": n})
}

// HandleProxyImage is a stub for the show-images opt-in. Phase-2
// landed without a working implementation — the SPA defaults images
// off, and turning them on without this endpoint just leaves them
// broken (no remote fetch happens). Tracked as a phase-2 follow-up.
func (h *InboxHandler) HandleProxyImage(w http.ResponseWriter, r *http.Request) {
	Error(w, http.StatusNotImplemented, "image proxy not implemented; remote images are off in v1")
}

// authorize resolves :address from the URL, looks it up in the spec,
// and verifies the caller has the mapped role. On failure writes the
// HTTP error and returns ok=false.
func (h *InboxHandler) authorize(w http.ResponseWriter, r *http.Request) (mail.MailboxSpec, bool) {
	address := chi.URLParam(r, "address")
	for _, s := range h.spec {
		if !strings.EqualFold(s.Address, address) {
			continue
		}
		if !strings.EqualFold(s.Type, "shared") {
			Error(w, http.StatusNotFound, "mailbox not found")
			return mail.MailboxSpec{}, false
		}
		claims := middleware.GetClaims(r.Context())
		if claims == nil {
			Error(w, http.StatusUnauthorized, "authentication required")
			return mail.MailboxSpec{}, false
		}
		if !hasInboxRole(claims.Roles, s.Role) {
			Error(w, http.StatusForbidden, "role required")
			return mail.MailboxSpec{}, false
		}
		return s, true
	}
	Error(w, http.StatusNotFound, "mailbox not found")
	return mail.MailboxSpec{}, false
}

func (h *InboxHandler) resolveInbox(ctx context.Context, address string) (accountID, mailboxID string, err error) {
	accountID, err = h.admin.LookupPrincipal(ctx, address)
	if err != nil {
		return "", "", err
	}
	mboxes, err := h.jmap.ListMailboxes(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	for _, m := range mboxes {
		if strings.EqualFold(m.Role, "inbox") {
			return accountID, m.ID, nil
		}
	}
	return "", "", errInboxNotFound
}

func (h *InboxHandler) resolveArchive(ctx context.Context, address string) (accountID, mailboxID string, err error) {
	accountID, err = h.admin.LookupPrincipal(ctx, address)
	if err != nil {
		return "", "", err
	}
	mboxes, err := h.jmap.ListMailboxes(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	for _, m := range mboxes {
		if strings.EqualFold(m.Role, "archive") {
			return accountID, m.ID, nil
		}
	}
	return "", "", errArchiveNotFound
}

// mailboxCounts returns (total, unread) for the inbox folder of an
// address. Failures here are non-fatal — they just degrade the
// sidebar unread-count badge.
func (h *InboxHandler) mailboxCounts(ctx context.Context, address string) (int, int, error) {
	accountID, err := h.admin.LookupPrincipal(ctx, address)
	if err != nil || accountID == "" {
		return 0, 0, err
	}
	mboxes, err := h.jmap.ListMailboxes(ctx, accountID)
	if err != nil {
		return 0, 0, err
	}
	for _, m := range mboxes {
		if strings.EqualFold(m.Role, "inbox") {
			return m.TotalThreads, m.UnreadThreads, nil
		}
	}
	return 0, 0, nil
}

func (h *InboxHandler) auditMailboxAction(ctx context.Context, action, address, threadID string) {
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		return
	}
	h.audit.LogAction(ctx, claims.ClubID, claims.UserID, "", action, "mailbox_thread", address+"/"+threadID, nil)
}

func hasInboxRole(roles []string, want string) bool {
	for _, r := range roles {
		if strings.EqualFold(r, want) {
			return true
		}
	}
	return false
}

var (
	errInboxNotFound   = inboxFolderErr("inbox folder not found on shared mailbox")
	errArchiveNotFound = inboxFolderErr("archive folder not found on shared mailbox")
)

type inboxFolderErr string

func (e inboxFolderErr) Error() string { return string(e) }
