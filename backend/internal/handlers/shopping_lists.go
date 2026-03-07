package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type ShoppingListsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewShoppingListsHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	log zerolog.Logger,
) *ShoppingListsHandler {
	return &ShoppingListsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "shopping_lists").Logger(),
	}
}

type shoppingList struct {
	ID          string    `json:"id"`
	ClubID      string    `json:"club_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedBy   string    `json:"created_by"`
	SharedWith  *string   `json:"shared_with"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ItemCount   int       `json:"item_count"`
}

type shoppingListItem struct {
	ID        string   `json:"id"`
	ListID    string   `json:"list_id"`
	TaskID    *string  `json:"task_id"`
	Item      string   `json:"item"`
	Quantity  *float64 `json:"quantity"`
	Unit      string   `json:"unit"`
	EstCost   *float64 `json:"est_cost"`
	Checked   bool     `json:"checked"`
	SortOrder int      `json:"sort_order"`
	TaskTitle *string  `json:"task_title"`
}

type createShoppingListRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type updateShoppingListRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	SharedWith  *string `json:"shared_with,omitempty"`
	Status      *string `json:"status,omitempty"`
}

type addItemRequest struct {
	Item     string   `json:"item"`
	Quantity *float64 `json:"quantity"`
	Unit     string   `json:"unit"`
	EstCost  *float64 `json:"est_cost"`
	TaskID   *string  `json:"task_id"`
}

type fromTasksRequest struct {
	TaskIDs    []string `json:"task_ids"`
	ProjectID  *string  `json:"project_id"`
	EventID    *string  `json:"event_id"`
}

func (h *ShoppingListsHandler) HandleListShoppingLists(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT sl.id, sl.club_id, sl.title, sl.description, sl.created_by,
		        sl.shared_with, sl.status, sl.created_at, sl.updated_at,
		        (SELECT COUNT(*) FROM shopping_list_items WHERE list_id = sl.id)
		 FROM shopping_lists sl
		 WHERE sl.club_id = $1
		 ORDER BY sl.created_at DESC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list shopping lists")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	lists := make([]shoppingList, 0)
	for rows.Next() {
		var l shoppingList
		if err := rows.Scan(
			&l.ID, &l.ClubID, &l.Title, &l.Description, &l.CreatedBy,
			&l.SharedWith, &l.Status, &l.CreatedAt, &l.UpdatedAt, &l.ItemCount,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan shopping list")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		lists = append(lists, l)
	}

	JSON(w, http.StatusOK, lists)
}

func (h *ShoppingListsHandler) HandleCreateShoppingList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createShoppingListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		Error(w, http.StatusBadRequest, "title is required")
		return
	}

	var l shoppingList
	err := h.db.QueryRow(ctx,
		`INSERT INTO shopping_lists (club_id, title, description, created_by)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, club_id, title, description, created_by, shared_with, status, created_at, updated_at`,
		claims.ClubID, req.Title, req.Description, claims.UserID,
	).Scan(&l.ID, &l.ClubID, &l.Title, &l.Description, &l.CreatedBy,
		&l.SharedWith, &l.Status, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create shopping list")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, l)
}

func (h *ShoppingListsHandler) HandleGetShoppingList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	listID := chi.URLParam(r, "listID")

	var l shoppingList
	err := h.db.QueryRow(ctx,
		`SELECT sl.id, sl.club_id, sl.title, sl.description, sl.created_by,
		        sl.shared_with, sl.status, sl.created_at, sl.updated_at,
		        (SELECT COUNT(*) FROM shopping_list_items WHERE list_id = sl.id)
		 FROM shopping_lists sl
		 WHERE sl.id = $1 AND sl.club_id = $2`,
		listID, claims.ClubID,
	).Scan(&l.ID, &l.ClubID, &l.Title, &l.Description, &l.CreatedBy,
		&l.SharedWith, &l.Status, &l.CreatedAt, &l.UpdatedAt, &l.ItemCount)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "shopping list not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get shopping list")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, l)
}

func (h *ShoppingListsHandler) HandleUpdateShoppingList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	listID := chi.URLParam(r, "listID")

	var req updateShoppingListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var l shoppingList
	err := h.db.QueryRow(ctx,
		`UPDATE shopping_lists SET
			title = COALESCE($3, title),
			description = COALESCE($4, description),
			shared_with = CASE WHEN $5::text = '' THEN NULL ELSE COALESCE($5::uuid, shared_with) END,
			status = COALESCE($6, status),
			updated_at = now()
		 WHERE id = $1 AND club_id = $2
		 RETURNING id, club_id, title, description, created_by, shared_with, status, created_at, updated_at`,
		listID, claims.ClubID, req.Title, req.Description, req.SharedWith, req.Status,
	).Scan(&l.ID, &l.ClubID, &l.Title, &l.Description, &l.CreatedBy,
		&l.SharedWith, &l.Status, &l.CreatedAt, &l.UpdatedAt)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "shopping list not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update shopping list")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, l)
}

func (h *ShoppingListsHandler) HandleDeleteShoppingList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	listID := chi.URLParam(r, "listID")

	tag, err := h.db.Exec(ctx,
		`DELETE FROM shopping_lists WHERE id = $1 AND club_id = $2`,
		listID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete shopping list")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "shopping list not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *ShoppingListsHandler) HandleListItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	listID := chi.URLParam(r, "listID")

	rows, err := h.db.Query(ctx,
		`SELECT sli.id, sli.list_id, sli.task_id, sli.item, sli.quantity,
		        sli.unit, sli.est_cost, sli.checked, sli.sort_order,
		        t.title
		 FROM shopping_list_items sli
		 LEFT JOIN tasks t ON t.id = sli.task_id
		 JOIN shopping_lists sl ON sl.id = sli.list_id
		 WHERE sli.list_id = $1 AND sl.club_id = $2
		 ORDER BY sli.sort_order, sli.item`,
		listID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list items")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	items := make([]shoppingListItem, 0)
	for rows.Next() {
		var i shoppingListItem
		if err := rows.Scan(
			&i.ID, &i.ListID, &i.TaskID, &i.Item, &i.Quantity,
			&i.Unit, &i.EstCost, &i.Checked, &i.SortOrder, &i.TaskTitle,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan item")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		items = append(items, i)
	}

	JSON(w, http.StatusOK, items)
}

func (h *ShoppingListsHandler) HandleAddItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	listID := chi.URLParam(r, "listID")

	var req addItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Item == "" {
		Error(w, http.StatusBadRequest, "item is required")
		return
	}

	var i shoppingListItem
	err := h.db.QueryRow(ctx,
		`INSERT INTO shopping_list_items (list_id, task_id, item, quantity, unit, est_cost)
		 SELECT $1, $3, $4, $5, $6, $7
		 FROM shopping_lists WHERE id = $1 AND club_id = $2
		 RETURNING id, list_id, task_id, item, quantity, unit, est_cost, checked, sort_order`,
		listID, claims.ClubID, req.TaskID, req.Item, req.Quantity, req.Unit, req.EstCost,
	).Scan(&i.ID, &i.ListID, &i.TaskID, &i.Item, &i.Quantity, &i.Unit, &i.EstCost, &i.Checked, &i.SortOrder)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to add item")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusCreated, i)
}

func (h *ShoppingListsHandler) HandleToggleItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	itemID := chi.URLParam(r, "itemID")

	var checked bool
	err := h.db.QueryRow(ctx,
		`UPDATE shopping_list_items sli
		 SET checked = NOT sli.checked
		 FROM shopping_lists sl
		 WHERE sli.id = $1 AND sli.list_id = sl.id AND sl.club_id = $2
		 RETURNING sli.checked`,
		itemID, claims.ClubID,
	).Scan(&checked)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to toggle item")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"checked": checked})
}

func (h *ShoppingListsHandler) HandleDeleteItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	itemID := chi.URLParam(r, "itemID")

	tag, err := h.db.Exec(ctx,
		`DELETE FROM shopping_list_items sli
		 USING shopping_lists sl
		 WHERE sli.id = $1 AND sli.list_id = sl.id AND sl.club_id = $2`,
		itemID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to delete item")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "item not found")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandlePopulateFromTasks adds task materials to a shopping list.
func (h *ShoppingListsHandler) HandlePopulateFromTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	listID := chi.URLParam(r, "listID")

	var req fromTasksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	query := `
		SELECT t.id, m->>'item', (m->>'quantity')::numeric, m->>'unit', (m->>'est_cost')::numeric
		FROM tasks t, jsonb_array_elements(t.materials) AS m
		WHERE t.club_id = $1 AND jsonb_array_length(t.materials) > 0`

	var args []any
	args = append(args, claims.ClubID)

	if len(req.TaskIDs) > 0 {
		query += ` AND t.id = ANY($2)`
		args = append(args, req.TaskIDs)
	} else if req.ProjectID != nil {
		query += ` AND t.project_id = $2`
		args = append(args, *req.ProjectID)
	} else if req.EventID != nil {
		query += ` AND t.project_id IN (SELECT project_id FROM project_events WHERE event_id = $2)`
		args = append(args, *req.EventID)
	} else {
		Error(w, http.StatusBadRequest, "provide task_ids, project_id, or event_id")
		return
	}

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query task materials")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var taskID, item string
		var quantity, estCost *float64
		var unit string
		if err := rows.Scan(&taskID, &item, &quantity, &unit, &estCost); err != nil {
			h.log.Error().Err(err).Msg("failed to scan material")
			continue
		}
		_, err := h.db.Exec(ctx,
			`INSERT INTO shopping_list_items (list_id, task_id, item, quantity, unit, est_cost)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			listID, taskID, item, quantity, unit, estCost,
		)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to insert material item")
			continue
		}
		count++
	}

	JSON(w, http.StatusOK, map[string]int{"items_added": count})
}
