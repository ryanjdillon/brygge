package email

import "context"

// Sender abstracts email delivery. Handlers accept this instead of a
// concrete client so tests can inject a MockSender and production can
// swap transports without handler changes.
type Sender interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
	SendWithAttachment(ctx context.Context, to, subject, htmlBody, filename string, attachment []byte) error
	// SendWithHeaders sends a plain HTML email with extra RFC 5322 headers
	// (e.g. List-Unsubscribe). Use for broadcast sends; transactional sends
	// should use Send to avoid accidentally adding opt-out headers.
	SendWithHeaders(ctx context.Context, to, subject, htmlBody string, extraHeaders map[string]string) error
}
