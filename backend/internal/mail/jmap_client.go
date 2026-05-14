package mail

// Minimal JMAP client (RFC 8620 / RFC 8621) used by the read-only
// shared-inbox surface (DIL-277). The Brygge backend acts as a
// server-side JMAP client on behalf of a member, authenticating to
// Stalwart with admin Basic auth and scoping per-call to the shared
// principal's accountId. This avoids the per-user JMAP token-mint
// dance until Stalwart 0.15's exact token endpoint is verified
// (still an open item from DIL-276).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// JMAPClient talks to Stalwart's JMAP endpoint. The session
// discovery step in vanilla JMAP returns the per-account API URL;
// Stalwart serves a single API URL for all accounts at /jmap so we
// hardcode it. If a future Stalwart version moves it, the only
// affected method is Call().
type JMAPClient struct {
	baseURL string
	admin   *AdminClient
	http    *http.Client
}

func NewJMAPClient(baseURL string, admin *AdminClient) *JMAPClient {
	return &JMAPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		admin:   admin,
		http:    admin.http,
	}
}

// invocation is the JMAP method-call wire shape: [methodName, args, clientId].
type invocation [3]any

// Call sends one or more method invocations and decodes the response.
// `using` defaults to the JMAP core + mail capabilities when nil.
func (c *JMAPClient) Call(ctx context.Context, using []string, calls []invocation) ([]invocation, error) {
	if using == nil {
		using = []string{
			"urn:ietf:params:jmap:core",
			"urn:ietf:params:jmap:mail",
		}
	}
	body := struct {
		Using       []string     `json:"using"`
		MethodCalls []invocation `json:"methodCalls"`
	}{Using: using, MethodCalls: calls}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/jmap/api", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.admin.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("jmap %s: %s", resp.Status, truncate(raw, 240))
	}
	var env struct {
		MethodResponses []invocation `json:"methodResponses"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("jmap decode: %w", err)
	}
	return env.MethodResponses, nil
}

// Mailbox is the slim projection of JMAP Mailbox/get we need.
type Mailbox struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Role          string `json:"role"` // "inbox", "archive", …
	TotalEmails   int    `json:"totalEmails"`
	UnreadEmails  int    `json:"unreadEmails"`
	TotalThreads  int    `json:"totalThreads"`
	UnreadThreads int    `json:"unreadThreads"`
}

// ListMailboxes returns all mailboxes (folders) for an account.
func (c *JMAPClient) ListMailboxes(ctx context.Context, accountID string) ([]Mailbox, error) {
	resp, err := c.Call(ctx, nil, []invocation{
		{"Mailbox/get", map[string]any{
			"accountId": accountID,
			"ids":       nil,
			"properties": []string{
				"id", "name", "role",
				"totalEmails", "unreadEmails",
				"totalThreads", "unreadThreads",
			},
		}, "0"},
	})
	if err != nil {
		return nil, err
	}
	if len(resp) == 0 {
		return nil, fmt.Errorf("ListMailboxes: empty response")
	}
	var args struct {
		List []Mailbox `json:"list"`
	}
	if err := decodeArgs(resp[0], &args); err != nil {
		return nil, err
	}
	return args.List, nil
}

// EmailSummary is the slim Email/get projection used by thread lists.
type EmailSummary struct {
	ID          string         `json:"id"`
	ThreadID    string         `json:"threadId"`
	MailboxIDs  map[string]any `json:"mailboxIds"`
	Keywords    map[string]any `json:"keywords"`
	Subject     string         `json:"subject"`
	From        []EmailAddress `json:"from"`
	To          []EmailAddress `json:"to"`
	Preview     string         `json:"preview"`
	ReceivedAt  string         `json:"receivedAt"`
	HasAttach   bool           `json:"hasAttachment"`
}

type EmailAddress struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// QueryEmails returns paginated email IDs in a mailbox, newest first.
// `text` is a JMAP full-text filter applied to subject/from/body.
func (c *JMAPClient) QueryEmails(ctx context.Context, accountID, mailboxID, text string, position, limit int) (ids []string, total int, err error) {
	filter := map[string]any{"inMailbox": mailboxID}
	if text != "" {
		filter = map[string]any{
			"operator":   "AND",
			"conditions": []any{filter, map[string]any{"text": text}},
		}
	}
	resp, err := c.Call(ctx, nil, []invocation{
		{"Email/query", map[string]any{
			"accountId":        accountID,
			"filter":           filter,
			"sort":             []any{map[string]any{"property": "receivedAt", "isAscending": false}},
			"position":         position,
			"limit":            limit,
			"calculateTotal":   true,
		}, "0"},
	})
	if err != nil {
		return nil, 0, err
	}
	var args struct {
		IDs   []string `json:"ids"`
		Total int      `json:"total"`
	}
	if err := decodeArgs(resp[0], &args); err != nil {
		return nil, 0, err
	}
	return args.IDs, args.Total, nil
}

// GetEmailSummaries fetches header projections for a batch of email IDs.
func (c *JMAPClient) GetEmailSummaries(ctx context.Context, accountID string, ids []string) ([]EmailSummary, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	resp, err := c.Call(ctx, nil, []invocation{
		{"Email/get", map[string]any{
			"accountId": accountID,
			"ids":       ids,
			"properties": []string{
				"id", "threadId", "mailboxIds", "keywords",
				"subject", "from", "to", "preview", "receivedAt", "hasAttachment",
			},
		}, "0"},
	})
	if err != nil {
		return nil, err
	}
	var args struct {
		List []EmailSummary `json:"list"`
	}
	if err := decodeArgs(resp[0], &args); err != nil {
		return nil, err
	}
	return args.List, nil
}

// EmailFull is Email/get with body parts for ThreadReader rendering.
type EmailFull struct {
	EmailSummary
	BodyValues map[string]struct {
		Value string `json:"value"`
	} `json:"bodyValues"`
	HTMLBody    []EmailBodyPart `json:"htmlBody"`
	TextBody    []EmailBodyPart `json:"textBody"`
	Attachments []EmailBodyPart `json:"attachments"`
}

type EmailBodyPart struct {
	PartID      string `json:"partId"`
	BlobID      string `json:"blobId"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Size        int    `json:"size"`
	CID         string `json:"cid"`
	Disposition string `json:"disposition"`
}

// GetEmailsFull fetches every email in a thread with body parts.
func (c *JMAPClient) GetEmailsFull(ctx context.Context, accountID string, ids []string) ([]EmailFull, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	resp, err := c.Call(ctx, nil, []invocation{
		{"Email/get", map[string]any{
			"accountId":            accountID,
			"ids":                  ids,
			"fetchHTMLBodyValues":  true,
			"fetchTextBodyValues":  true,
			"maxBodyValueBytes":    256 * 1024,
			"properties": []string{
				"id", "threadId", "mailboxIds", "keywords",
				"subject", "from", "to", "preview", "receivedAt", "hasAttachment",
				"htmlBody", "textBody", "bodyValues", "attachments",
			},
		}, "0"},
	})
	if err != nil {
		return nil, err
	}
	var args struct {
		List []EmailFull `json:"list"`
	}
	if err := decodeArgs(resp[0], &args); err != nil {
		return nil, err
	}
	return args.List, nil
}

// GetThreadEmailIDs returns all email IDs that belong to a thread.
func (c *JMAPClient) GetThreadEmailIDs(ctx context.Context, accountID, threadID string) ([]string, error) {
	resp, err := c.Call(ctx, nil, []invocation{
		{"Thread/get", map[string]any{
			"accountId": accountID,
			"ids":       []string{threadID},
		}, "0"},
	})
	if err != nil {
		return nil, err
	}
	var args struct {
		List []struct {
			EmailIDs []string `json:"emailIds"`
		} `json:"list"`
	}
	if err := decodeArgs(resp[0], &args); err != nil {
		return nil, err
	}
	if len(args.List) == 0 {
		return nil, fmt.Errorf("thread %s not found", threadID)
	}
	return args.List[0].EmailIDs, nil
}

// SetKeywordOnThread toggles a keyword (e.g. "$seen") on every email
// in a thread. Returns the number of emails updated.
func (c *JMAPClient) SetKeywordOnThread(ctx context.Context, accountID, threadID, keyword string, value bool) (int, error) {
	emailIDs, err := c.GetThreadEmailIDs(ctx, accountID, threadID)
	if err != nil {
		return 0, err
	}
	if len(emailIDs) == 0 {
		return 0, nil
	}
	update := make(map[string]any, len(emailIDs))
	for _, id := range emailIDs {
		// JMAP patch syntax: setting a keyword pointer to true sets
		// it; setting it to null removes it.
		var v any
		if value {
			v = true
		}
		update[id] = map[string]any{"keywords/" + keyword: v}
	}
	_, err = c.Call(ctx, nil, []invocation{
		{"Email/set", map[string]any{
			"accountId": accountID,
			"update":    update,
		}, "0"},
	})
	if err != nil {
		return 0, err
	}
	return len(emailIDs), nil
}

// MoveThreadToMailbox moves every email in a thread to a single
// destination mailbox (used for archive). JMAP idiom for "move" is
// to overwrite mailboxIds with the destination set.
func (c *JMAPClient) MoveThreadToMailbox(ctx context.Context, accountID, threadID, destMailboxID string) (int, error) {
	emailIDs, err := c.GetThreadEmailIDs(ctx, accountID, threadID)
	if err != nil {
		return 0, err
	}
	if len(emailIDs) == 0 {
		return 0, nil
	}
	update := make(map[string]any, len(emailIDs))
	for _, id := range emailIDs {
		update[id] = map[string]any{
			"mailboxIds": map[string]bool{destMailboxID: true},
		}
	}
	_, err = c.Call(ctx, nil, []invocation{
		{"Email/set", map[string]any{
			"accountId": accountID,
			"update":    update,
		}, "0"},
	})
	if err != nil {
		return 0, err
	}
	return len(emailIDs), nil
}

// decodeArgs pulls the args object (index 1) out of a JMAP invocation
// triple and decodes it into out.
func decodeArgs(inv invocation, out any) error {
	if len(inv) < 2 {
		return fmt.Errorf("jmap: malformed invocation")
	}
	raw, err := json.Marshal(inv[1])
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

