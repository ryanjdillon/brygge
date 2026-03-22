package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const resendAPIURL = "https://api.resend.com/emails"

// resendAPIURLOverride allows tests to point at a mock server.
var resendAPIURLOverride string

func apiURL() string {
	if resendAPIURLOverride != "" {
		return resendAPIURLOverride
	}
	return resendAPIURL
}

// Client sends emails via the Resend API.
type Client struct {
	apiKey      string
	fromAddress string
	httpClient  *http.Client
}

// NewClient creates a Resend email client. Returns nil if apiKey is empty.
func NewClient(apiKey, fromAddress string) *Client {
	if apiKey == "" {
		return nil
	}
	return &Client{
		apiKey:      apiKey,
		fromAddress: fromAddress,
		httpClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

type sendRequest struct {
	From    string `json:"from"`
	To      []string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

type sendResponse struct {
	ID string `json:"id"`
}

type errorResponse struct {
	Message    string `json:"message"`
	StatusCode int    `json:"statusCode"`
}

// Send delivers an email via Resend. Returns nil on success.
func (c *Client) Send(ctx context.Context, to, subject, htmlBody string) error {
	payload, err := json.Marshal(sendRequest{
		From:    c.fromAddress,
		To:      []string{to},
		Subject: subject,
		HTML:    htmlBody,
	})
	if err != nil {
		return fmt.Errorf("marshaling email request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending email: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("resend rate limited (429)")
	}
	if resp.StatusCode >= 400 {
		var errResp errorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Message != "" {
			return fmt.Errorf("resend API error (%d): %s", resp.StatusCode, errResp.Message)
		}
		return fmt.Errorf("resend API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// SendWithAttachment delivers an email with a PDF attachment via Resend.
func (c *Client) SendWithAttachment(ctx context.Context, to, subject, htmlBody, filename string, attachment []byte) error {
	type attachmentPayload struct {
		Filename string `json:"filename"`
		Content  []byte `json:"content"`
	}
	type requestWithAttachment struct {
		From        string              `json:"from"`
		To          []string            `json:"to"`
		Subject     string              `json:"subject"`
		HTML        string              `json:"html"`
		Attachments []attachmentPayload `json:"attachments"`
	}

	payload, err := json.Marshal(requestWithAttachment{
		From:    c.fromAddress,
		To:      []string{to},
		Subject: subject,
		HTML:    htmlBody,
		Attachments: []attachmentPayload{
			{Filename: filename, Content: attachment},
		},
	})
	if err != nil {
		return fmt.Errorf("marshaling email request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending email: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("resend rate limited (429)")
	}
	if resp.StatusCode >= 400 {
		var errResp errorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Message != "" {
			return fmt.Errorf("resend API error (%d): %s", resp.StatusCode, errResp.Message)
		}
		return fmt.Errorf("resend API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}
