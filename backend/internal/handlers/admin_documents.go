package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

const maxUploadSize = 50 << 20 // 50 MB

type AdminDocumentsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewAdminDocumentsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *AdminDocumentsHandler {
	return &AdminDocumentsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "admin_documents").Logger(),
	}
}

func (h *AdminDocumentsHandler) HandleListDocuments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	hasBoard := false
	for _, role := range claims.Roles {
		if role == "board" || role == "admin" || role == "harbor_master" || role == "treasurer" {
			hasBoard = true
			break
		}
	}

	var rows pgx.Rows
	var err error

	if hasBoard {
		rows, err = h.db.Query(ctx,
			`SELECT d.id, d.title, d.filename, d.content_type, d.size_bytes,
			        d.visibility, d.created_at, d.updated_at, u.full_name
			 FROM documents d
			 JOIN clubs c ON c.id = d.club_id
			 JOIN users u ON u.id = d.uploaded_by
			 WHERE c.slug = $1
			 ORDER BY d.created_at DESC`,
			claims.ClubID,
		)
	} else {
		rows, err = h.db.Query(ctx,
			`SELECT d.id, d.title, d.filename, d.content_type, d.size_bytes,
			        d.visibility, d.created_at, d.updated_at, u.full_name
			 FROM documents d
			 JOIN clubs c ON c.id = d.club_id
			 JOIN users u ON u.id = d.uploaded_by
			 WHERE c.slug = $1 AND d.visibility = 'member'
			 ORDER BY d.created_at DESC`,
			claims.ClubID,
		)
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query documents")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type docRow struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
		Visibility  string `json:"visibility"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
		UploadedBy  string `json:"uploaded_by"`
	}

	var docs []docRow
	for rows.Next() {
		var d docRow
		if err := rows.Scan(&d.ID, &d.Title, &d.Filename, &d.ContentType, &d.SizeBytes,
			&d.Visibility, &d.CreatedAt, &d.UpdatedAt, &d.UploadedBy); err != nil {
			h.log.Error().Err(err).Msg("failed to scan document row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("document rows iteration error")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if docs == nil {
		docs = []docRow{}
	}

	JSON(w, http.StatusOK, map[string]any{"documents": docs})
}

func (h *AdminDocumentsHandler) HandleGetDocument(w http.ResponseWriter, r *http.Request) {
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

	hasBoard := false
	for _, role := range claims.Roles {
		if role == "board" || role == "admin" || role == "harbor_master" || role == "treasurer" {
			hasBoard = true
			break
		}
	}

	type docDetail struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Filename    string `json:"filename"`
		S3Key       string `json:"s3_key"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
		Visibility  string `json:"visibility"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
		UploadedBy  string `json:"uploaded_by"`
	}

	var d docDetail
	err := h.db.QueryRow(ctx,
		`SELECT d.id, d.title, d.filename, d.s3_key, d.content_type, d.size_bytes,
		        d.visibility, d.created_at, d.updated_at, u.full_name
		 FROM documents d
		 JOIN clubs c ON c.id = d.club_id
		 JOIN users u ON u.id = d.uploaded_by
		 WHERE d.id = $1 AND c.slug = $2`,
		docID, claims.ClubID,
	).Scan(&d.ID, &d.Title, &d.Filename, &d.S3Key, &d.ContentType, &d.SizeBytes,
		&d.Visibility, &d.CreatedAt, &d.UpdatedAt, &d.UploadedBy)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("doc_id", docID).Msg("failed to query document")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if d.Visibility == "board" && !hasBoard {
		Error(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"document": d})
}

func (h *AdminDocumentsHandler) HandleUploadDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		Error(w, http.StatusBadRequest, "file too large or invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		Error(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	title := r.FormValue("title")
	if title == "" {
		title = header.Filename
	}

	visibility := r.FormValue("visibility")
	if visibility != "member" && visibility != "board" {
		visibility = "member"
	}

	var clubID string
	err = h.db.QueryRow(ctx, `SELECT id FROM clubs WHERE slug = $1`, claims.ClubID).Scan(&clubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to resolve club")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	s3Key := fmt.Sprintf("documents/%s/%s", clubID, filepath.Base(header.Filename))

	var docID string
	err = h.db.QueryRow(ctx,
		`INSERT INTO documents (club_id, title, filename, s3_key, content_type, size_bytes, visibility, uploaded_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		clubID, title, header.Filename, s3Key, header.Header.Get("Content-Type"),
		header.Size, visibility, claims.UserID,
	).Scan(&docID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to insert document record")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// TODO: upload file to S3-compatible storage using s3Key
	h.log.Warn().
		Str("doc_id", docID).
		Str("s3_key", s3Key).
		Int64("size", header.Size).
		Msg("S3 upload stub - file metadata saved but file not uploaded")

	h.log.Info().
		Str("doc_id", docID).
		Str("filename", header.Filename).
		Str("visibility", visibility).
		Msg("document uploaded")

	JSON(w, http.StatusCreated, map[string]string{
		"id":     docID,
		"s3_key": s3Key,
	})
}

func (h *AdminDocumentsHandler) HandleDeleteDocument(w http.ResponseWriter, r *http.Request) {
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

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var clubID, title, filename, s3Key string
	err = tx.QueryRow(ctx,
		`SELECT d.club_id, d.title, d.filename, d.s3_key
		 FROM documents d
		 JOIN clubs c ON c.id = d.club_id
		 WHERE d.id = $1 AND c.slug = $2`,
		docID, claims.ClubID,
	).Scan(&clubID, &title, &filename, &s3Key)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query document for deletion")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	oldData, _ := json.Marshal(map[string]any{
		"title":    title,
		"filename": filename,
		"s3_key":   s3Key,
	})
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, user_id, action, entity_type, entity_id, old_data)
		 VALUES ($1, $2, 'delete_document', 'document', $3, $4)`,
		clubID, claims.UserID, docID, oldData,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to write audit log")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	_, err = tx.Exec(ctx, `DELETE FROM documents WHERE id = $1`, docID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete document")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// TODO: delete file from S3 using s3Key
	h.log.Warn().Str("s3_key", s3Key).Msg("S3 delete stub - file not deleted from storage")

	h.log.Info().
		Str("doc_id", docID).
		Str("actor", claims.UserID).
		Msg("document deleted")

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
