package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClaudeClientSetsAPIKey(t *testing.T) {
	client := NewClaudeClient("sk-test-key-123")

	if client.APIKey != "sk-test-key-123" {
		t.Errorf("APIKey = %q, want %q", client.APIKey, "sk-test-key-123")
	}
	if client.HTTPClient == nil {
		t.Error("HTTPClient is nil")
	}
}

func newMockAnthropicServer(t *testing.T, statusCode int, response any, assertHeaders func(h http.Header)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if assertHeaders != nil {
			assertHeaders(r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(response)
	}))
}

func newTestClaudeClient(serverURL string) *ClaudeClient {
	return &ClaudeClient{
		APIKey: "test-api-key",
		HTTPClient: &http.Client{
			Transport: rewriteTransport{serverURL: serverURL},
		},
	}
}

type rewriteTransport struct {
	serverURL string
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.serverURL[len("http://"):]
	return http.DefaultTransport.RoundTrip(req)
}

func TestSummarizeComments(t *testing.T) {
	summaryJSON := `{"action_items":["Fix dock 3"],"issues":["Noise complaints"],"proposals":["New kayak rack"]}`

	var gotHeaders http.Header
	server := newMockAnthropicServer(t, http.StatusOK, messagesResponse{
		Content: []contentBlock{
			{Type: "text", Text: summaryJSON},
		},
	}, func(h http.Header) {
		gotHeaders = h.Clone()
	})
	defer server.Close()

	client := newTestClaudeClient(server.URL)

	comments := []Comment{
		{Author: "Ola", Body: "We need to fix dock 3", CreatedAt: "2025-01-01"},
	}

	summary, err := client.SummarizeComments(context.Background(), "Dock Report", comments)
	if err != nil {
		t.Fatalf("SummarizeComments() error = %v", err)
	}

	if gotHeaders.Get("x-api-key") != "test-api-key" {
		t.Errorf("x-api-key = %q, want %q", gotHeaders.Get("x-api-key"), "test-api-key")
	}
	if gotHeaders.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want %q", gotHeaders.Get("anthropic-version"), "2023-06-01")
	}

	if len(summary.ActionItems) != 1 || summary.ActionItems[0] != "Fix dock 3" {
		t.Errorf("ActionItems = %v, want [Fix dock 3]", summary.ActionItems)
	}
	if len(summary.Issues) != 1 || summary.Issues[0] != "Noise complaints" {
		t.Errorf("Issues = %v, want [Noise complaints]", summary.Issues)
	}
	if len(summary.Proposals) != 1 || summary.Proposals[0] != "New kayak rack" {
		t.Errorf("Proposals = %v, want [New kayak rack]", summary.Proposals)
	}
}

func TestSummarizeCommentsAPIError(t *testing.T) {
	server := newMockAnthropicServer(t, http.StatusTooManyRequests, messagesResponse{
		Error: &apiErrorBody{
			Type:    "rate_limit_error",
			Message: "Rate limit exceeded",
		},
	}, nil)
	defer server.Close()

	client := newTestClaudeClient(server.URL)

	_, err := client.SummarizeComments(context.Background(), "Doc", []Comment{
		{Author: "Test", Body: "Hello", CreatedAt: "2025-01-01"},
	})
	if err == nil {
		t.Fatal("SummarizeComments() expected error for 429, got nil")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusTooManyRequests)
	}
	if apiErr.Type != "rate_limit_error" {
		t.Errorf("Type = %q, want %q", apiErr.Type, "rate_limit_error")
	}
}

func TestGenerateSakliste(t *testing.T) {
	saklisteJSON := `{"items":[{"number":1,"title":"Vedlikehold brygge 3","description":"Diskutere reparasjon"}]}`

	server := newMockAnthropicServer(t, http.StatusOK, messagesResponse{
		Content: []contentBlock{
			{Type: "text", Text: saklisteJSON},
		},
	}, nil)
	defer server.Close()

	client := newTestClaudeClient(server.URL)

	sakliste, err := client.GenerateSakliste(context.Background(), "Board Meeting", []Comment{
		{Author: "Kari", Body: "Dock 3 needs repair", CreatedAt: "2025-01-01"},
	}, "")
	if err != nil {
		t.Fatalf("GenerateSakliste() error = %v", err)
	}

	if len(sakliste.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(sakliste.Items))
	}
	if sakliste.Items[0].Number != 1 {
		t.Errorf("Items[0].Number = %d, want 1", sakliste.Items[0].Number)
	}
	if sakliste.Items[0].Title != "Vedlikehold brygge 3" {
		t.Errorf("Items[0].Title = %q, want %q", sakliste.Items[0].Title, "Vedlikehold brygge 3")
	}
}
