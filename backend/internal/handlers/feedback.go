package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

// b64Enc accepts padded base64; RawStdEncoding used as fallback when canvas omits padding.
var b64Enc = base64.StdEncoding

const (
	linearLabelBug     = "4ebcd87f-49b5-42e7-96f2-2ea50a0ddf2d"
	linearLabelFeature = "5b21a454-f831-4b28-9c44-13c42e2123de"
	linearLabelKBL     = "4d2f6d8e-857c-4d66-b7f8-b2dd0c54974a"
	linearGraphQLURL   = "https://api.linear.app/graphql"
)

type FeedbackHandler struct {
	db  *pgxpool.Pool
	log zerolog.Logger
}

func NewFeedbackHandler(db *pgxpool.Pool, log zerolog.Logger) *FeedbackHandler {
	return &FeedbackHandler{
		db:  db,
		log: log.With().Str("handler", "feedback").Logger(),
	}
}

func (h *FeedbackHandler) HandleSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var enabled bool
	var apiKey, teamID, triageID string
	if err := h.db.QueryRow(ctx,
		`SELECT feature_feedback, feedback_linear_api_key,
		        feedback_linear_team_id, feedback_linear_triage_id
		 FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&enabled, &apiKey, &teamID, &triageID); err != nil {
		h.log.Error().Err(err).Msg("failed to load feedback config")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !enabled || apiKey == "" || teamID == "" || triageID == "" {
		Error(w, http.StatusServiceUnavailable, "feedback not configured")
		return
	}

	var req struct {
		Type        string `json:"type"`
		Title       string `json:"title"`
		Description string `json:"description"`
		PageURL     string `json:"page_url"`
		Screenshot  string `json:"screenshot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" {
		Error(w, http.StatusBadRequest, "description is required")
		return
	}

	if req.Type != "bug" && req.Type != "feature" {
		req.Type = "feature"
	}

	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		if len(req.Description) > 60 {
			req.Title = req.Description[:57] + "..."
		} else {
			req.Title = req.Description
		}
	}

	var submitterName, submitterEmail string
	_ = h.db.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(full_name, ''), first_name || ' ' || last_name, email), email
		 FROM users WHERE id = $1`,
		claims.UserID,
	).Scan(&submitterName, &submitterEmail)

	labelID := linearLabelFeature
	if req.Type == "bug" {
		labelID = linearLabelBug
	}
	labelIDs := []string{labelID, linearLabelKBL}

	now := time.Now().UTC()
	body := req.Description
	body += fmt.Sprintf("\n\n---\n**Submitted:** %s  \n**Submitted by:** %s (%s)  \n**Page:** %s",
		now.Format("2006-01-02 15:04 UTC"),
		submitterName,
		submitterEmail,
		req.PageURL,
	)

	if req.Screenshot != "" {
		if attachmentURL, err := uploadScreenshot(req.Screenshot, apiKey); err != nil {
			h.log.Warn().Err(err).Msg("failed to upload screenshot to Linear; continuing without it")
		} else if attachmentURL != "" {
			body += fmt.Sprintf("\n\n![Screenshot](%s)", attachmentURL)
		}
	}

	issueID, err := createLinearIssue(req.Title, body, labelIDs, teamID, triageID, apiKey)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create Linear issue")
		Error(w, http.StatusInternalServerError, "failed to submit feedback")
		return
	}

	h.log.Info().Str("issue_id", issueID).Str("type", req.Type).Str("user", claims.UserID).Msg("feedback submitted")
	JSON(w, http.StatusCreated, map[string]string{"id": issueID})
}

func createLinearIssue(title, description string, labelIDs []string, teamID, triageID, apiKey string) (string, error) {
	labelsJSON, _ := json.Marshal(labelIDs)

	query := fmt.Sprintf(`mutation {
		issueCreate(input: {
			teamId: "%s",
			stateId: "%s",
			title: %s,
			description: %s,
			labelIds: %s
		}) {
			success
			issue { id identifier }
		}
	}`, teamID, triageID, jsonStr(title), jsonStr(description), string(labelsJSON))

	resp, err := linearGraphQL(query, apiKey)
	if err != nil {
		return "", err
	}

	var result struct {
		Data struct {
			IssueCreate struct {
				Success bool `json:"success"`
				Issue   struct {
					ID         string `json:"id"`
					Identifier string `json:"identifier"`
				} `json:"issue"`
			} `json:"issueCreate"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("linear error: %s", result.Errors[0].Message)
	}
	if !result.Data.IssueCreate.Success {
		return "", fmt.Errorf("issue creation returned success=false")
	}
	return result.Data.IssueCreate.Issue.Identifier, nil
}

func uploadScreenshot(dataURL, apiKey string) (string, error) {
	b64 := dataURL
	if strings.HasPrefix(dataURL, "data:") {
		idx := strings.Index(dataURL, ",")
		if idx < 0 {
			return "", fmt.Errorf("invalid data URL")
		}
		b64 = dataURL[idx+1:]
	}

	b64 = strings.TrimSpace(b64)
	imgBytes, err := b64Enc.DecodeString(b64)
	if err != nil {
		imgBytes, err = base64.RawStdEncoding.DecodeString(b64)
		if err != nil {
			return "", fmt.Errorf("decode base64: %w", err)
		}
	}

	size := int64(len(imgBytes))
	filename := "screenshot.png"
	contentType := "image/png"

	prepQuery := fmt.Sprintf(`mutation {
		fileUpload(name: %s, size: %d, contentType: %s) {
			uploadFile { uploadUrl assetUrl headers { key value } }
		}
	}`, jsonStr(filename), size, jsonStr(contentType))

	prepResp, err := linearGraphQL(prepQuery, apiKey)
	if err != nil {
		return "", fmt.Errorf("prepare upload: %w", err)
	}

	var prepResult struct {
		Data struct {
			FileUpload struct {
				UploadFile struct {
					UploadURL string `json:"uploadUrl"`
					AssetURL  string `json:"assetUrl"`
					Headers   []struct {
						Key   string `json:"key"`
						Value string `json:"value"`
					} `json:"headers"`
				} `json:"uploadFile"`
			} `json:"fileUpload"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(prepResp, &prepResult); err != nil {
		return "", fmt.Errorf("parse upload prep: %w", err)
	}
	if len(prepResult.Errors) > 0 {
		return "", fmt.Errorf("linear upload prep error: %s", prepResult.Errors[0].Message)
	}

	uf := prepResult.Data.FileUpload.UploadFile
	if uf.UploadURL == "" {
		return "", fmt.Errorf("empty upload URL from Linear")
	}

	uploadReq, err := http.NewRequest(http.MethodPut, uf.UploadURL, bytes.NewReader(imgBytes))
	if err != nil {
		return "", err
	}
	uploadReq.Header.Set("Content-Type", contentType)
	for _, hdr := range uf.Headers {
		uploadReq.Header.Set(hdr.Key, hdr.Value)
	}

	uploadResp, err := http.DefaultClient.Do(uploadReq)
	if err != nil {
		return "", fmt.Errorf("PUT screenshot: %w", err)
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode >= 300 {
		return "", fmt.Errorf("PUT screenshot returned %d", uploadResp.StatusCode)
	}

	return uf.AssetURL, nil
}

func linearGraphQL(query, apiKey string) ([]byte, error) {
	payload, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequest(http.MethodPost, linearGraphQLURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
