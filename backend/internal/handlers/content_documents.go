package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/microcosm-cc/bluemonday"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

var htmlPolicy = bluemonday.UGCPolicy()

type ContentDocumentsHandler struct {
	db  *pgxpool.Pool
	log zerolog.Logger
}

func NewContentDocumentsHandler(db *pgxpool.Pool, log zerolog.Logger) *ContentDocumentsHandler {
	return &ContentDocumentsHandler{
		db:  db,
		log: log.With().Str("handler", "content_documents").Logger(),
	}
}

type contentDocRow struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	BodyHTML    string     `json:"body_html"`
	Visibility  string     `json:"visibility"`
	Published   bool       `json:"published"`
	Revision    int        `json:"revision"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	CreatedBy   string     `json:"created_by"`
	UpdatedBy   string     `json:"updated_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (h *ContentDocumentsHandler) HandleAdminList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT cd.id, cd.title, cd.body_html, cd.visibility, cd.published,
		        cd.revision, cd.published_at,
		        COALESCE(cu.full_name, ''), COALESCE(uu.full_name, ''), cd.created_at, cd.updated_at
		 FROM content_documents cd
		 JOIN users cu ON cu.id = cd.created_by
		 JOIN users uu ON uu.id = cd.updated_by
		 WHERE cd.club_id = $1
		 ORDER BY cd.created_at DESC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list content documents")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	docs := []contentDocRow{}
	for rows.Next() {
		var d contentDocRow
		if err := rows.Scan(&d.ID, &d.Title, &d.BodyHTML, &d.Visibility, &d.Published,
			&d.Revision, &d.PublishedAt,
			&d.CreatedBy, &d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan content document")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("content document rows error")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"documents": docs})
}

func (h *ContentDocumentsHandler) HandleAdminCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		Title      string `json:"title"`
		BodyHTML   string `json:"body_html"`
		Visibility string `json:"visibility"`
		Published  bool   `json:"published"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		Error(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.Visibility != "board" && req.Visibility != "member" && req.Visibility != "slip_holder" {
		req.Visibility = "member"
	}

	safe := htmlPolicy.Sanitize(req.BodyHTML)

	var docID string
	err := h.db.QueryRow(ctx,
		`INSERT INTO content_documents
		        (club_id, title, body_html, visibility, published, revision, published_at, created_by, updated_by)
		 VALUES ($1, $2, $3, $4, $5,
		         CASE WHEN $5 THEN 1 ELSE 0 END,
		         CASE WHEN $5 THEN now() ELSE NULL END,
		         $6, $6)
		 RETURNING id`,
		claims.ClubID, req.Title, safe, req.Visibility, req.Published, claims.UserID,
	).Scan(&docID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to insert content document")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info().Str("doc_id", docID).Str("actor", claims.UserID).Msg("content document created")
	JSON(w, http.StatusCreated, map[string]string{"id": docID})
}

func (h *ContentDocumentsHandler) HandleAdminUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	docID := chi.URLParam(r, "docID")
	if docID == "" {
		Error(w, http.StatusBadRequest, "document ID is required")
		return
	}

	var req struct {
		Title      string `json:"title"`
		BodyHTML   string `json:"body_html"`
		Visibility string `json:"visibility"`
		Published  bool   `json:"published"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		Error(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.Visibility != "board" && req.Visibility != "member" && req.Visibility != "slip_holder" {
		req.Visibility = "member"
	}

	safe := htmlPolicy.Sanitize(req.BodyHTML)

	tag, err := h.db.Exec(ctx,
		`UPDATE content_documents
		 SET title      = $3,
		     body_html  = $4,
		     visibility = $5,
		     published  = $6,
		     revision   = CASE
		                    WHEN $6 AND NOT published THEN 1
		                    WHEN $6 AND published     THEN revision + 1
		                    ELSE revision
		                  END,
		     published_at = CASE
		                      WHEN $6 AND published_at IS NULL THEN now()
		                      ELSE published_at
		                    END,
		     updated_by = $7,
		     updated_at = now()
		 WHERE id = $1 AND club_id = $2`,
		docID, claims.ClubID, req.Title, safe, req.Visibility, req.Published, claims.UserID,
	)
	if err != nil {
		h.log.Error().Err(err).Str("doc_id", docID).Msg("failed to update content document")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "document not found")
		return
	}

	h.log.Info().Str("doc_id", docID).Str("actor", claims.UserID).Msg("content document updated")
	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *ContentDocumentsHandler) HandleAdminDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	docID := chi.URLParam(r, "docID")
	if docID == "" {
		Error(w, http.StatusBadRequest, "document ID is required")
		return
	}

	tag, err := h.db.Exec(ctx,
		`DELETE FROM content_documents WHERE id = $1 AND club_id = $2`,
		docID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Str("doc_id", docID).Msg("failed to delete content document")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "document not found")
		return
	}

	h.log.Info().Str("doc_id", docID).Str("actor", claims.UserID).Msg("content document deleted")
	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandlePortalList returns the combined portal documents view: published file
// uploads and published content documents, filtered by the caller's roles.
func (h *ContentDocumentsHandler) HandlePortalList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	visibilities := portalVisibilities(claims.Roles)

	type fileDoc struct {
		ID          string    `json:"id"`
		Kind        string    `json:"kind"`
		Title       string    `json:"title"`
		Filename    string    `json:"filename"`
		ContentType string    `json:"content_type"`
		SizeBytes   int64     `json:"size_bytes"`
		Visibility  string    `json:"visibility"`
		CreatedAt   time.Time `json:"created_at"`
	}

	type contentDoc struct {
		ID          string     `json:"id"`
		Kind        string     `json:"kind"`
		Title       string     `json:"title"`
		BodyHTML    string     `json:"body_html"`
		Visibility  string     `json:"visibility"`
		Revision    int        `json:"revision"`
		PublishedAt *time.Time `json:"published_at,omitempty"`
		CreatedAt   time.Time  `json:"created_at"`
		UpdatedAt   time.Time  `json:"updated_at"`
	}

	fileRows, err := h.db.Query(ctx,
		`SELECT d.id, d.title, d.filename, d.content_type, d.size_bytes, d.visibility, d.created_at
		 FROM documents d
		 WHERE d.club_id = $1 AND d.visibility = ANY($2)
		 ORDER BY d.created_at DESC`,
		claims.ClubID, visibilities,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list portal file documents")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer fileRows.Close()

	files := []fileDoc{}
	for fileRows.Next() {
		var d fileDoc
		d.Kind = "file"
		if err := fileRows.Scan(&d.ID, &d.Title, &d.Filename, &d.ContentType, &d.SizeBytes,
			&d.Visibility, &d.CreatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan file document")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		files = append(files, d)
	}
	if err := fileRows.Err(); err != nil {
		h.log.Error().Err(err).Msg("file document rows error")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	contentRows, err := h.db.Query(ctx,
		`SELECT cd.id, cd.title, cd.body_html, cd.visibility, cd.revision, cd.published_at, cd.created_at, cd.updated_at
		 FROM content_documents cd
		 WHERE cd.club_id = $1 AND cd.published = true AND cd.visibility = ANY($2)
		 ORDER BY cd.created_at DESC`,
		claims.ClubID, visibilities,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list portal content documents")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer contentRows.Close()

	authored := []contentDoc{}
	for contentRows.Next() {
		var d contentDoc
		d.Kind = "authored"
		if err := contentRows.Scan(&d.ID, &d.Title, &d.BodyHTML, &d.Visibility,
			&d.Revision, &d.PublishedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan content document")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		authored = append(authored, d)
	}
	if err := contentRows.Err(); err != nil {
		h.log.Error().Err(err).Msg("content document rows error")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"files":    files,
		"authored": authored,
	})
}

// portalVisibilities returns the set of visibility values the caller can see.
func portalVisibilities(roles []string) []string {
	isBoard := false
	isSlipHolder := false
	for _, r := range roles {
		switch r {
		case "board", "admin", "harbor_master", "treasurer":
			isBoard = true
		case "slip_holder":
			isSlipHolder = true
		}
	}
	vis := []string{"member"}
	if isBoard {
		vis = append(vis, "board")
	}
	if isBoard || isSlipHolder {
		vis = append(vis, "slip_holder")
	}
	return vis
}

func (h *ContentDocumentsHandler) HandlePortalGetContentDoc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	docID := chi.URLParam(r, "docID")
	if docID == "" {
		Error(w, http.StatusBadRequest, "document ID is required")
		return
	}

	visibilities := portalVisibilities(claims.Roles)

	var d contentDocRow
	err := h.db.QueryRow(ctx,
		`SELECT cd.id, cd.title, cd.body_html, cd.visibility, cd.published,
		        cd.revision, cd.published_at,
		        COALESCE(cu.full_name, ''), COALESCE(uu.full_name, ''), cd.created_at, cd.updated_at
		 FROM content_documents cd
		 JOIN users cu ON cu.id = cd.created_by
		 JOIN users uu ON uu.id = cd.updated_by
		 WHERE cd.id = $1 AND cd.club_id = $2 AND cd.published = true AND cd.visibility = ANY($3)`,
		docID, claims.ClubID, visibilities,
	).Scan(&d.ID, &d.Title, &d.BodyHTML, &d.Visibility, &d.Published,
		&d.Revision, &d.PublishedAt,
		&d.CreatedBy, &d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("doc_id", docID).Msg("failed to get portal content document")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"document": d})
}
