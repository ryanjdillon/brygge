package email

import (
	"context"
	"errors"
)

// ErrDeliveryAmbiguous marks a send whose outcome is unknown: the connection
// failed while the message body was being written or while waiting for the
// relay's answer to the end of DATA. The relay may have queued the message,
// so a retry can deliver it twice. No caller may retry such a send
// automatically; it needs a human (or a later delivery event) to decide.
var ErrDeliveryAmbiguous = errors.New("email delivery outcome unknown")

// Provider names the relay a send went through. It is the email_sends
// provider column and the provider attribute of brygge.email.send.
type Provider string

const (
	ProviderSES      Provider = "ses"
	ProviderStalwart Provider = "stalwart"
)

// SendMeta describes what a send is about, for the email_sends row and the
// SES message tags. IDs are UUID strings as the handlers carry them; an empty
// or malformed ID is recorded as NULL. ClubID empty means "the instance's
// club", resolved by the recorder from CLUB_SLUG.
type SendMeta struct {
	ClubID string
	Kind   Kind
	// UserID is the member the mail is about, when known. It ties the row to
	// the member so a GDPR erasure removes it.
	UserID string
	// RelatedType and RelatedID name the record the mail carries, e.g.
	// "invoice" and the faktura's id.
	RelatedType string
	RelatedID   string
}

type sendMetaKey struct{}

// WithSendMeta tags ctx with what the next send is about. Its Kind takes
// precedence over WithKind.
func WithSendMeta(ctx context.Context, m SendMeta) context.Context {
	return context.WithValue(ctx, sendMetaKey{}, m)
}

// SendMetaFrom returns the metadata WithSendMeta put on ctx.
func SendMetaFrom(ctx context.Context) (SendMeta, bool) {
	m, ok := ctx.Value(sendMetaKey{}).(SendMeta)
	return m, ok
}

// attempt carries one recorded send between the recorder and the transport:
// the recorder sets sendID (the SES send_id tag), the transport reports the
// relay's message id back.
type attempt struct {
	sendID    string
	messageID string
}

type attemptKey struct{}

func withAttempt(ctx context.Context, a *attempt) context.Context {
	return context.WithValue(ctx, attemptKey{}, a)
}

func attemptFrom(ctx context.Context) *attempt {
	a, _ := ctx.Value(attemptKey{}).(*attempt)
	return a
}
