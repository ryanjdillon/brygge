package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/ai"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type AIDocumentsHandler struct {
	db     *pgxpool.Pool
	claude *ai.ClaudeClient
	config *config.Config
	log    zerolog.Logger
}

func NewAIDocumentsHandler(db *pgxpool.Pool, claude *ai.ClaudeClient, cfg *config.Config, log zerolog.Logger) *AIDocumentsHandler {
	return &AIDocumentsHandler{
		db:     db,
		claude: claude,
		config: cfg,
		log:    log.With().Str("handler", "ai_documents").Logger(),
	}
}

func (h *AIDocumentsHandler) fetchDocumentComments(w http.ResponseWriter, r *http.Request) (docTitle string, comments []ai.Comment, ok bool) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return "", nil, false
	}

	if h.claude == nil {
		Error(w, http.StatusNotImplemented, "AI features not configured: ANTHROPIC_API_KEY is not set")
		return "", nil, false
	}

	docID := chi.URLParam(r, "docID")
	if docID == "" {
		Error(w, http.StatusBadRequest, "document ID is required")
		return "", nil, false
	}

	err := h.db.QueryRow(ctx,
		`SELECT d.title
		 FROM documents d
		 JOIN clubs c ON c.id = d.club_id
		 WHERE d.id = $1 AND c.slug = $2`,
		docID, claims.ClubID,
	).Scan(&docTitle)
	if err != nil {
		h.log.Error().Err(err).Str("doc_id", docID).Msg("failed to query document")
		Error(w, http.StatusNotFound, "document not found")
		return "", nil, false
	}

	rows, err := h.db.Query(ctx,
		`SELECT u.full_name, dc.body, dc.created_at
		 FROM document_comments dc
		 JOIN users u ON u.id = dc.user_id
		 JOIN documents d ON d.id = dc.document_id
		 JOIN clubs c ON c.id = d.club_id
		 WHERE dc.document_id = $1 AND c.slug = $2
		 ORDER BY dc.created_at ASC`,
		docID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Str("doc_id", docID).Msg("failed to query comments")
		Error(w, http.StatusInternalServerError, "internal error")
		return "", nil, false
	}
	defer rows.Close()

	for rows.Next() {
		var c ai.Comment
		if err := rows.Scan(&c.Author, &c.Body, &c.CreatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan comment row")
			Error(w, http.StatusInternalServerError, "internal error")
			return "", nil, false
		}
		comments = append(comments, c)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("comment rows iteration error")
		Error(w, http.StatusInternalServerError, "internal error")
		return "", nil, false
	}

	if len(comments) == 0 {
		Error(w, http.StatusBadRequest, "no comments found for this document")
		return "", nil, false
	}

	return docTitle, comments, true
}

func (h *AIDocumentsHandler) HandleSummarizeComments(w http.ResponseWriter, r *http.Request) {
	docTitle, comments, ok := h.fetchDocumentComments(w, r)
	if !ok {
		return
	}

	h.log.Info().
		Str("doc_title", docTitle).
		Int("comment_count", len(comments)).
		Msg("GDPR notice: sending document comments to Anthropic API for summarization")

	summary, err := h.claude.SummarizeComments(r.Context(), docTitle, comments)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to summarize comments via AI")
		Error(w, http.StatusBadGateway, "AI service unavailable")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"summary": summary})
}

func (h *AIDocumentsHandler) HandleGenerateAgenda(w http.ResponseWriter, r *http.Request) {
	docTitle, comments, ok := h.fetchDocumentComments(w, r)
	if !ok {
		return
	}

	var body struct {
		ExistingAgenda string `json:"existing_agenda"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}

	h.log.Info().
		Str("doc_title", docTitle).
		Int("comment_count", len(comments)).
		Msg("GDPR notice: sending document comments to Anthropic API for agenda generation")

	agenda, err := h.claude.GenerateAgenda(r.Context(), docTitle, comments, body.ExistingAgenda)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate agenda via AI")
		Error(w, http.StatusBadGateway, "AI service unavailable")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"agenda": agenda})
}
