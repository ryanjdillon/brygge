package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/mail"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// InboxHandler exposes the read-only surface for the role-gated
// shared inbox (DIL-277). Each request authenticates to JMAP AS THE
// LOGGED-IN USER using credentials provisioned by DIL-321 — admin
// Basic auth wouldn't work because Stalwart's JMAP session only
// exposes the authenticated principal's own account plus accounts
// the user has been granted shareWith on. The user's session
// surfaces both their own mailbox AND the shared board mailbox via
// the same `accounts` map.
type InboxHandler struct {
	db          *pgxpool.Pool
	jmapFact    *mail.JMAPFactory
	provisioner *mail.UserProvisioner
	adminUser   string
	adminPass   string
	passwords   mail.PrincipalPasswords // shared-principal service passwords (DIL-278 send path)
	clubAbbrev  string                  // uppercased club slug, e.g. "KBL" — prepended to From names
	audit       *audit.Service
	spec        []mail.MailboxSpec
	log         zerolog.Logger

	// Cache: shared-mailbox address → JMAP account ID (e.g.
	// "kasserar@..." → "i"). Populated lazily on first lookup. The
	// JMAP id is stable across Stalwart restarts for an existing
	// principal, so this cache lives for the process lifetime.
	mu        sync.RWMutex
	sharedIDs map[string]string
}

func NewInboxHandler(db *pgxpool.Pool, jmapFact *mail.JMAPFactory, provisioner *mail.UserProvisioner, adminUser, adminPass string, passwords mail.PrincipalPasswords, clubSlug string, auditSvc *audit.Service, spec []mail.MailboxSpec, log zerolog.Logger) *InboxHandler {
	return &InboxHandler{
		db:          db,
		jmapFact:    jmapFact,
		provisioner: provisioner,
		adminUser:   adminUser,
		adminPass:   adminPass,
		passwords:   passwords,
		clubAbbrev:  strings.ToUpper(clubSlug),
		audit:       auditSvc,
		spec:        spec,
		log:         log.With().Str("handler", "inbox").Logger(),
		sharedIDs:   make(map[string]string),
	}
}

// MailboxView is the projection returned by GET /mailboxes. Only the
// fields the SPA needs — JMAP IDs stay server-side.
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

	userJMAP, err := h.userJMAP(ctx, claims.UserID)
	if err != nil {
		// User has no Stalwart principal yet — they can still see
		// the empty list; sidebar entry gates on roles only.
		h.log.Debug().Err(err).Str("user_id", claims.UserID).Msg("no user JMAP credentials")
		JSON(w, http.StatusOK, map[string]any{"mailboxes": []MailboxView{}})
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
		if total, unread, err := h.mailboxCounts(ctx, userJMAP, s.Address); err == nil {
			mv.Total = total
			mv.Unread = unread
		} else {
			h.log.Debug().Err(err).Str("address", s.Address).Msg("mailbox counts unavailable")
		}
		views = append(views, mv)
	}
	JSON(w, http.StatusOK, map[string]any{"mailboxes": views})
}

// HandleListThreads paginates the thread list for a shared mailbox.
//
//	GET /inbox/:address/threads?cursor=&q=&limit=
func (h *InboxHandler) HandleListThreads(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	spec, ok := h.authorize(w, r)
	if !ok {
		return
	}
	claims := middleware.GetClaims(ctx)
	userJMAP, err := h.userJMAP(ctx, claims.UserID)
	if err != nil {
		Error(w, http.StatusForbidden, "user not provisioned for mail")
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

	accountID, inboxID, err := h.resolveInbox(ctx, userJMAP, spec.Address)
	if err != nil {
		h.log.Warn().Err(err).Str("address", spec.Address).Msg("inbox resolve failed")
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}

	ids, total, err := userJMAP.QueryEmails(ctx, accountID, inboxID, text, cursor, limit)
	if err != nil {
		h.log.Warn().Err(err).Msg("Email/query failed")
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}
	summaries, err := userJMAP.GetEmailSummaries(ctx, accountID, ids)
	if err != nil {
		h.log.Warn().Err(err).Msg("Email/get failed")
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}

	type threadRow struct {
		ThreadID   string              `json:"thread_id"`
		Subject    string              `json:"subject"`
		From       []mail.EmailAddress `json:"from"`
		Preview    string              `json:"preview"`
		ReceivedAt string              `json:"received_at"`
		Unread     bool                `json:"unread"`
		HasAttach  bool                `json:"has_attachment"`
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
	claims := middleware.GetClaims(ctx)
	userJMAP, err := h.userJMAP(ctx, claims.UserID)
	if err != nil {
		Error(w, http.StatusForbidden, "user not provisioned for mail")
		return
	}
	threadID := chi.URLParam(r, "thread_id")
	if threadID == "" {
		Error(w, http.StatusBadRequest, "thread_id required")
		return
	}

	accountID, err := h.sharedAccountID(ctx, spec.Address)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}
	ids, err := userJMAP.GetThreadEmailIDs(ctx, accountID, threadID)
	if err != nil {
		Error(w, http.StatusNotFound, "thread not found")
		return
	}
	emails, err := userJMAP.GetEmailsFull(ctx, accountID, ids)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}

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
	claims := middleware.GetClaims(ctx)
	userJMAP, err := h.userJMAP(ctx, claims.UserID)
	if err != nil {
		Error(w, http.StatusForbidden, "user not provisioned for mail")
		return
	}
	threadID := chi.URLParam(r, "thread_id")
	read := r.URL.Query().Get("read") != "false"

	accountID, err := h.sharedAccountID(ctx, spec.Address)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}
	n, err := userJMAP.SetKeywordOnThread(ctx, accountID, threadID, "$seen", read)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}
	h.auditMailboxAction(ctx, audit.ActionInboxMarkRead, spec.Address, threadID)
	JSON(w, http.StatusOK, map[string]any{"updated": n, "read": read})
}

// HandleArchiveThread moves a thread from Inbox to Archive.
func (h *InboxHandler) HandleArchiveThread(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	spec, ok := h.authorize(w, r)
	if !ok {
		return
	}
	claims := middleware.GetClaims(ctx)
	userJMAP, err := h.userJMAP(ctx, claims.UserID)
	if err != nil {
		Error(w, http.StatusForbidden, "user not provisioned for mail")
		return
	}
	threadID := chi.URLParam(r, "thread_id")

	accountID, archiveID, err := h.resolveArchive(ctx, userJMAP, spec.Address)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}
	n, err := userJMAP.MoveThreadToMailbox(ctx, accountID, threadID, archiveID)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend error")
		return
	}
	h.auditMailboxAction(ctx, audit.ActionInboxThreadArchived, spec.Address, threadID)
	JSON(w, http.StatusOK, map[string]any{"moved": n})
}

// sendAttachment is the attachment reference included in a send payload.
type sendAttachment struct {
	BlobID string `json:"blobId"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
}

// SendRequest is the SPA-facing payload for POST /:address/send.
type SendRequest struct {
	To          []emailAddr      `json:"to"`
	Cc          []emailAddr      `json:"cc,omitempty"`
	Bcc         []emailAddr      `json:"bcc,omitempty"`        // individually addressed BCC (compose UI)
	BccGroups   []string         `json:"bcc_groups,omitempty"` // named groups expanded server-side
	Subject     string           `json:"subject"`
	BodyText    string           `json:"body_text"`
	BodyHTML    string           `json:"body_html,omitempty"`
	InReplyTo   string           `json:"in_reply_to,omitempty"`
	Attachments []sendAttachment `json:"attachments,omitempty"`
}

type emailAddr struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

// HandleSend composes and submits a message AS the shared principal.
// Auth: caller must hold the address's mapped role (authorize()) AND
// have re-verified TOTP within the last 10 minutes (RequireFreshTOTP,
// applied at the route level). Backend authenticates JMAP as the
// shared principal using the service password from the password map.
//
//	POST /inbox/:address/send
func (h *InboxHandler) HandleSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	spec, ok := h.authorize(w, r)
	if !ok {
		return
	}
	claims := middleware.GetClaims(ctx)

	var req SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.To) == 0 && len(req.Bcc) == 0 && len(req.BccGroups) == 0 {
		Error(w, http.StatusBadRequest, "at least one recipient required")
		return
	}
	if strings.TrimSpace(req.Subject) == "" && strings.TrimSpace(req.BodyText) == "" && strings.TrimSpace(req.BodyHTML) == "" {
		Error(w, http.StatusBadRequest, "subject or body required")
		return
	}

	pw := h.passwords.Get(spec.Address)
	if pw == "" {
		h.log.Error().Str("address", spec.Address).Msg("no service password for shared principal — send disabled")
		Error(w, http.StatusServiceUnavailable, "mail backend not configured for sending")
		return
	}
	jmap := h.jmapFact.AsPrincipal(principalLocalPart(spec.Address), pw)

	accountID, draftsID, sentID, err := h.resolveSendMailboxes(ctx, jmap)
	if err != nil {
		h.log.Warn().Err(err).Str("address", spec.Address).Msg("resolve send mailboxes failed")
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}

	identityID, err := h.resolveIdentity(ctx, jmap, accountID, spec.Address)
	if err != nil {
		h.log.Warn().Err(err).Str("address", spec.Address).Msg("resolve identity failed")
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}

	// Build BCC list from three sources, deduplicated by lowercase email:
	// 1. spec.BccMembers — existing auto-BCC on certain shared mailboxes
	// 2. req.BccGroups  — named groups from the compose UI
	// 3. req.Bcc        — individually searched members from the compose UI
	var bcc []mail.EmailAddress
	bccSeen := map[string]bool{}
	addBCC := func(name, email string) {
		key := strings.ToLower(email)
		if !bccSeen[key] {
			bccSeen[key] = true
			bcc = append(bcc, mail.EmailAddress{Name: name, Email: email})
		}
	}

	if spec.BccMembers {
		members, err := h.roleMemberEmails(ctx, spec.Role, claims.ClubID)
		if err != nil {
			h.log.Warn().Err(err).Msg("bcc_members lookup failed; sending without bcc")
		} else {
			for _, e := range members {
				addBCC("", e)
			}
		}
	}

	if len(req.BccGroups) > 0 {
		validGroups := map[string]bool{
			"all": true, "members": true, "board": true,
			"slip_holders": true, "waiting_list": true,
		}
		roleMap := map[string]string{
			"members": "member", "board": "board", "slip_holders": "slip_holder",
		}
		for _, g := range req.BccGroups {
			if !validGroups[g] {
				Error(w, http.StatusBadRequest, "unknown bcc_group: "+g)
				return
			}
			var emails []string
			var gerr error
			switch g {
			case "waiting_list":
				emails, gerr = h.waitingListEmails(ctx, claims.ClubID)
			case "all":
				for _, role := range []string{"member", "board", "slip_holder"} {
					re, rerr := h.roleMemberEmails(ctx, role, claims.ClubID)
					if rerr != nil {
						h.log.Warn().Err(rerr).Str("role", role).Msg("bcc_groups all-role lookup failed")
						continue
					}
					emails = append(emails, re...)
				}
			default:
				emails, gerr = h.roleMemberEmails(ctx, roleMap[g], claims.ClubID)
			}
			if gerr != nil {
				h.log.Warn().Err(gerr).Str("group", g).Msg("bcc_groups lookup failed")
			}
			for _, e := range emails {
				addBCC("", e)
			}
		}
	}

	for _, a := range req.Bcc {
		addBCC(a.Name, a.Email)
	}

	// Group-only compose: no explicit To → use the sending mailbox as the
	// visible recipient so the message is well-formed; real recipients receive via BCC.
	if len(req.To) == 0 {
		req.To = []emailAddr{{Email: spec.Address}}
	}

	// From name combines the club abbreviation (uppercase slug,
	// e.g. "KBL") with the spec's role display name (e.g.
	// "Kasserar") so recipients see "KBL Kasserar" at the top of
	// the message. Gmail's column override then surfaces the
	// club's contact-card name ("Klokkarvik Båtlag") in inbox
	// listings.
	fromName := spec.DisplayName
	if h.clubAbbrev != "" {
		fromName = h.clubAbbrev + " " + spec.DisplayName
	}

	// Build attachment body parts for JMAP Email/set.
	var attachBodyParts []map[string]any
	for _, att := range req.Attachments {
		attachBodyParts = append(attachBodyParts, map[string]any{
			"blobId":      att.BlobID,
			"type":        att.Type,
			"name":        att.Name,
			"disposition": "attachment",
		})
	}

	sendReq := mail.SendEmailRequest{
		FromAddress:     spec.Address,
		FromName:        fromName,
		ReplyTo:         spec.Address,
		To:              toMailAddrs(req.To),
		Cc:              toMailAddrs(req.Cc),
		Bcc:             bcc,
		Subject:         req.Subject,
		BodyText:        req.BodyText,
		BodyHTML:        req.BodyHTML,
		InReplyTo:       req.InReplyTo,
		ActorID:         claims.UserID,
		AttachBodyParts: attachBodyParts,
	}
	if req.InReplyTo != "" {
		sendReq.References = []string{req.InReplyTo}
	}

	emailID, messageID, err := jmap.SendEmail(ctx, accountID, identityID, draftsID, sentID, sendReq)
	if err != nil {
		// Stalwart-side error detail stays in journald (Warn line
		// just below). The 502 response carries only a generic
		// message — the user can't act on JMAP internals, and the
		// browser console log + server log together give us the
		// diagnostic chain when needed.
		h.log.Warn().Err(err).Str("address", spec.Address).Msg("send failed")
		Error(w, http.StatusBadGateway, "send failed")
		return
	}

	h.audit.Log(ctx, audit.Entry{
		ClubID:     strPtrIfSet(claims.ClubID),
		ActorID:    strPtrIfSet(claims.UserID),
		Action:     audit.ActionInboxMessageSent,
		Resource:   "mailbox_thread",
		ResourceID: spec.Address + "/" + emailID,
		Details: map[string]any{
			"target_address":  spec.Address,
			"recipient_count": len(req.To) + len(req.Cc) + len(bcc),
			"in_reply_to":     req.InReplyTo,
			"message_id":      messageID,
			"bcc_members":     spec.BccMembers,
			"bcc_groups":      req.BccGroups,
			"attachment_count": len(req.Attachments),
		},
	})

	JSON(w, http.StatusOK, map[string]any{
		"email_id":   emailID,
		"message_id": messageID,
	})
}

func toMailAddrs(in []emailAddr) []mail.EmailAddress {
	out := make([]mail.EmailAddress, 0, len(in))
	for _, a := range in {
		out = append(out, mail.EmailAddress{Name: a.Name, Email: a.Email})
	}
	return out
}

func principalLocalPart(addr string) string {
	if i := strings.IndexByte(addr, '@'); i >= 0 {
		return addr[:i]
	}
	return addr
}

func strPtrIfSet(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// resolveIdentity finds the JMAP Identity id for the From address.
// Stalwart 0.15 auto-creates a default identity per principal —
// we prefer the one whose email matches `address` and fall back to
// the first available if no exact match exists.
func (h *InboxHandler) resolveIdentity(ctx context.Context, jmap *mail.JMAPClient, accountID, address string) (string, error) {
	identities, err := jmap.ListIdentities(ctx, accountID)
	if err != nil {
		return "", err
	}
	if len(identities) == 0 {
		return "", fmt.Errorf("no JMAP identities for %s", address)
	}
	for _, i := range identities {
		if strings.EqualFold(i.Email, address) {
			return i.ID, nil
		}
	}
	return identities[0].ID, nil
}

// resolveSendMailboxes finds the JMAP accountId for the shared
// principal (from the principal's own session, which only contains
// its own account) plus the Drafts/Sent folder ids. Stalwart 0.15
// only auto-creates Inbox on principal init, so the first time we
// send from a shared mailbox we create the Drafts/Sent folders
// ourselves via Mailbox/set — idempotent because we enumerate
// first.
func (h *InboxHandler) resolveSendMailboxes(ctx context.Context, jmap *mail.JMAPClient) (accountID, draftsID, sentID string, err error) {
	accounts, err := jmap.SessionAccounts(ctx)
	if err != nil {
		return "", "", "", err
	}
	if len(accounts) == 0 {
		return "", "", "", errInboxNotFound
	}
	accountID = accounts[0]
	mboxes, err := jmap.ListMailboxes(ctx, accountID)
	if err != nil {
		return "", "", "", err
	}
	for _, m := range mboxes {
		switch strings.ToLower(m.Role) {
		case "drafts":
			draftsID = m.ID
		case "sent":
			sentID = m.ID
		}
	}
	if draftsID == "" {
		id, cerr := jmap.CreateMailbox(ctx, accountID, "Drafts", "drafts")
		if cerr != nil {
			return "", "", "", fmt.Errorf("create Drafts: %w", cerr)
		}
		draftsID = id
		h.log.Info().Str("account", accountID).Str("mailbox", id).Msg("created Drafts folder on demand")
	}
	if sentID == "" {
		id, cerr := jmap.CreateMailbox(ctx, accountID, "Sent", "sent")
		if cerr != nil {
			return "", "", "", fmt.Errorf("create Sent: %w", cerr)
		}
		sentID = id
		h.log.Info().Str("account", accountID).Str("mailbox", id).Msg("created Sent folder on demand")
	}
	return accountID, draftsID, sentID, nil
}

func (h *InboxHandler) roleMemberEmails(ctx context.Context, role, clubID string) ([]string, error) {
	rows, err := h.db.Query(ctx, `
		SELECT u.email
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		WHERE ur.role = $1::user_role AND u.club_id = $2
		GROUP BY u.email
	`, role, clubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (h *InboxHandler) waitingListEmails(ctx context.Context, clubID string) ([]string, error) {
	rows, err := h.db.Query(ctx, `
		SELECT DISTINCT u.email
		FROM waiting_list_entries wle
		JOIN users u ON u.id = wle.user_id
		WHERE wle.club_id = $1
		  AND wle.status NOT IN ('offered_accepted', 'cancelled')
		  AND u.email IS NOT NULL
		  AND u.email <> ''
	`, clubID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// sharedJMAP returns a JMAPClient authenticated as the shared principal
// for the given address, using the service password from the password map.
func (h *InboxHandler) sharedJMAP(address string) (*mail.JMAPClient, error) {
	pw := h.passwords.Get(address)
	if pw == "" {
		return nil, fmt.Errorf("no service password for %s", address)
	}
	return h.jmapFact.AsPrincipal(principalLocalPart(address), pw), nil
}

// resolveSharedAccountID returns the JMAP account ID for a shared principal
// by calling SessionAccounts on the shared principal's own JMAP session.
func (h *InboxHandler) resolveSharedAccountID(ctx context.Context, jmap *mail.JMAPClient, address string) (string, error) {
	accounts, err := jmap.SessionAccounts(ctx)
	if err != nil {
		return "", fmt.Errorf("sharedAccountID %s: %w", address, err)
	}
	if len(accounts) == 0 {
		return "", fmt.Errorf("sharedAccountID %s: no accounts in session", address)
	}
	return accounts[0], nil
}

// HandleBlobDownload proxies a JMAP blob to the browser as a download.
// GET /api/v1/admin/inbox/{address}/blob/{blobId}?name=<filename>
func (h *InboxHandler) HandleBlobDownload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	spec, ok := h.authorize(w, r)
	if !ok {
		return
	}

	blobID := chi.URLParam(r, "blobId")
	if blobID == "" {
		Error(w, http.StatusBadRequest, "missing blobId")
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "attachment"
	}

	sharedJMAP, err := h.sharedJMAP(spec.Address)
	if err != nil {
		h.log.Warn().Err(err).Str("address", spec.Address).Msg("sharedJMAP unavailable")
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}

	accountID, err := h.resolveSharedAccountID(ctx, sharedJMAP, spec.Address)
	if err != nil {
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}

	ct, body, err := sharedJMAP.DownloadBlob(ctx, accountID, blobID, name)
	if err != nil {
		h.log.Warn().Err(err).Str("blobId", blobID).Msg("blob download failed")
		Error(w, http.StatusBadGateway, "blob unavailable")
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeFilename(name)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, body) //nolint:errcheck
}

func sanitizeFilename(name string) string {
	r := strings.NewReplacer(`"`, "", `\`, "", "\n", "", "\r", "")
	return r.Replace(name)
}

var allowedMIMETypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true,
	"image/webp": true, "image/svg+xml": true,
	"application/pdf":    true,
	"application/msword": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"application/vnd.ms-excel": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true,
	"text/plain": true, "text/csv": true,
}

const maxUploadBytes = 10 * 1024 * 1024 // 10 MB

// HandleBlobUpload receives a multipart file upload, validates the MIME type,
// and stores it in the JMAP blob store under the shared principal's account.
// POST /api/v1/admin/inbox/{address}/blob
func (h *InboxHandler) HandleBlobUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	spec, ok := h.authorize(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1024)
	if err := r.ParseMultipartForm(4 * 1024 * 1024); err != nil {
		Error(w, http.StatusBadRequest, "file too large or bad form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		Error(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	if header.Size > maxUploadBytes {
		Error(w, http.StatusRequestEntityTooLarge, "file too large (max 10 MB)")
		return
	}

	// Sniff MIME type from the first 512 bytes.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	detectedType := http.DetectContentType(buf[:n])
	// Seek back so we can stream the full file.
	if seeker, ok := file.(io.Seeker); ok {
		seeker.Seek(0, io.SeekStart) //nolint:errcheck
	}

	// Use declared Content-Type as canonical; fall back to detected.
	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = detectedType
	}
	// Strip parameters (e.g. charset).
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	if !allowedMIMETypes[ct] {
		Error(w, http.StatusUnsupportedMediaType, "file type not allowed")
		return
	}

	sharedJMAP, err := h.sharedJMAP(spec.Address)
	if err != nil {
		h.log.Warn().Err(err).Str("address", spec.Address).Msg("sharedJMAP unavailable for blob upload")
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}
	accountID, err := h.resolveSharedAccountID(ctx, sharedJMAP, spec.Address)
	if err != nil {
		h.log.Warn().Err(err).Str("address", spec.Address).Msg("resolveSharedAccountID failed for blob upload")
		Error(w, http.StatusBadGateway, "mail backend unavailable")
		return
	}

	blobID, err := sharedJMAP.UploadBlob(ctx, accountID, ct, file)
	if err != nil {
		h.log.Warn().Err(err).Msg("blob upload failed")
		Error(w, http.StatusBadGateway, "upload failed")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"blobId": blobID,
		"name":   header.Filename,
		"size":   header.Size,
		"type":   ct,
	})
}

// HandleProxyImage proxies remote images through the server with SSRF guards.
// GET /api/v1/admin/inbox/proxy-image?url=<encoded>
func (h *InboxHandler) HandleProxyImage(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	if raw == "" {
		Error(w, http.StatusBadRequest, "missing url")
		return
	}

	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		Error(w, http.StatusBadRequest, "invalid url")
		return
	}

	host := parsed.Hostname()
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		Error(w, http.StatusBadGateway, "could not resolve host")
		return
	}
	for _, a := range addrs {
		if isPrivateIP(a) {
			Error(w, http.StatusForbidden, "forbidden host")
			return
		}
	}

	// Custom dialer that re-validates the IP after DNS resolution
	// to guard against DNS rebinding.
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			connHost, _, _ := net.SplitHostPort(addr)
			if isPrivateIP(connHost) {
				return nil, fmt.Errorf("forbidden: private IP %s", connHost)
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 1 {
				return http.ErrUseLastResponse
			}
			rHost := req.URL.Hostname()
			rAddrs, rerr := net.LookupHost(rHost)
			if rerr != nil {
				return fmt.Errorf("redirect host unresolvable")
			}
			for _, a := range rAddrs {
				if isPrivateIP(a) {
					return fmt.Errorf("redirect to private IP forbidden")
				}
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, raw, nil)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid url")
		return
	}
	req.Header.Set("User-Agent", "Brygge-ImageProxy/1.0")

	resp, err := client.Do(req)
	if err != nil {
		Error(w, http.StatusBadGateway, "upstream error")
		return
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		Error(w, http.StatusBadGateway, "not an image")
		return
	}

	w.Header().Set("Content-Type", ct)
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "max-age=3600")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, io.LimitReader(resp.Body, 5*1024*1024)) //nolint:errcheck
}

// isPrivateIP returns true for loopback, RFC1918, link-local, and
// IPv6 private ranges. Input is a string IP (from net.LookupHost or
// net.SplitHostPort).
func isPrivateIP(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return true // unparseable → treat as private
	}
	private := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
		"100.64.0.0/10", // Tailscale/CGNAT
	}
	for _, cidr := range private {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// authorize resolves :address, verifies the caller has the mapped role.
// chi v5 returns the raw URL segment; the `@` in email addresses is
// percent-encoded by the SPA (kasserar%40…), so we decode before
// comparing against the spec.
func (h *InboxHandler) authorize(w http.ResponseWriter, r *http.Request) (mail.MailboxSpec, bool) {
	raw := chi.URLParam(r, "address")
	address, derr := url.PathUnescape(raw)
	if derr != nil {
		address = raw
	}
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

// userJMAP builds a JMAPClient authenticated as the calling user.
// Returns an error if the user has no provisioned credentials yet.
func (h *InboxHandler) userJMAP(ctx context.Context, userID string) (*mail.JMAPClient, error) {
	user, pass, ok, err := h.provisioner.Credentials(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errUserNotProvisioned
	}
	return h.jmapFact.AsPrincipal(user, pass), nil
}

// sharedAccountID returns the JMAP id of the shared mailbox's owner
// principal. Lazily populated via admin's Principal/get; cached for
// the process lifetime since JMAP ids are stable on Stalwart 0.15.
func (h *InboxHandler) sharedAccountID(ctx context.Context, address string) (string, error) {
	key := strings.ToLower(address)
	h.mu.RLock()
	if id, ok := h.sharedIDs[key]; ok {
		h.mu.RUnlock()
		return id, nil
	}
	h.mu.RUnlock()

	admin := h.jmapFact.AsPrincipal(h.adminUser, h.adminPass)
	accounts, err := admin.SessionAccounts(ctx)
	if err != nil {
		return "", err
	}
	if len(accounts) == 0 {
		return "", errInboxNotFound
	}
	principals, err := admin.ListPrincipals(ctx, accounts[0])
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range principals {
		if p.Email != "" {
			h.sharedIDs[strings.ToLower(p.Email)] = p.ID
		}
	}
	if id, ok := h.sharedIDs[key]; ok {
		return id, nil
	}
	return "", errInboxNotFound
}

// resolveInbox finds the Inbox folder of a shared mailbox, scoped by
// the user's JMAP session. Stalwart honors shareWith on cross-account
// Mailbox/get calls — the user can enumerate the shared owner's
// folders even though the owner is a different principal.
func (h *InboxHandler) resolveInbox(ctx context.Context, userJMAP *mail.JMAPClient, address string) (accountID, mailboxID string, err error) {
	accountID, err = h.sharedAccountID(ctx, address)
	if err != nil {
		return "", "", err
	}
	mboxes, err := userJMAP.ListMailboxes(ctx, accountID)
	if err != nil {
		return "", "", err
	}
	for _, m := range mboxes {
		if strings.EqualFold(m.Role, "inbox") {
			return accountID, m.ID, nil
		}
	}
	// Stalwart creates Inbox on first login; create it explicitly if missing.
	id, cerr := userJMAP.CreateMailbox(ctx, accountID, "Inbox", "inbox")
	if cerr != nil {
		return "", "", fmt.Errorf("create Inbox for %s: %w", address, cerr)
	}
	h.log.Info().Str("address", address).Str("mailbox", id).Msg("created Inbox folder on demand")
	return accountID, id, nil
}

func (h *InboxHandler) resolveArchive(ctx context.Context, userJMAP *mail.JMAPClient, address string) (accountID, mailboxID string, err error) {
	accountID, err = h.sharedAccountID(ctx, address)
	if err != nil {
		return "", "", err
	}
	mboxes, err := userJMAP.ListMailboxes(ctx, accountID)
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

// mailboxCounts returns (totalThreads, unreadThreads) for the inbox
// folder of an address. Failures here are non-fatal — they degrade
// the sidebar badge but don't fail the page load.
func (h *InboxHandler) mailboxCounts(ctx context.Context, userJMAP *mail.JMAPClient, address string) (int, int, error) {
	accountID, err := h.sharedAccountID(ctx, address)
	if err != nil {
		return 0, 0, err
	}
	mboxes, err := userJMAP.ListMailboxes(ctx, accountID)
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
	// Empty role means the mailbox is accessible to any board or admin user.
	if want == "" {
		for _, r := range roles {
			if strings.EqualFold(r, "admin") || strings.EqualFold(r, "board") {
				return true
			}
		}
		return false
	}
	for _, r := range roles {
		if strings.EqualFold(r, want) {
			return true
		}
	}
	return false
}

var (
	errInboxNotFound      = inboxFolderErr("inbox folder not found on shared mailbox")
	errArchiveNotFound    = inboxFolderErr("archive folder not found on shared mailbox")
	errUserNotProvisioned = inboxFolderErr("user has no JMAP credentials provisioned")
)

type inboxFolderErr string

func (e inboxFolderErr) Error() string { return string(e) }
