package sesevents

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Retention is how long sends and their events are kept.
const Retention = "13 months"

// Store persists delivery events and advances email_sends.status.
type Store struct {
	db *pgxpool.Pool
}

// NewStore returns a Store over db.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// Apply stores the records of one SES event in a single transaction and
// returns the records that were new. SQS delivers at least once, so a
// redelivered event is dropped by the unique key and returned as not new,
// which keeps the delivery counter and the send status from moving twice.
// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-short-and-long-polling.html
func (s *Store) Apply(ctx context.Context, recs []Record) ([]Record, error) {
	if len(recs) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sesevents: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inserted []Record
	for _, r := range recs {
		ok, err := applyOne(ctx, tx, r)
		if err != nil {
			return nil, err
		}
		if ok {
			inserted = append(inserted, r)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sesevents: commit: %w", err)
	}
	return inserted, nil
}

func applyOne(ctx context.Context, tx pgx.Tx, r Record) (bool, error) {
	sendID, curStatus, err := lockSend(ctx, tx, r)
	if err != nil {
		return false, err
	}

	var sendArg any
	if sendID != "" {
		sendArg = sendID
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO mail_delivery_events
			(send_id, ses_message_id, event_type, event_at, recipient, recipient_domain,
			 bounce_type, bounce_subtype, delay_type, smtp_status, diagnostic, remote_mta, raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		sendArg, r.SESMessageID, r.EventType, r.EventAt,
		nullable(r.Recipient), nullable(RecipientDomain(r.Recipient)),
		nullable(r.BounceType), nullable(r.BounceSubType), nullable(r.DelayType),
		nullable(r.SMTPStatus), nullable(r.Diagnostic), nullable(r.RemoteMTA),
		string(r.Raw),
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sesevents: insert event: %w", err)
	}
	if sendID == "" {
		return true, nil
	}

	newStatus := curStatus
	if ShouldAdvance(curStatus, r.Status) {
		newStatus = r.Status
	}
	if _, err := tx.Exec(ctx, `
		UPDATE email_sends
		   SET status         = $2,
		       ses_message_id = COALESCE(ses_message_id, $3),
		       last_event_at  = GREATEST(COALESCE(last_event_at, $4), $4),
		       updated_at     = now()
		 WHERE id = $1`,
		sendID, newStatus, r.SESMessageID, r.EventAt,
	); err != nil {
		return false, fmt.Errorf("sesevents: update send: %w", err)
	}
	return true, nil
}

// lockSend finds the email_sends row an event belongs to and locks it, so
// two events for the same send applied concurrently compare ranks against
// each other's result. The send_id tag is authoritative; the SES message id
// is the fallback for a send whose tag was lost. Returns "" when neither
// matches a row (the event is still stored, with a NULL send_id).
func lockSend(ctx context.Context, tx pgx.Tx, r Record) (id, status string, err error) {
	if _, perr := uuid.Parse(r.SendIDTag); perr == nil {
		err = tx.QueryRow(ctx,
			`SELECT id::text, status FROM email_sends WHERE id = $1 FOR UPDATE`, r.SendIDTag,
		).Scan(&id, &status)
		if err == nil {
			return id, status, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", "", fmt.Errorf("sesevents: lock send by id: %w", err)
		}
	}
	err = tx.QueryRow(ctx,
		`SELECT id::text, status FROM email_sends WHERE ses_message_id = $1 FOR UPDATE`, r.SESMessageID,
	).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("sesevents: lock send by message id: %w", err)
	}
	return id, status, nil
}

// PurgeExpired deletes sends and events past the retention window. Both
// tables hold recipient addresses (personal data), so they are not kept
// longer than the delivery history is useful.
func (s *Store) PurgeExpired(ctx context.Context) (sends, events int64, err error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM mail_delivery_events WHERE received_at < now() - $1::interval`, Retention)
	if err != nil {
		return 0, 0, fmt.Errorf("sesevents: purge events: %w", err)
	}
	events = tag.RowsAffected()
	tag, err = s.db.Exec(ctx,
		`DELETE FROM email_sends WHERE created_at < now() - $1::interval`, Retention)
	if err != nil {
		return 0, events, fmt.Errorf("sesevents: purge sends: %w", err)
	}
	return tag.RowsAffected(), events, nil
}

// Ping reports whether the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// compile-time check that the pool-backed store satisfies the worker's
// dependency.
var _ EventStore = (*Store)(nil)
