package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// deletionGracePeriod is the cooling-off window between a member's deletion
// request and the earliest moment an admin may process it. GDPR Art. 12(3)
// gives the club one month from the request to act, so the work item belongs
// from grace_end and is overdue one calendar month after requested_at.
const deletionGracePeriod = 14 * 24 * time.Hour

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

func (h *GDPRHandler) HandleDataExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	export := map[string]any{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
	}

	var name, email *string
	var phone *string
	err := h.db.QueryRow(ctx,
		`SELECT name, email, phone FROM users WHERE id = $1`,
		claims.UserID,
	).Scan(&name, &email, &phone)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query user profile")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	export["profile"] = map[string]any{"name": name, "email": email, "phone": phone}

	boats, err := h.collectRows(ctx,
		`SELECT id, name, make, model, year, length_ft, registration_number FROM boats WHERE owner_id = $1`,
		claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query boats")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	export["boats"] = boats

	bookings, err := h.collectRows(ctx,
		`SELECT id, resource_id, start_time, end_time, status, created_at FROM bookings WHERE user_id = $1`,
		claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query bookings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	export["bookings"] = bookings

	waitingList, err := h.collectRows(ctx,
		`SELECT id, status, position, created_at FROM waiting_list_entries WHERE user_id = $1`,
		claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query waiting list")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	export["waiting_list"] = waitingList

	payments, err := h.collectRows(ctx,
		`SELECT id, amount, currency, status, description, created_at FROM payments WHERE user_id = $1`,
		claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query payments")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	export["payments"] = payments

	consents, err := h.collectRows(ctx,
		`SELECT id, consent_type, version, granted_at, revoked_at FROM user_consents WHERE user_id = $1`,
		claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query consents")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	export["consents"] = consents

	w.Header().Set("Content-Disposition", "attachment; filename=data-export.json")
	JSON(w, http.StatusOK, export)
}

func (h *GDPRHandler) collectRows(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	descs := rows.FieldDescriptions()
	var result []map[string]any

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(descs))
		for i, desc := range descs {
			row[string(desc.Name)] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if result == nil {
		result = []map[string]any{}
	}
	return result, nil
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

	if req.ConsentType == "" || req.Version == "" {
		Error(w, http.StatusBadRequest, "consent_type and version are required")
		return
	}

	var id string
	var grantedAt time.Time
	err := h.db.QueryRow(ctx,
		`INSERT INTO user_consents (user_id, club_id, consent_type, version)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, granted_at`,
		claims.UserID, claims.ClubID, req.ConsentType, req.Version,
	).Scan(&id, &grantedAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to record consent")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, map[string]any{
		"id":           id,
		"consent_type": req.ConsentType,
		"version":      req.Version,
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

func (h *GDPRHandler) HandleGetLegalDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	docType := chi.URLParam(r, "docType")

	var id, version, content string
	var publishedAt time.Time
	err := h.db.QueryRow(ctx,
		`SELECT id, version, content, published_at
		 FROM legal_documents
		 WHERE doc_type = $1 AND published_at IS NOT NULL
		 ORDER BY published_at DESC
		 LIMIT 1`,
		docType,
	).Scan(&id, &version, &content, &publishedAt)
	if err == pgx.ErrNoRows {
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
// The member's own routes (data export, delete request, consents) stay under
// /members/me.
func (h *GDPRHandler) MountAdminRoutes(r chi.Router, roleGate func(http.Handler) http.Handler) {
	r.Route("/gdpr", func(r chi.Router) {
		r.Use(roleGate)
		r.Get("/deletion-requests", h.HandleListDeletionRequests)
		r.Post("/deletion-requests/{requestID}/process", h.HandleProcessDeletion)
		r.Get("/legal", h.HandleAdminListLegalDocuments)
		r.Post("/legal", h.HandleAdminCreateLegalDocument)
	})
}

func (h *GDPRHandler) HandleListDeletionRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT dr.id, dr.user_id, u.name, u.email, dr.requested_at, dr.grace_end, dr.status
		 FROM deletion_requests dr
		 JOIN users u ON u.id = dr.user_id
		 WHERE dr.club_id = $1 AND dr.status = 'pending'
		 ORDER BY dr.requested_at ASC`,
		claims.ClubID,
	)
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

func (h *GDPRHandler) HandleProcessDeletion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	requestID := chi.URLParam(r, "requestID")

	var userID string
	var graceEnd time.Time
	var status string
	err := h.db.QueryRow(ctx,
		`SELECT user_id, grace_end, status FROM deletion_requests WHERE id = $1 AND club_id = $2`,
		requestID, claims.ClubID,
	).Scan(&userID, &graceEnd, &status)
	if err == pgx.ErrNoRows {
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

	if time.Now().Before(graceEnd) {
		Error(w, http.StatusBadRequest, "grace period has not expired")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	// Retention gate (BRY-208). Data with no bookkeeping obligation is
	// always wiped now. But if the member still has in-window financial
	// records, their name/email must be retained for those records
	// (bokføringsloven §13) — so we defer identity erasure by deactivating
	// them (BRY-203) and let the scheduled purge (BRY-207) finish once
	// retention clears. Only when retention is already cleared do we erase
	// identity immediately.
	ret, rerr := accounting.MemberFinancialRetention(ctx, tx, claims.ClubID, userID)
	if rerr != nil {
		h.log.Error().Err(rerr).Msg("failed to evaluate member retention")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Always wipe data carrying no retention obligation.
	recordsWiped := []string{"push_subscriptions", "notification_preferences", "user_consents"}
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

	identityErased := ret.Eligible
	if identityErased {
		if _, err = tx.Exec(ctx,
			`UPDATE users SET first_name = 'Slettet', last_name = 'bruker',
			    email = gen_random_uuid()::text || '@deleted.local', phone = '',
			    address_line = '', postal_code = '', city = '', updated_at = now()
			  WHERE id = $1`,
			userID,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to anonymize user")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	} else {
		// Defer identity erasure: deactivate so they're hidden and blocked
		// from login, and drop their sessions. The purge job erases the
		// remaining PII once ret.ClearsAt passes.
		if _, err = tx.Exec(ctx,
			`UPDATE users SET deactivated_at = now(), deactivated_by = $2, updated_at = now()
			  WHERE id = $1 AND deactivated_at IS NULL`,
			userID, claims.UserID,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to deactivate user for deferred erasure")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
			h.log.Error().Err(err).Msg("failed to clear sessions")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// Audit the outcome, including what was wiped now vs deferred, so the
	// records_wiped trail is populated (see docs/developer/reference/audit-actions.md).
	auditDetails, _ := json.Marshal(map[string]any{
		"records_wiped":       recordsWiped,
		"identity_erased":     identityErased,
		"retention_clears_at": ret.ClearsAt,
	})
	if _, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'gdpr.deletion_processed', 'user', $3, $4)`,
		claims.ClubID, claims.UserID, userID, auditDetails,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to write audit log")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, err = tx.Exec(ctx,
		`UPDATE deletion_requests SET processed_at = now(), status = 'processed' WHERE id = $1`,
		requestID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update deletion request status")
		Error(w, http.StatusInternalServerError, "internal error")
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
