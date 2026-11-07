package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// deletionGracePeriod is the cooling-off window between a member's deletion
// request and the earliest moment an admin may process it. GDPR Art. 12(3)
// gives the club one month from the request to act, so the work item belongs
// from grace_end and is overdue one calendar month after requested_at.
const deletionGracePeriod = 14 * 24 * time.Hour

// legalDocTypes is the vocabulary of legal_documents.doc_type: the documents a
// club publishes in versions. The column is TEXT without a CHECK, so these
// handlers are what keep it closed.
var legalDocTypes = map[string]bool{
	"terms":         true,
	"privacy":       true,
	"statutes":      true,
	"harbour_rules": true,
	"terms_of_sale": true,
}

// consentTypes is the vocabulary of user_consents.consent_type. Each legal
// document type is a consent to that document's version; newsletter is an
// opt-in with no document behind it.
var consentTypes = map[string]bool{
	"terms":         true,
	"privacy":       true,
	"statutes":      true,
	"harbour_rules": true,
	"terms_of_sale": true,
	"newsletter":    true,
}

// newsletterConsentVersion is what a newsletter opt-in records as its version:
// there is no document behind it, so there is nothing to version.
const newsletterConsentVersion = ""

// codeLegalDocumentUnpublished answers a consent to a document type the
// caller's club has not published, so there is no version to consent to.
const codeLegalDocumentUnpublished = "legal_document_unpublished"

// codeLegalDocumentStale answers a consent whose expected version is no longer
// the club's current one: the member was shown a text the board has since
// replaced, and must read the new one before consenting to it.
const codeLegalDocumentStale = "legal_document_stale"

// latestLegalOrder decides which published row is a type's current version,
// shared by the public read and LatestLegalVersions so the text a visitor
// reads is the version a consent records. created_at, then id, break a tie
// between two rows published in the same instant.
const latestLegalOrder = `published_at DESC, created_at DESC, id DESC`

// LatestLegalVersions returns the latest published version of each of
// docTypes for clubID, and the types among docTypes the club has not
// published (in the order asked). An unknown type is an error, not a missing
// one, so a caller's typo cannot pass as an unpublished document.
func LatestLegalVersions(ctx context.Context, q dbQuerier, clubID string, docTypes []string) (versions map[string]string, missing []string, err error) {
	for _, t := range docTypes {
		if !legalDocTypes[t] {
			return nil, nil, fmt.Errorf("unknown legal document type %q", t)
		}
	}
	versions = make(map[string]string, len(docTypes))
	if len(docTypes) == 0 {
		return versions, nil, nil
	}

	rows, err := q.Query(ctx,
		`SELECT DISTINCT ON (doc_type) doc_type, version
		 FROM legal_documents
		 WHERE club_id = $1 AND doc_type = ANY($2) AND published_at IS NOT NULL
		 ORDER BY doc_type, `+latestLegalOrder,
		clubID, docTypes,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query latest legal versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var docType, version string
		if err := rows.Scan(&docType, &version); err != nil {
			return nil, nil, fmt.Errorf("scan latest legal version: %w", err)
		}
		versions[docType] = version
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate latest legal versions: %w", err)
	}

	for _, t := range docTypes {
		if _, ok := versions[t]; !ok && !slices.Contains(missing, t) {
			missing = append(missing, t)
		}
	}
	return versions, missing, nil
}

type GDPRHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewGDPRHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *GDPRHandler {
	return &GDPRHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "gdpr").Logger(),
	}
}

// exportSection is one named part of a member's data export. query selects
// the section's rows for the member $1 in club $2; Postgres renders them as
// JSON, so UUIDs, numerics, dates and enums come out as plain JSON values.
// Every column is the member's own data: ids and names of other people (the
// admin who granted a role or confirmed a boat, a guest on a booking, the
// board address a notice about the member went to) are left out on purpose.
type exportSection struct {
	key string
	// single sections are one object (null when there is no row) rather
	// than a list.
	single bool
	query  string
}

// slipLabelSQL is the slip's label as the board writes it ("A12"), for alias
// s of slips: the section is prefixed unless the number already carries it.
const slipLabelSQL = `CASE WHEN s.number LIKE COALESCE(s.section, '') || '%' THEN s.number
	ELSE COALESCE(s.section, '') || s.number END`

// dataExportSections is the member data export (GDPR Art. 15 and 20). The
// response is a JSON object, so its keys carry no order; within a list
// section, rows follow the query's ORDER BY (json_agg over a sorted subquery
// keeps that order in practice, though SQL does not promise it). A test runs
// every query against the migrated schema, so a renamed column fails CI
// instead of answering 500 to the member.
var dataExportSections = []exportSection{
	{key: "profile", single: true, query: `
		SELECT first_name, last_name, email, phone, address_line, postal_code, city,
		       is_local, address_verified, address_kommunenummer, address_verified_at,
		       preferred_language, hide_in_directory,
		       member_since, membership_cancelled_at, membership_ends_on, membership_cancel_reason,
		       created_at
		  FROM users WHERE id = $1 AND club_id = $2`},
	{key: "roles", query: `
		SELECT role, granted_at FROM user_roles
		 WHERE user_id = $1 AND club_id = $2 ORDER BY granted_at, role`},
	{key: "harbor_share", single: true, query: `
		SELECT harbor_share_owner AS owner, harbor_share_paid_on AS paid_on
		  FROM users WHERE id = $1 AND club_id = $2`},
	{key: "boats", query: `
		SELECT id, name, type, manufacturer, model, length_m, beam_m, draft_m, weight_kg,
		       registration_number, mmsi, call_sign, measurements_confirmed, created_at
		  FROM boats WHERE user_id = $1 AND club_id = $2 ORDER BY created_at, id`},
	{key: "slip_assignments", query: `
		SELECT sa.id, ` + slipLabelSQL + ` AS slip_label, sa.boat_id, sa.assignment_type,
		       sa.assigned_at, sa.released_at, sa.harbor_membership_amount, sa.harbor_membership_paid_at
		  FROM slip_assignments sa JOIN slips s ON s.id = sa.slip_id
		 WHERE sa.user_id = $1 AND sa.club_id = $2 ORDER BY sa.assigned_at, sa.id`},
	{key: "waiting_list", query: `
		SELECT id, boat_id, position, is_local, status, offer_deadline, created_at
		  FROM waiting_list_entries WHERE user_id = $1 AND club_id = $2 ORDER BY created_at, id`},
	// The recipient_* columns are left out: they name the organisation or
	// contact person a faktura was addressed to, who may not be the member.
	{key: "fakturas", query: `
		SELECT i.id, i.invoice_number, i.kid_number, i.issue_date, i.due_date, i.total_amount,
		       i.status, i.category, i.purpose, i.sent_at, i.member_name, i.member_email,
		       i.credits_invoice_id, i.payment_id, i.online_paid_at, i.created_at,
		       (SELECT COALESCE(json_agg(l ORDER BY l.description, l.id), '[]'::json)
		          FROM (SELECT il.id, il.description, il.sub_description, il.quantity, il.unit_price,
		                       il.line_total, il.category, il.boat_id
		                  FROM invoice_lines il WHERE il.invoice_id = i.id) l) AS lines
		  FROM invoices i WHERE i.user_id = $1 AND i.club_id = $2
		 ORDER BY i.issue_date, i.invoice_number, i.id`},
	// A payment or order can settle several members' fakturas. The member
	// sees the whole row only when it is theirs alone (they are its payer and
	// it settles nobody else's faktura); otherwise they see their own
	// allocation (the sum of their fakturas it settles) and allocation_only,
	// without the description, the provider reference or the order lines,
	// which describe other people's money.
	{key: "payments", query: `
		SELECT p.id, p.type, p.currency, p.status, p.due_date, p.paid_at, p.created_at,
		       NOT f.whole AS allocation_only,
		       CASE WHEN f.whole THEN p.amount ELSE a.own_amount END AS amount,
		       CASE WHEN f.whole THEN p.description END AS description,
		       CASE WHEN f.whole THEN p.vipps_reference END AS vipps_reference
		  FROM payments p
		 CROSS JOIN LATERAL (
		       SELECT EXISTS (SELECT 1 FROM invoices oi
		                       WHERE oi.payment_id = p.id AND oi.user_id IS DISTINCT FROM $1) AS shared,
		              (SELECT COALESCE(sum(mi.total_amount), 0) FROM invoices mi
		                WHERE mi.payment_id = p.id AND mi.user_id = $1) AS own_amount) a
		 CROSS JOIN LATERAL (SELECT p.user_id IS NOT DISTINCT FROM $1 AND NOT a.shared AS whole) f
		 WHERE p.club_id = $2
		   AND (p.user_id = $1
		        OR p.id IN (SELECT payment_id FROM invoices WHERE user_id = $1 AND club_id = $2))
		 ORDER BY p.created_at, p.id`},
	// An order is the member's when they placed it or when it pays one of
	// their fakturas. guest_* and the pay token are always left out: the
	// first may name whoever paid, the second is a live capability.
	{key: "orders", query: `
		SELECT o.id, o.status, o.purpose, o.provider, o.currency, o.paid_at, o.created_at,
		       NOT f.whole AS allocation_only,
		       CASE WHEN f.whole THEN o.total_amount ELSE a.own_amount END AS total_amount,
		       CASE WHEN f.whole THEN o.vipps_reference END AS vipps_reference,
		       CASE WHEN f.whole THEN
		           (SELECT COALESCE(json_agg(l ORDER BY l.created_at, l.name), '[]'::json)
		              FROM (SELECT ol.name, ol.quantity, ol.unit_price, ol.total_price, ol.created_at
		                      FROM order_lines ol WHERE ol.order_id = o.id) l)
		       ELSE '[]'::json END AS lines,
		       (SELECT COALESCE(json_agg(os.invoice_id ORDER BY os.invoice_id), '[]'::json)
		          FROM order_sources os JOIN invoices i ON i.id = os.invoice_id
		         WHERE os.order_id = o.id AND i.user_id = $1) AS faktura_ids
		  FROM orders o
		 CROSS JOIN LATERAL (
		       SELECT EXISTS (SELECT 1 FROM order_sources os JOIN invoices oi ON oi.id = os.invoice_id
		                       WHERE os.order_id = o.id AND oi.user_id IS DISTINCT FROM $1) AS shared,
		              (SELECT COALESCE(sum(mi.total_amount), 0)
		                 FROM order_sources os JOIN invoices mi ON mi.id = os.invoice_id
		                WHERE os.order_id = o.id AND mi.user_id = $1) AS own_amount) a
		 CROSS JOIN LATERAL (SELECT o.user_id IS NOT DISTINCT FROM $1 AND NOT a.shared AS whole) f
		 WHERE o.club_id = $2
		   AND (o.user_id = $1
		        OR EXISTS (SELECT 1 FROM order_sources os JOIN invoices i ON i.id = os.invoice_id
		                    WHERE os.order_id = o.id AND i.user_id = $1 AND i.club_id = $2))
		 ORDER BY o.created_at, o.id`},
	{key: "consents", query: `
		SELECT id, consent_type, version, granted_at, revoked_at
		  FROM user_consents WHERE user_id = $1 AND club_id = $2 ORDER BY granted_at, id`},
	{key: "notification_preferences", query: `
		SELECT category, enabled FROM notification_preferences
		 WHERE user_id = $1 AND club_id = $2 ORDER BY category`},
	{key: "communication_preferences", query: `
		SELECT category, email_enabled, updated_at FROM communication_preferences
		 WHERE user_id = $1 AND club_id = $2 ORDER BY category`},
	// The endpoint's host names the push service; the rest of the URL and
	// the keys are a capability to push to the device, so they stay out.
	{key: "push_subscriptions", query: `
		SELECT substring(endpoint FROM '^[A-Za-z][A-Za-z0-9+.-]*://([^/:?#]+)') AS endpoint_host,
		       user_agent, created_at
		  FROM push_subscriptions WHERE user_id = $1 AND club_id = $2 ORDER BY created_at, id`},
	{key: "bookings", query: `
		SELECT b.id, r.name AS resource_name, b.start_date, b.end_date, b.status, b.notes,
		       b.boat_length_m, b.boat_beam_m, b.boat_draft_m, b.payment_id, b.created_at
		  FROM bookings b LEFT JOIN resources r ON r.id = b.resource_id
		 WHERE b.user_id = $1 AND b.club_id = $2 ORDER BY b.start_date, b.id`},
	// The mail log names the member on notices sent to the board about them,
	// so the recipient is shown only when it is the member's own address.
	// error and outcome_detail are left out: provider text can quote other
	// addresses.
	{key: "email_sends", query: `
		SELECT es.kind, es.status, es.created_at,
		       CASE WHEN lower(es.recipient) = lower(u.email) THEN es.recipient END AS recipient
		  FROM email_sends es JOIN users u ON u.id = $1 AND u.club_id = $2
		 WHERE es.club_id = $2 AND (es.user_id = u.id OR lower(es.recipient) = lower(u.email))
		 ORDER BY es.created_at, es.id`},
}

// sql wraps the section's query so it returns the section as one JSON value.
func (s exportSection) sql() string {
	if s.single {
		return `SELECT row_to_json(x) FROM (` + s.query + `) x`
	}
	return `SELECT COALESCE(json_agg(x), '[]'::json) FROM (` + s.query + `) x`
}

// collectDataExport reads every section for userID in clubID.
func collectDataExport(ctx context.Context, q dbQuerier, userID, clubID string) (map[string]any, error) {
	export := make(map[string]any, len(dataExportSections)+1)
	for _, s := range dataExportSections {
		var raw []byte
		err := q.QueryRow(ctx, s.sql(), userID, clubID).Scan(&raw)
		if s.single && errors.Is(err, pgx.ErrNoRows) {
			raw = []byte("null")
		} else if err != nil {
			return nil, fmt.Errorf("export section %s: %w", s.key, err)
		}
		export[s.key] = json.RawMessage(raw)
	}
	return export, nil
}

// HandleDataExport answers GET /members/me/data-export with the caller's own
// data (dataExportSections), as one JSON document the portal downloads.
// The sections are read in one read-only snapshot, so a faktura and its
// payment cannot disagree because one was written between two reads.
func (h *GDPRHandler) HandleDataExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	tx, err := h.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin data export")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only; nothing to commit

	export, err := collectDataExport(ctx, tx, claims.UserID, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to collect data export")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	now := time.Now().UTC()
	export["exported_at"] = now.Format(time.RFC3339)

	// The document is the member's personal data: no cache may keep it.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition",
		`attachment; filename="data-export-`+now.Format(time.DateOnly)+`.json"`)
	JSON(w, http.StatusOK, export)
}

func (h *GDPRHandler) HandleRequestDeletion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var existing string
	err := h.db.QueryRow(ctx,
		`SELECT id FROM deletion_requests WHERE user_id = $1 AND status = 'pending'`,
		claims.UserID,
	).Scan(&existing)
	if err == nil {
		Error(w, http.StatusConflict, "a pending deletion request already exists")
		return
	}
	if err != pgx.ErrNoRows {
		h.log.Error().Err(err).Msg("failed to check existing deletion request")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	graceEnd := time.Now().Add(deletionGracePeriod)

	var id string
	var requestedAt time.Time
	err = h.db.QueryRow(ctx,
		`INSERT INTO deletion_requests (user_id, club_id, grace_end, status)
		 VALUES ($1, $2, $3, 'pending')
		 RETURNING id, requested_at`,
		claims.UserID, claims.ClubID, graceEnd,
	).Scan(&id, &requestedAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create deletion request")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, map[string]any{
		"id":           id,
		"status":       "pending",
		"requested_at": requestedAt,
		"grace_end":    graceEnd,
	})
}

func (h *GDPRHandler) HandleCancelDeletion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var id string
	err := h.db.QueryRow(ctx,
		`UPDATE deletion_requests
		 SET cancelled_at = now(), status = 'cancelled'
		 WHERE user_id = $1 AND status = 'pending'
		 RETURNING id`,
		claims.UserID,
	).Scan(&id)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "no pending deletion request found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to cancel deletion request")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"id": id, "status": "cancelled"})
}

func (h *GDPRHandler) HandleGetDeletionStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var id, status string
	var requestedAt, graceEnd time.Time
	var cancelledAt, processedAt *time.Time
	err := h.db.QueryRow(ctx,
		`SELECT id, status, requested_at, grace_end, cancelled_at, processed_at
		 FROM deletion_requests
		 WHERE user_id = $1
		 ORDER BY requested_at DESC
		 LIMIT 1`,
		claims.UserID,
	).Scan(&id, &status, &requestedAt, &graceEnd, &cancelledAt, &processedAt)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "no deletion request found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query deletion status")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"id":           id,
		"status":       status,
		"requested_at": requestedAt,
		"grace_end":    graceEnd,
		"cancelled_at": cancelledAt,
		"processed_at": processedAt,
	})
}

// HandleRecordConsent records the caller's consent to the current version of
// a legal document, or their newsletter opt-in. The version is resolved here
// from the caller's club, never taken from the body: a client could otherwise
// record consent to a version the club never published. The body's version
// is optional and only an expectation: the version the client showed the
// member. When it is not the current one the board has published a new text
// since, so the consent is refused (409) rather than recorded against a text
// the member never saw. A newsletter opt-in has no document, so its version
// is not checked.
func (h *GDPRHandler) HandleRecordConsent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		ConsentType string `json:"consent_type"`
		Version     string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ConsentType == "" {
		Error(w, http.StatusBadRequest, "consent_type is required")
		return
	}
	if !consentTypes[req.ConsentType] {
		Error(w, http.StatusBadRequest, "unknown consent_type")
		return
	}

	version := newsletterConsentVersion
	if legalDocTypes[req.ConsentType] {
		versions, _, err := LatestLegalVersions(ctx, h.db, claims.ClubID, []string{req.ConsentType})
		if err != nil {
			h.log.Error().Err(err).Msg("failed to resolve legal document version")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		v, ok := versions[req.ConsentType]
		if !ok {
			JSON(w, http.StatusConflict, map[string]any{
				"error": "the club has not published this document",
				"code":  codeLegalDocumentUnpublished,
			})
			return
		}
		if req.Version != "" && req.Version != v {
			JSON(w, http.StatusConflict, map[string]any{
				"error":   "the document has a newer version",
				"code":    codeLegalDocumentStale,
				"version": v,
			})
			return
		}
		version = v
	}

	var id string
	var grantedAt time.Time
	err := h.db.QueryRow(ctx,
		`INSERT INTO user_consents (user_id, club_id, consent_type, version)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, granted_at`,
		claims.UserID, claims.ClubID, req.ConsentType, version,
	).Scan(&id, &grantedAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to record consent")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, map[string]any{
		"id":           id,
		"consent_type": req.ConsentType,
		"version":      version,
		"granted_at":   grantedAt,
	})
}

func (h *GDPRHandler) HandleGetMyConsents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT id, consent_type, version, granted_at, revoked_at
		 FROM user_consents
		 WHERE user_id = $1
		 ORDER BY granted_at DESC`,
		claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query consents")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type consent struct {
		ID          string     `json:"id"`
		ConsentType string     `json:"consent_type"`
		Version     string     `json:"version"`
		GrantedAt   time.Time  `json:"granted_at"`
		RevokedAt   *time.Time `json:"revoked_at"`
	}

	consents := make([]consent, 0)
	for rows.Next() {
		var c consent
		if err := rows.Scan(&c.ID, &c.ConsentType, &c.Version, &c.GrantedAt, &c.RevokedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan consent")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		consents = append(consents, c)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating consents")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"consents": consents})
}

// HandleGetLegalDocument serves the deploy's club's latest published version of
// a document. The route is public, so the club comes from the static config
// (one club per deploy), like the other public endpoints.
func (h *GDPRHandler) HandleGetLegalDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	docType := chi.URLParam(r, "docType")
	if !legalDocTypes[docType] {
		Error(w, http.StatusNotFound, "document not found")
		return
	}

	var id, version, content string
	var publishedAt time.Time
	err := h.db.QueryRow(ctx,
		`SELECT id, version, content, published_at
		 FROM legal_documents
		 WHERE club_id = (SELECT id FROM clubs WHERE slug = $2)
		   AND doc_type = $1 AND published_at IS NOT NULL
		 ORDER BY `+latestLegalOrder+`
		 LIMIT 1`,
		docType, h.config.ClubSlug,
	).Scan(&id, &version, &content, &publishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query legal document")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"id":           id,
		"doc_type":     docType,
		"version":      version,
		"content":      content,
		"published_at": publishedAt,
	})
}

// MountAdminRoutes registers the deletion-request queue and the legal-document
// editor under /admin (behind the group's RequireAdminTOTP). GDPR is one
// function held by the site admin by default (docs/adr/board-workflows.md,
// BRD-3), so the caller passes an admin-only roleGate, matching the SPA page.
// Processing a deletion erases or anonymises a member irreversibly, so it
// additionally takes freshGate (a fresh TOTP, BRY-509); reads and the legal
// editor stay behind the 12-hour step-up. The member's own routes (data
// export, delete request, consents) stay under /members/me.
func (h *GDPRHandler) MountAdminRoutes(r chi.Router, roleGate, freshGate func(http.Handler) http.Handler) {
	r.Route("/gdpr", func(r chi.Router) {
		r.Use(roleGate)
		r.Get("/deletion-requests", h.HandleListDeletionRequests)
		r.With(freshGate).Post("/deletion-requests/{requestID}/process", h.HandleProcessDeletion)
		r.Get("/legal", h.HandleAdminListLegalDocuments)
		r.Post("/legal", h.HandleAdminCreateLegalDocument)
	})
}

// deletionRequestListSQL lists a club's pending deletion requests ($1) with
// the requesting member's name and address. A test runs it against the
// migrated schema alongside the data export.
const deletionRequestListSQL = `SELECT dr.id, dr.user_id, COALESCE(u.full_name, ''), u.email,
	       dr.requested_at, dr.grace_end, dr.status
	  FROM deletion_requests dr
	  JOIN users u ON u.id = dr.user_id
	 WHERE dr.club_id = $1 AND dr.status = 'pending'
	 ORDER BY dr.requested_at ASC`

func (h *GDPRHandler) HandleListDeletionRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx, deletionRequestListSQL, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query deletion requests")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type deletionRequest struct {
		ID          string    `json:"id"`
		UserID      string    `json:"user_id"`
		UserName    string    `json:"user_name"`
		UserEmail   string    `json:"user_email"`
		RequestedAt time.Time `json:"requested_at"`
		GraceEnd    time.Time `json:"grace_end"`
		Status      string    `json:"status"`
	}

	requests := make([]deletionRequest, 0)
	for rows.Next() {
		var dr deletionRequest
		if err := rows.Scan(&dr.ID, &dr.UserID, &dr.UserName, &dr.UserEmail, &dr.RequestedAt, &dr.GraceEnd, &dr.Status); err != nil {
			h.log.Error().Err(err).Msg("failed to scan deletion request")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		requests = append(requests, dr)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating deletion requests")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"requests": requests})
}

// revokeMemberAccess ends every way a member can authenticate: it deactivates
// the account (blocking login and the session lookup), drops all sessions, and
// removes passkeys, TOTP enrolment, recovery codes, magic links, the durable
// pay link and developer tokens. It
// is the single step both deletion outcomes (identity erased now, or deferred
// behind retention) run, so neither can leave a usable credential behind.
// Sessions live in Postgres, so deleting them in the caller's transaction is
// what makes the revocation atomic with the deactivation. Must run before the
// identity is anonymised: the magic-link rows are keyed by the old email.
// Returns the number of session rows deleted, which includes expired sessions
// the purge job has not yet removed. It refuses a user that is not a person:
// the club's system actor holds no credentials and is never deactivated. An
// empty actorID records no deactivated_by: a job, not a person, did it.
func revokeMemberAccess(ctx context.Context, tx pgx.Tx, clubID, userID, actorID string) (int64, error) {
	var email string
	if err := tx.QueryRow(ctx,
		`SELECT email FROM users WHERE id = $1 AND club_id = $2 AND kind = 'person'`,
		userID, clubID,
	).Scan(&email); err != nil {
		return 0, fmt.Errorf("read member email: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET deactivated_at = now(), deactivated_by = NULLIF($2::text, '')::uuid, updated_at = now()
		  WHERE id = $1 AND deactivated_at IS NULL`,
		userID, actorID,
	); err != nil {
		return 0, fmt.Errorf("deactivate member: %w", err)
	}

	tag, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	revoked := tag.RowsAffected()

	for _, stmt := range []string{
		`DELETE FROM webauthn_credentials WHERE user_id = $1`,
		`DELETE FROM totp_recovery_codes WHERE user_id = $1`,
		`UPDATE users SET totp_secret_encrypted = NULL, totp_enabled = false WHERE id = $1`,
	} {
		if _, err := tx.Exec(ctx, stmt, userID); err != nil {
			return 0, fmt.Errorf("clear credentials: %w", err)
		}
	}
	// The durable pay link is a standing capability that needs no login, so it
	// must not outlive the member's access. Developer tokens minted by the
	// member would otherwise keep working too.
	if _, err := tx.Exec(ctx, `UPDATE users SET payables_token = NULL WHERE id = $1`, userID); err != nil {
		return 0, fmt.Errorf("revoke tokens: %w", err)
	}
	if err := revokeDeveloperTokens(ctx, tx, clubID, userID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM magic_links WHERE club_id = $1 AND lower(email) = lower($2)`,
		clubID, email,
	); err != nil {
		return 0, fmt.Errorf("clear magic links: %w", err)
	}
	return revoked, nil
}

// HandleProcessDeletion carries out a member's deletion request once its
// cooling-off window has passed. The request row is locked for the whole
// transaction, so two admins processing the same request serialise and the
// second sees it already processed.
func (h *GDPRHandler) HandleProcessDeletion(w http.ResponseWriter, r *http.Request) { //nolint:cyclop // one branch per erasure step in one transaction; split tracked in BRY-424
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	requestID := chi.URLParam(r, "requestID")
	// deletion_requests.id is a UUID; a malformed id would otherwise surface
	// as a cast error from Postgres and answer 500.
	parsedID, err := uuid.Parse(requestID)
	if err != nil {
		Error(w, http.StatusNotFound, "deletion request not found")
		return
	}
	// Use the canonical form: uuid.Parse also accepts urn:uuid: and braced
	// forms that Postgres would reject.
	requestID = parsedID.String()

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Processing deactivates the member, so it can take the club's last active
	// admin; the lock goes before any row lock (see lockClubAdmins).
	if err = lockClubAdmins(ctx, tx, claims.ClubID); err != nil {
		h.log.Error().Err(err).Msg("failed to lock club admins")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	var userID string
	var graceEnd time.Time
	var status string
	err = tx.QueryRow(ctx,
		`SELECT user_id, grace_end, status FROM deletion_requests
		  WHERE id = $1 AND club_id = $2 FOR UPDATE`,
		requestID, claims.ClubID,
	).Scan(&userID, &graceEnd, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "deletion request not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query deletion request")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if status != "pending" {
		Error(w, http.StatusBadRequest, "deletion request is not pending")
		return
	}

	// An admin erasing their own account would lock themselves out mid-request
	// and leave the club without the actor who can undo it.
	if userID == claims.UserID {
		Error(w, http.StatusBadRequest, "cannot process your own deletion request")
		return
	}

	if time.Now().Before(graceEnd) {
		Error(w, http.StatusBadRequest, "grace period has not expired")
		return
	}

	// The club's only active admin cannot be erased: processing deactivates
	// them and nobody could undo it. The request stays pending until another
	// admin exists.
	if err = ensureNotLastActiveAdmin(ctx, tx, claims.ClubID, userID); err != nil {
		if !writeAdminGuard(w, err) {
			h.log.Error().Err(err).Msg("failed to check last admin")
			Error(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	// Retention gate (BRY-208). Data with no bookkeeping obligation is
	// always wiped now. But if the member still has in-window financial
	// records, their name/email must be retained for those records
	// (bokføringsloven §13) — so we defer identity erasure and let the
	// scheduled purge (BRY-207) finish once retention clears. Only when
	// retention is already cleared do we erase identity immediately.
	ret, rerr := accounting.MemberFinancialRetention(ctx, tx, claims.ClubID, userID)
	if rerr != nil {
		h.log.Error().Err(rerr).Msg("failed to evaluate member retention")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Always wipe data carrying no retention obligation.
	recordsWiped := []string{"push_subscriptions", "notification_preferences", "user_consents", "email_sends", "mail_delivery_events", "membership_applications", "membership_cancel_reason"}
	for _, stmt := range []string{
		`DELETE FROM push_subscriptions WHERE user_id = $1`,
		`DELETE FROM notification_preferences WHERE user_id = $1`,
		`DELETE FROM user_consents WHERE user_id = $1`,
	} {
		if _, err = tx.Exec(ctx, stmt, userID); err != nil {
			h.log.Error().Err(err).Str("stmt", stmt).Msg("failed to wipe non-retained user data")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if err = accounting.DeleteMemberMailLog(ctx, tx, userID); err != nil {
		h.log.Error().Err(err).Msg("failed to delete mail log")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err = accounting.EraseMemberMembershipData(ctx, tx, userID); err != nil {
		h.log.Error().Err(err).Msg("failed to erase membership data")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Both outcomes lock the member out; only the identity differs. A deferred
	// member keeps their name until the purge job erases it once ret.ClearsAt
	// passes.
	sessionsRevoked, err := revokeMemberAccess(ctx, tx, claims.ClubID, userID, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to revoke member access")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	identityErased := ret.Eligible
	if identityErased {
		if _, err = tx.Exec(ctx,
			`UPDATE users SET first_name = 'Slettet', last_name = 'bruker',
			    email = gen_random_uuid()::text || '`+accounting.ErasedEmailSuffix+`', phone = '',
			    address_line = '', postal_code = '', city = '', updated_at = now()
			  WHERE id = $1`,
			userID,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to anonymize user")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err = audit.ScrubMemberPII(ctx, tx, userID); err != nil {
			h.log.Error().Err(err).Msg("failed to scrub audit log")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// Audit the outcome, including what was wiped now vs deferred, so the
	// records_wiped trail is populated (see docs/developer/reference/audit-actions.md).
	// resource_id is the erased user, so the request id lives in the details
	// to tie the entry back to the deletion_requests row.
	auditDetails, _ := json.Marshal(map[string]any{
		"request_id":          requestID,
		"records_wiped":       recordsWiped,
		"identity_erased":     identityErased,
		"retention_clears_at": ret.ClearsAt,
		"sessions_revoked":    sessionsRevoked,
	})
	if _, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, actor_ip, action, resource, resource_id, details)
		 VALUES ($1, $2, $3, $4, 'user', $5, $6)`,
		claims.ClubID, claims.UserID, r.RemoteAddr, audit.ActionGDPRDeletionProcessed, userID, auditDetails,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to write audit log")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// The row is locked above, so the status guard cannot lose a race; it
	// keeps the update correct if the lock is ever dropped.
	tag, err := tx.Exec(ctx,
		`UPDATE deletion_requests SET processed_at = now(), status = 'processed'
		  WHERE id = $1 AND status = 'pending'`,
		requestID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update deletion request status")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() != 1 {
		Error(w, http.StatusBadRequest, "deletion request is not pending")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit deletion transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"id":              requestID,
		"status":          "processed",
		"identity_erased": identityErased,
		"deferred_until":  ret.ClearsAt,
	})
}

func (h *GDPRHandler) HandleAdminCreateLegalDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		DocType string `json:"doc_type"`
		Version string `json:"version"`
		Content string `json:"content"`
		Publish bool   `json:"publish"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.DocType == "" || req.Version == "" || req.Content == "" {
		Error(w, http.StatusBadRequest, "doc_type, version, and content are required")
		return
	}
	if !legalDocTypes[req.DocType] {
		Error(w, http.StatusBadRequest, "unknown doc_type")
		return
	}

	var publishedAt *time.Time
	if req.Publish {
		now := time.Now()
		publishedAt = &now
	}

	var id string
	var createdAt time.Time
	err := h.db.QueryRow(ctx,
		`INSERT INTO legal_documents (club_id, doc_type, version, content, published_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, created_at`,
		claims.ClubID, req.DocType, req.Version, req.Content, publishedAt,
	).Scan(&id, &createdAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create legal document")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, map[string]any{
		"id":           id,
		"doc_type":     req.DocType,
		"version":      req.Version,
		"published_at": publishedAt,
		"created_at":   createdAt,
	})
}

func (h *GDPRHandler) HandleAdminListLegalDocuments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT id, doc_type, version, published_at, created_at
		 FROM legal_documents
		 WHERE club_id = $1
		 ORDER BY created_at DESC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query legal documents")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type legalDoc struct {
		ID          string     `json:"id"`
		DocType     string     `json:"doc_type"`
		Version     string     `json:"version"`
		PublishedAt *time.Time `json:"published_at"`
		CreatedAt   time.Time  `json:"created_at"`
	}

	docs := make([]legalDoc, 0)
	for rows.Next() {
		var d legalDoc
		if err := rows.Scan(&d.ID, &d.DocType, &d.Version, &d.PublishedAt, &d.CreatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan legal document")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating legal documents")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"documents": docs})
}
