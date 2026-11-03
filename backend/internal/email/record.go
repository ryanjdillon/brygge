package email

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Sender-owned email_sends statuses. Delivery events (sesevents) move a row
// on from these; they never move it back.
const (
	SendStatusSending   = "sending"
	SendStatusSubmitted = "submitted"
	SendStatusFailed    = "failed"
	SendStatusUnknown   = "unknown"
)

// ErrNoClub is returned by SendLog.Start when the send names no club and the
// instance's club cannot be found, so there is nothing to file the row under.
var ErrNoClub = errors.New("email send log: no club to record the send under")

// LoggedSend is one email_sends row as the sender opens it.
type LoggedSend struct {
	ID        string
	Meta      SendMeta
	Provider  Provider
	Recipient string
}

// SendLog persists the sender's side of email_sends. Start runs before the
// relay is contacted, Finish once the SMTP exchange is over.
type SendLog interface {
	Start(ctx context.Context, s LoggedSend) error
	Finish(ctx context.Context, id, status, messageID, errText string) error
}

// finishTimeout bounds the status update after a send. It runs detached from
// the caller's context: a request cancelled mid-send must still leave the
// row saying what happened.
const finishTimeout = 5 * time.Second

// maxErrorText caps the stored SMTP error; relay replies can be long and the
// row only needs enough to tell the failure apart.
const maxErrorText = 500

// Record wraps next so every send is logged in email_sends: a row in status
// sending is written before the relay is contacted, then set to submitted,
// failed or unknown (ErrDeliveryAmbiguous). The row's id travels to the
// transport as the SES send_id tag. If the row cannot be written the mail is
// still sent, unrecorded: losing a log row is better than losing a login
// link. Returns nil when next is nil.
func Record(next Sender, provider Provider, log SendLog, logger zerolog.Logger) Sender {
	if next == nil {
		return nil
	}
	return &recordedSender{next: next, provider: provider, log: log, logger: logger}
}

type recordedSender struct {
	next     Sender
	provider Provider
	log      SendLog
	logger   zerolog.Logger
}

var _ Sender = (*recordedSender)(nil)

func (r *recordedSender) Send(ctx context.Context, to, subject, htmlBody string) error {
	return r.record(ctx, to, func(ctx context.Context) error {
		return r.next.Send(ctx, to, subject, htmlBody)
	})
}

func (r *recordedSender) SendWithAttachment(ctx context.Context, to, subject, htmlBody, filename string, attachment []byte) error {
	return r.record(ctx, to, func(ctx context.Context) error {
		return r.next.SendWithAttachment(ctx, to, subject, htmlBody, filename, attachment)
	})
}

func (r *recordedSender) SendWithHeaders(ctx context.Context, to, subject, htmlBody string, extraHeaders map[string]string) error {
	return r.record(ctx, to, func(ctx context.Context) error {
		return r.next.SendWithHeaders(ctx, to, subject, htmlBody, extraHeaders)
	})
}

func (r *recordedSender) record(ctx context.Context, to string, send func(context.Context) error) error {
	meta, _ := SendMetaFrom(ctx)
	meta.Kind = kindFrom(ctx)
	id := uuid.NewString()
	if err := r.log.Start(ctx, LoggedSend{ID: id, Meta: meta, Provider: r.provider, Recipient: to}); err != nil {
		ev := r.logger.Error()
		if errors.Is(err, ErrNoClub) {
			ev = r.logger.Warn()
		}
		ev.Err(err).Str("kind", string(meta.Kind)).Str("provider", string(r.provider)).
			Msg("email send not recorded; sending anyway")
		return send(ctx)
	}

	a := &attempt{sendID: id}
	err := send(withAttempt(ctx, a))

	status, errText := SendStatusSubmitted, ""
	switch {
	case errors.Is(err, ErrDeliveryAmbiguous):
		status, errText = SendStatusUnknown, err.Error()
	case err != nil:
		status, errText = SendStatusFailed, err.Error()
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	if ferr := r.log.Finish(fctx, id, status, a.messageID, truncate(errText, maxErrorText)); ferr != nil {
		r.logger.Error().Err(ferr).Str("send_id", id).Str("status", status).
			Msg("email sent but its send log row was not updated")
	}
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary so the stored text stays valid UTF-8.
	for n > 0 && n < len(s) && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

// PGSendLog is the SendLog over the email_sends table.
type PGSendLog struct {
	db       *pgxpool.Pool
	clubSlug string

	mu     sync.Mutex
	clubID string
}

// NewSendLog returns a SendLog writing to db. clubSlug (CLUB_SLUG) names the
// club a send without SendMeta.ClubID is filed under, the same way the
// handlers that run without claims (magic link, contact form) find theirs.
func NewSendLog(db *pgxpool.Pool, clubSlug string) *PGSendLog {
	return &PGSendLog{db: db, clubSlug: clubSlug}
}

// Start inserts the row in status sending.
func (l *PGSendLog) Start(ctx context.Context, s LoggedSend) error {
	clubID := s.Meta.ClubID
	if _, err := uuid.Parse(clubID); err != nil {
		if clubID, err = l.instanceClub(ctx); err != nil {
			return err
		}
	}
	var relatedType any
	if s.Meta.RelatedType != "" {
		relatedType = s.Meta.RelatedType
	}
	_, err := l.db.Exec(ctx, `
		INSERT INTO email_sends
			(id, club_id, user_id, kind, recipient, recipient_domain,
			 related_type, related_id, provider, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		s.ID, clubID, nullUUID(s.Meta.UserID), string(s.Meta.Kind), s.Recipient,
		recipientDomain(s.Recipient), relatedType, nullUUID(s.Meta.RelatedID),
		string(s.Provider), SendStatusSending,
	)
	if err != nil {
		return fmt.Errorf("email send log: insert: %w", err)
	}
	return nil
}

// Finish records the SMTP outcome. Only a row still in sending is updated:
// a delivery event that arrived first has already moved it further, and the
// sender's own status ranks below every event's.
func (l *PGSendLog) Finish(ctx context.Context, id, status, messageID, errText string) error {
	_, err := l.db.Exec(ctx, `
		UPDATE email_sends
		   SET status         = $2,
		       ses_message_id = COALESCE(ses_message_id, NULLIF($3, '')),
		       error          = NULLIF($4, ''),
		       updated_at     = now()
		 WHERE id = $1 AND status = $5`,
		id, status, messageID, errText, SendStatusSending,
	)
	if err != nil {
		return fmt.Errorf("email send log: finish: %w", err)
	}
	return nil
}

// instanceClub resolves CLUB_SLUG once; a deployment serves one club.
func (l *PGSendLog) instanceClub(ctx context.Context) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.clubID != "" {
		return l.clubID, nil
	}
	if l.clubSlug == "" {
		return "", ErrNoClub
	}
	var id string
	err := l.db.QueryRow(ctx, `SELECT id::text FROM clubs WHERE slug = $1`, l.clubSlug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoClub
	}
	if err != nil {
		return "", fmt.Errorf("email send log: resolve club: %w", err)
	}
	l.clubID = id
	return id, nil
}

func nullUUID(s string) any {
	if _, err := uuid.Parse(s); err != nil {
		return nil
	}
	return s
}

// recipientDomain is the lower-cased part after the last '@', matching
// sesevents.RecipientDomain so sends and their events group alike.
func recipientDomain(addr string) string {
	i := strings.LastIndexByte(addr, '@')
	if i < 0 || i == len(addr)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(addr[i+1:]))
}
