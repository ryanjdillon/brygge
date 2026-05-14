package mail

// Minimal JMAP client (RFC 8620 / RFC 8621) for the shared-inbox
// surface (DIL-275/276/277). Stalwart 0.15's JMAP requires
// authenticating AS the account whose mailboxes you operate on —
// admin Basic auth sees only the admin's own account in the session
// resource and cannot scope calls to other accountIds. So the
// reconciler (and the user-facing read path) constructs one client
// per principal it acts as.
//
// Stalwart 0.15 also doesn't support standard OAuth password-grant
// or token-exchange on /auth/token (verified against the running
// 0.15.x build — both return invalid_grant). So per-principal Basic
// auth using a service password is the practical path; the password
// for each shared principal lives in
// /etc/stalwart/board-mailbox-passwords.json, root-owned + brygge-
// readable, written by stalwart-mailbox-config.service. Real users
// (DIL-277 read path) authenticate with their own Stalwart password.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// JMAPClient talks to Stalwart's JMAP endpoint as a single
// authenticated principal. Cheap to construct; share the http.Client
// across instances so connection pooling actually pools.
type JMAPClient struct {
	baseURL string
	user    string
	pass    string
	http    *http.Client
}

// NewJMAPClient builds a client authenticating as `user`/`pass`. The
// caller is expected to reuse `httpClient` across instances; pass nil
// to get a sensible default (10s timeout).
func NewJMAPClient(baseURL, user, pass string, httpClient *http.Client) *JMAPClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &JMAPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		user:    user,
		pass:    pass,
		http:    httpClient,
	}
}

// JMAPFactory mints per-principal JMAPClients backed by a shared
// http.Client. Convenient for the reconciler loop where we change
// principals each iteration.
type JMAPFactory struct {
	baseURL string
	http    *http.Client
}

func NewJMAPFactory(baseURL string) *JMAPFactory {
	return &JMAPFactory{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (f *JMAPFactory) AsPrincipal(user, pass string) *JMAPClient {
	return &JMAPClient{
		baseURL: f.baseURL,
		user:    user,
		pass:    pass,
		http:    f.http,
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
	// Stalwart 0.15 serves JMAP at /jmap/ (verified against the
	// session resource at /jmap/session). The standard RFC 8620
	// discovery endpoint is /.well-known/jmap, but it returns the
	// same apiUrl, so we skip the round-trip and hardcode.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/jmap/", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.pass)
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

// SessionAccounts returns the JMAP account IDs the authenticated
// principal has access to. Stalwart 0.15 shows just the principal's
// own account here (admin sees admin's id; leiar sees leiar's id).
func (c *JMAPClient) SessionAccounts(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/jmap/session", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jmap session %s: %s", resp.Status, truncate(body, 200))
	}
	var env struct {
		Accounts map[string]any `json:"accounts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("jmap session: decode: %w", err)
	}
	ids := make([]string, 0, len(env.Accounts))
	for id := range env.Accounts {
		ids = append(ids, id)
	}
	return ids, nil
}

// Principal is the slim Principal/get projection used by the
// reconciler to map emails to JMAP account IDs. The JMAP id (e.g.
// "f") is distinct from the admin REST API's numeric id (e.g. 5).
// shareWith and most JMAP operations require the JMAP id.
type Principal struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Type  string `json:"type"`
}

// ListPrincipals enumerates every principal visible to the calling
// account (admin sees all). Requires the JMAP Principals capability
// (`urn:ietf:params:jmap:principals`).
func (c *JMAPClient) ListPrincipals(ctx context.Context, accountID string) ([]Principal, error) {
	resp, err := c.Call(ctx, []string{
		"urn:ietf:params:jmap:core",
		"urn:ietf:params:jmap:principals",
	}, []invocation{
		{"Principal/get", map[string]any{
			"accountId":  accountID,
			"ids":        nil,
			"properties": []string{"id", "name", "email", "type"},
		}, "0"},
	})
	if err != nil {
		return nil, err
	}
	if len(resp) == 0 {
		return nil, fmt.Errorf("Principal/get: empty response")
	}
	var args struct {
		List []Principal `json:"list"`
	}
	if err := decodeArgs(resp[0], &args); err != nil {
		return nil, err
	}
	return args.List, nil
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

// ShareRights is the per-principal grant set on a JMAP Mailbox.
// Stalwart 0.15 implements RFC 8621 §2.5 (`shareWith`); no admin REST
// surface exists for ACL CRUD — sharing is set on the mailbox itself,
// via JMAP, as the mailbox's owner principal.
//
// Field names match the RFC 8621 §2.5 vocabulary exactly. Stalwart
// rejects deviations with `invalidProperties` and the offending name
// in `notUpdated.<id>.description` (verified 2026-05-14).
type ShareRights struct {
	MayReadItems   bool `json:"mayReadItems"`
	MayAddItems    bool `json:"mayAddItems"`
	MayRemoveItems bool `json:"mayRemoveItems"`
	MaySetSeen     bool `json:"maySetSeen"`
	MaySetKeywords bool `json:"maySetKeywords"`
	MayCreateChild bool `json:"mayCreateChild"`
	MayRename      bool `json:"mayRename"`
	MayDelete      bool `json:"mayDelete"`
	MaySubmit      bool `json:"maySubmit"`
}

// SetMailboxShareWith replaces the `shareWith` map on a single
// mailbox (typically the Inbox of a shared principal). `accountID` is
// the owner — the same principal id used to scope every other call
// in this client. `shareWith` is keyed by the receiving principal's
// id; an empty map clears all shares.
func (c *JMAPClient) SetMailboxShareWith(ctx context.Context, accountID, mailboxID string, shareWith map[string]ShareRights) error {
	resp, err := c.Call(ctx, nil, []invocation{
		{"Mailbox/set", map[string]any{
			"accountId": accountID,
			"update": map[string]any{
				mailboxID: map[string]any{
					"shareWith": shareWith,
				},
			},
		}, "0"},
	})
	if err != nil {
		return err
	}
	if len(resp) == 0 {
		return fmt.Errorf("Mailbox/set: empty response")
	}
	var args struct {
		NotUpdated map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"notUpdated"`
	}
	if err := decodeArgs(resp[0], &args); err != nil {
		return err
	}
	if e, bad := args.NotUpdated[mailboxID]; bad {
		return fmt.Errorf("Mailbox/set %s: %s: %s", mailboxID, e.Type, e.Description)
	}
	return nil
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

