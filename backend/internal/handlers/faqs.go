package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// faqGroupKeys is the fixed set of section keys. The public page orders and
// labels sections from this list via i18n chrome; the club only chooses which
// group each question belongs to. Keep in sync with the migration CHECK and
// the frontend group order.
var faqGroupKeys = []string{"newcomers", "members", "deposit", "general"}

func isValidFaqGroup(g string) bool {
	for _, k := range faqGroupKeys {
		if k == g {
			return true
		}
	}
	return false
}

type FaqsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewFaqsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *FaqsHandler {
	return &FaqsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "faqs").Logger(),
	}
}

type faqItem struct {
	ID        string    `json:"id"`
	GroupKey  string    `json:"group_key"`
	Position  int       `json:"position"`
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	LinkURL   *string   `json:"link_url,omitempty"`
	LinkLabel *string   `json:"link_label,omitempty"`
	Published bool      `json:"published"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// orderByGroupSQL sorts rows by the fixed group order then position. Kept as a
// literal ARRAY so a club can't reorder the sections themselves.
const orderByGroupSQL = `ORDER BY array_position(ARRAY['newcomers','members','deposit','general']::text[], group_key), position, created_at`

// HandleListPublic returns the published FAQ items for the deploy's club,
// ordered by group then position. Club is resolved from the static config
// (single club per deploy), matching the other public endpoints.
func (h *FaqsHandler) HandleListPublic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := h.db.Query(ctx,
		`SELECT id, group_key, position, question, answer, link_url, link_label, published, created_at, updated_at
		 FROM faqs
		 WHERE club_id = (SELECT id FROM clubs WHERE slug = $1) AND published = true
		 `+orderByGroupSQL,
		h.config.ClubSlug,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list public faqs")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items, err := scanFaqItems(rows)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to scan public faqs")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"items": items})
}

// HandleAdminList returns every FAQ item for the caller's club (published and
// drafts), for the board editor.
func (h *FaqsHandler) HandleAdminList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT id, group_key, position, question, answer, link_url, link_label, published, created_at, updated_at
		 FROM faqs
		 WHERE club_id = $1
		 `+orderByGroupSQL,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list faqs")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items, err := scanFaqItems(rows)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to scan faqs")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"items": items})
}

type faqWriteReq struct {
	GroupKey  string  `json:"group_key"`
	Question  string  `json:"question"`
	Answer    string  `json:"answer"`
	LinkURL   *string `json:"link_url"`
	LinkLabel *string `json:"link_label"`
	Published bool    `json:"published"`
}

// normalize trims text fields and blanks out an empty link pair.
func (req *faqWriteReq) normalize() {
	req.GroupKey = strings.TrimSpace(req.GroupKey)
	req.Question = strings.TrimSpace(req.Question)
	req.Answer = strings.TrimSpace(req.Answer)
	req.LinkURL = trimOptional(req.LinkURL)
	req.LinkLabel = trimOptional(req.LinkLabel)
}

func trimOptional(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func (h *FaqsHandler) HandleAdminCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req faqWriteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.normalize()
	if !isValidFaqGroup(req.GroupKey) {
		Error(w, http.StatusBadRequest, "invalid group")
		return
	}
	if req.Question == "" || req.Answer == "" {
		Error(w, http.StatusBadRequest, "question and answer are required")
		return
	}

	var id string
	err := h.db.QueryRow(ctx,
		`INSERT INTO faqs (club_id, group_key, position, question, answer, link_url, link_label, published)
		 VALUES ($1, $2,
		         COALESCE((SELECT MAX(position) + 1 FROM faqs WHERE club_id = $1 AND group_key = $2), 0),
		         $3, $4, $5, $6, $7)
		 RETURNING id`,
		claims.ClubID, req.GroupKey, req.Question, req.Answer, req.LinkURL, req.LinkLabel, req.Published,
	).Scan(&id)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to insert faq")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info().Str("faq_id", id).Str("actor", claims.UserID).Msg("faq created")
	JSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *FaqsHandler) HandleAdminUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	faqID := chi.URLParam(r, "faqID")
	if faqID == "" {
		Error(w, http.StatusBadRequest, "faq ID is required")
		return
	}

	var req faqWriteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.normalize()
	if !isValidFaqGroup(req.GroupKey) {
		Error(w, http.StatusBadRequest, "invalid group")
		return
	}
	if req.Question == "" || req.Answer == "" {
		Error(w, http.StatusBadRequest, "question and answer are required")
		return
	}

	tag, err := h.db.Exec(ctx,
		`UPDATE faqs
		 SET group_key  = $3,
		     question   = $4,
		     answer     = $5,
		     link_url   = $6,
		     link_label = $7,
		     published  = $8,
		     updated_at = now()
		 WHERE id = $1 AND club_id = $2`,
		faqID, claims.ClubID, req.GroupKey, req.Question, req.Answer, req.LinkURL, req.LinkLabel, req.Published,
	)
	if err != nil {
		h.log.Error().Err(err).Str("faq_id", faqID).Msg("failed to update faq")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "faq not found")
		return
	}

	h.log.Info().Str("faq_id", faqID).Str("actor", claims.UserID).Msg("faq updated")
	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *FaqsHandler) HandleAdminDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	faqID := chi.URLParam(r, "faqID")
	if faqID == "" {
		Error(w, http.StatusBadRequest, "faq ID is required")
		return
	}

	tag, err := h.db.Exec(ctx,
		`DELETE FROM faqs WHERE id = $1 AND club_id = $2`,
		faqID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Str("faq_id", faqID).Msg("failed to delete faq")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "faq not found")
		return
	}

	h.log.Info().Str("faq_id", faqID).Str("actor", claims.UserID).Msg("faq deleted")
	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleReorder sets position = list index for each id within a group. Ids not
// belonging to the caller's club are silently ignored (scoped UPDATE).
func (h *FaqsHandler) HandleReorder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		GroupKey string   `json:"group_key"`
		IDs      []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.GroupKey = strings.TrimSpace(req.GroupKey)
	if !isValidFaqGroup(req.GroupKey) {
		Error(w, http.StatusBadRequest, "invalid group")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin faq reorder tx")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	for i, id := range req.IDs {
		if _, err := tx.Exec(ctx,
			`UPDATE faqs SET position = $4, updated_at = now()
			 WHERE id = $1 AND club_id = $2 AND group_key = $3`,
			id, claims.ClubID, req.GroupKey, i,
		); err != nil {
			h.log.Error().Err(err).Str("faq_id", id).Msg("failed to reorder faq")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit faq reorder")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "reordered"})
}

// scanFaqItems drains a faqs query into a slice; callers own the rows.
func scanFaqItems(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]faqItem, error) {
	items := []faqItem{}
	for rows.Next() {
		var it faqItem
		if err := rows.Scan(&it.ID, &it.GroupKey, &it.Position, &it.Question, &it.Answer,
			&it.LinkURL, &it.LinkLabel, &it.Published, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
