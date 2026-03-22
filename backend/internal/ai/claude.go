package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	anthropicAPIURL     = "https://api.anthropic.com/v1/messages"
	anthropicVersion    = "2023-06-01"
	anthropicModel      = "claude-sonnet-4-20250514"
	defaultMaxTokens    = 2000
	defaultHTTPTimeout  = 60 * time.Second
)

type Comment struct {
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type Summary struct {
	ActionItems []string `json:"action_items"`
	Issues      []string `json:"issues"`
	Proposals   []string `json:"proposals"`
	RawText     string   `json:"raw_text"`
}

type Agenda struct {
	Items   []AgendaItem `json:"items"`
	RawText string         `json:"raw_text"`
}

type AgendaItem struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type APIError struct {
	StatusCode int    `json:"status_code"`
	Type       string `json:"type"`
	Message    string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("anthropic API error (status %d, type %q): %s", e.StatusCode, e.Type, e.Message)
}

type ClaudeClient struct {
	APIKey     string
	HTTPClient *http.Client
}

func NewClaudeClient(apiKey string) *ClaudeClient {
	return &ClaudeClient{
		APIKey: apiKey,
		HTTPClient: &http.Client{
			Timeout:   defaultHTTPTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

type messagesRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system"`
	Messages  []messageContent `json:"messages"`
}

type messageContent struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiErrorBody  `json:"error,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (c *ClaudeClient) sendMessage(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	reqBody := messagesRequest{
		Model:     anthropicModel,
		MaxTokens: defaultMaxTokens,
		System:    systemPrompt,
		Messages: []messageContent{
			{Role: "user", Content: userMessage},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp messagesResponse
		if jsonErr := json.Unmarshal(respBody, &errResp); jsonErr == nil && errResp.Error != nil {
			return "", &APIError{
				StatusCode: resp.StatusCode,
				Type:       errResp.Error.Type,
				Message:    errResp.Error.Message,
			}
		}
		return "", &APIError{
			StatusCode: resp.StatusCode,
			Type:       "unknown",
			Message:    string(respBody),
		}
	}

	var msgResp messagesResponse
	if err := json.Unmarshal(respBody, &msgResp); err != nil {
		return "", fmt.Errorf("unmarshalling response: %w", err)
	}

	var texts []string
	for _, block := range msgResp.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}

	return strings.Join(texts, "\n"), nil
}

func formatComments(comments []Comment) string {
	var b strings.Builder
	for i, c := range comments {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		fmt.Fprintf(&b, "From: %s (%s)\n%s", c.Author, c.CreatedAt, c.Body)
	}
	return b.String()
}

func (c *ClaudeClient) SummarizeComments(ctx context.Context, documentTitle string, comments []Comment) (*Summary, error) {
	systemPrompt := "You are helping a Norwegian harbor club board process member feedback on documents. " +
		"Extract and summarize: 1) Action items, 2) Issues/concerns raised, 3) Proposals. " +
		"Write in Norwegian unless the comments are in English. Be concise. " +
		"Respond in JSON format with keys: action_items (array of strings), issues (array of strings), proposals (array of strings)."

	userMessage := fmt.Sprintf("Document: %q\n\nComments:\n%s", documentTitle, formatComments(comments))

	rawText, err := c.sendMessage(ctx, systemPrompt, userMessage)
	if err != nil {
		return nil, fmt.Errorf("summarizing comments: %w", err)
	}

	summary := &Summary{RawText: rawText}

	cleaned := rawText
	if start := strings.Index(cleaned, "{"); start >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end >= start {
			cleaned = cleaned[start : end+1]
		}
	}

	if err := json.Unmarshal([]byte(cleaned), summary); err != nil {
		summary.ActionItems = []string{}
		summary.Issues = []string{}
		summary.Proposals = []string{}
	}

	return summary, nil
}

func (c *ClaudeClient) GenerateAgenda(ctx context.Context, documentTitle string, comments []Comment, existingAgenda string) (*Agenda, error) {
	systemPrompt := "Generate a structured meeting agenda for a Norwegian harbor club board meeting " +
		"based on the following document comments and feedback. Format with numbered items. Write in Norwegian. " +
		"Respond in JSON format with key: items (array of objects with number, title, description)."

	var userMessage strings.Builder
	fmt.Fprintf(&userMessage, "Document: %q\n\n", documentTitle)
	if existingAgenda != "" {
		fmt.Fprintf(&userMessage, "Existing agenda to build upon:\n%s\n\n", existingAgenda)
	}
	fmt.Fprintf(&userMessage, "Comments:\n%s", formatComments(comments))

	rawText, err := c.sendMessage(ctx, systemPrompt, userMessage.String())
	if err != nil {
		return nil, fmt.Errorf("generating agenda: %w", err)
	}

	agenda := &Agenda{RawText: rawText}

	cleaned := rawText
	if start := strings.Index(cleaned, "{"); start >= 0 {
		if end := strings.LastIndex(cleaned, "}"); end >= start {
			cleaned = cleaned[start : end+1]
		}
	}

	if err := json.Unmarshal([]byte(cleaned), agenda); err != nil {
		agenda.Items = []AgendaItem{}
	}

	return agenda, nil
}
