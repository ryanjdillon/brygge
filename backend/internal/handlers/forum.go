package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type ForumHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
	client *http.Client
}

func NewForumHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *ForumHandler {
	return &ForumHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "forum").Logger(),
		client: &http.Client{},
	}
}

func (h *ForumHandler) dendriteURL(path string) string {
	return fmt.Sprintf("%s%s", h.config.DendriteInternalURL, path)
}

type forumRoom struct {
	ID          string `json:"id"`
	Alias       string `json:"alias"`
	Name        string `json:"name"`
	Topic       string `json:"topic"`
	MemberCount int    `json:"memberCount"`
}

type forumMessage struct {
	ID         string `json:"id"`
	SenderName string `json:"senderName"`
	Content    string `json:"content"`
	Timestamp  string `json:"timestamp"`
}

type messagesResponse struct {
	Messages []forumMessage `json:"messages"`
	End      string         `json:"end"`
}

type sendMessageRequest struct {
	Content string `json:"content"`
}

// HandleListRooms returns the club's forum rooms from the matrix_rooms table.
func (h *ForumHandler) HandleListRooms(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT matrix_room_id, alias, name, topic, member_count
		 FROM matrix_rooms
		 WHERE club_id = $1
		 ORDER BY sort_order ASC, name ASC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query matrix rooms")
		Error(w, http.StatusInternalServerError, "failed to fetch rooms")
		return
	}
	defer rows.Close()

	rooms := make([]forumRoom, 0)
	for rows.Next() {
		var room forumRoom
		if err := rows.Scan(&room.ID, &room.Alias, &room.Name, &room.Topic, &room.MemberCount); err != nil {
			h.log.Error().Err(err).Msg("failed to scan room row")
			continue
		}
		rooms = append(rooms, room)
	}

	JSON(w, http.StatusOK, rooms)
}

// HandleGetRoomMessages proxies a room messages request to Dendrite.
func (h *ForumHandler) HandleGetRoomMessages(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	roomID := chi.URLParam(r, "roomID")
	if roomID == "" {
		Error(w, http.StatusBadRequest, "room ID is required")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	dendriteURL := h.dendriteURL(fmt.Sprintf(
		"/_matrix/client/v3/rooms/%s/messages?dir=b&limit=%d",
		roomID, limit,
	))

	before := r.URL.Query().Get("before")
	if before != "" {
		dendriteURL += "&from=" + before
	}

	token, err := h.getServiceToken(r)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get service token")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, dendriteURL, nil)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to create request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := h.client.Do(req)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to proxy to Dendrite")
		Error(w, http.StatusBadGateway, "messaging service unavailable")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to read response")
		return
	}

	if resp.StatusCode != http.StatusOK {
		h.log.Warn().Int("status", resp.StatusCode).Msg("Dendrite returned error")
		Error(w, http.StatusBadGateway, "messaging service error")
		return
	}

	var matrixResp struct {
		Chunk []struct {
			EventID        string `json:"event_id"`
			Sender         string `json:"sender"`
			OriginServerTS int64  `json:"origin_server_ts"`
			Content        struct {
				MsgType string `json:"msgtype"`
				Body    string `json:"body"`
			} `json:"content"`
			Type string `json:"type"`
		} `json:"chunk"`
		End string `json:"end"`
	}

	if err := json.Unmarshal(body, &matrixResp); err != nil {
		h.log.Error().Err(err).Msg("failed to parse Dendrite response")
		Error(w, http.StatusInternalServerError, "failed to parse messages")
		return
	}

	messages := make([]forumMessage, 0, len(matrixResp.Chunk))
	for _, event := range matrixResp.Chunk {
		if event.Type != "m.room.message" {
			continue
		}
		messages = append(messages, forumMessage{
			ID:         event.EventID,
			SenderName: extractLocalpart(event.Sender),
			Content:    event.Content.Body,
			Timestamp:  fmt.Sprintf("%d", event.OriginServerTS),
		})
	}

	// Reverse so oldest-first for display
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	JSON(w, http.StatusOK, messagesResponse{
		Messages: messages,
		End:      matrixResp.End,
	})
}

// HandleSendMessage sends a message to Dendrite on behalf of the user.
func (h *ForumHandler) HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	roomID := chi.URLParam(r, "roomID")
	if roomID == "" {
		Error(w, http.StatusBadRequest, "room ID is required")
		return
	}

	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Content == "" {
		Error(w, http.StatusBadRequest, "content is required")
		return
	}

	token, err := h.getServiceToken(r)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get service token")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	matrixBody, _ := json.Marshal(map[string]string{
		"msgtype": "m.text",
		"body":    fmt.Sprintf("[%s] %s", extractDisplayName(claims.UserID), req.Content),
	})

	txnID := fmt.Sprintf("brygge-%s-%d", claims.UserID, r.Context().Value(nil))
	dendriteURL := h.dendriteURL(fmt.Sprintf(
		"/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		roomID, txnID,
	))

	dendriteReq, err := http.NewRequestWithContext(r.Context(), http.MethodPut, dendriteURL, bytes.NewReader(matrixBody))
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to create request")
		return
	}
	dendriteReq.Header.Set("Authorization", "Bearer "+token)
	dendriteReq.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(dendriteReq)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to send message to Dendrite")
		Error(w, http.StatusBadGateway, "messaging service unavailable")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		h.log.Warn().Int("status", resp.StatusCode).Str("body", string(body)).Msg("Dendrite send error")
		Error(w, http.StatusBadGateway, "failed to send message")
		return
	}

	var sendResp struct {
		EventID string `json:"event_id"`
	}
	json.NewDecoder(resp.Body).Decode(&sendResp)

	JSON(w, http.StatusOK, map[string]string{"id": sendResp.EventID})
}

// HandleGetRoomMembers returns room member list.
func (h *ForumHandler) HandleGetRoomMembers(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	roomID := chi.URLParam(r, "roomID")
	if roomID == "" {
		Error(w, http.StatusBadRequest, "room ID is required")
		return
	}

	token, err := h.getServiceToken(r)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get service token")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	dendriteURL := h.dendriteURL(fmt.Sprintf(
		"/_matrix/client/v3/rooms/%s/joined_members",
		roomID,
	))

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, dendriteURL, nil)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to create request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := h.client.Do(req)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to proxy members request")
		Error(w, http.StatusBadGateway, "messaging service unavailable")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to read response")
		return
	}

	if resp.StatusCode != http.StatusOK {
		Error(w, http.StatusBadGateway, "messaging service error")
		return
	}

	var matrixResp struct {
		Joined map[string]struct {
			DisplayName string `json:"display_name"`
			AvatarURL   string `json:"avatar_url"`
		} `json:"joined"`
	}

	if err := json.Unmarshal(body, &matrixResp); err != nil {
		Error(w, http.StatusInternalServerError, "failed to parse members")
		return
	}

	type member struct {
		UserID      string `json:"userId"`
		DisplayName string `json:"displayName"`
	}

	members := make([]member, 0, len(matrixResp.Joined))
	for userID, info := range matrixResp.Joined {
		name := info.DisplayName
		if name == "" {
			name = extractLocalpart(userID)
		}
		members = append(members, member{
			UserID:      userID,
			DisplayName: name,
		})
	}

	JSON(w, http.StatusOK, members)
}

// getServiceToken returns the Dendrite service account token used for proxying.
func (h *ForumHandler) getServiceToken(r *http.Request) (string, error) {
	return h.config.DendriteServiceToken, nil
}

// extractLocalpart extracts "alice" from "@alice:example.com".
func extractLocalpart(matrixID string) string {
	if len(matrixID) < 2 {
		return matrixID
	}
	id := matrixID[1:]
	for i, ch := range id {
		if ch == ':' {
			return id[:i]
		}
	}
	return id
}

// extractDisplayName returns a display-friendly name from a user ID.
func extractDisplayName(userID string) string {
	return extractLocalpart(userID)
}
