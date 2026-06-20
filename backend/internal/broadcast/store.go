// Package broadcast holds the persistence layer for bulk member sends:
// a parent broadcast row plus one delivery row per recipient that the
// background worker drains. It is shared by the inbox send path (which
// enqueues) and the delivery worker (which claims and marks rows).
package broadcast

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Delivery statuses.
const (
	DeliveryPending = "pending"
	DeliverySending = "sending"
	DeliverySent    = "sent"
	DeliveryFailed  = "failed"
)

// Broadcast (parent) statuses.
const (
	BroadcastPending  = "pending"
	BroadcastComplete = "complete"
)

// Store wraps the pool with broadcast/delivery queries.
type Store struct {
	db *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// New describes a broadcast to enqueue.
type New struct {
	ClubID        string
	SentBy        string
	SourceAddress string
	Subject       string
	BodyText      string
	BodyHTML      string
	// Recipients is a human-readable summary label (e.g. "Members, Board").
	Recipients string
}

// Recipient is one resolved target for a bulk send. UserID is nil for
// ad-hoc addresses that don't map to a member.
type Recipient struct {
	UserID *string
	Email  string
}

// Enqueue inserts the parent broadcast plus one pending delivery per
// recipient in a single transaction and returns the broadcast id.
func (s *Store) Enqueue(ctx context.Context, n New, recipients []Recipient) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO broadcasts (club_id, subject, body, body_html, recipients, source_address, sent_by, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		n.ClubID, n.Subject, n.BodyText, n.BodyHTML, n.Recipients, n.SourceAddress, n.SentBy, BroadcastPending,
	).Scan(&id); err != nil {
		return "", fmt.Errorf("insert broadcast: %w", err)
	}

	batch := &pgx.Batch{}
	for _, r := range recipients {
		batch.Queue(`
			INSERT INTO broadcast_deliveries (broadcast_id, club_id, user_id, email, status)
			VALUES ($1, $2, $3, $4, $5)`,
			id, n.ClubID, r.UserID, r.Email, DeliveryPending)
	}
	br := tx.SendBatch(ctx, batch)
	for range recipients {
		if _, err := br.Exec(); err != nil {
			br.Close() //nolint:errcheck
			return "", fmt.Errorf("insert delivery: %w", err)
		}
	}
	if err := br.Close(); err != nil {
		return "", fmt.Errorf("close batch: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// Claimed is a pending delivery joined with the parent fields the worker
// needs to actually send the message.
type Claimed struct {
	DeliveryID    string
	BroadcastID   string
	ClubID        string
	SourceAddress string
	Subject       string
	BodyText      string
	BodyHTML      string
	Email         string
	Attempts      int
}

// ClaimPending atomically claims up to limit pending deliveries, flipping
// them to 'sending' so a concurrent worker can't grab the same rows
// (FOR UPDATE SKIP LOCKED), and returns them joined with their parent.
func (s *Store) ClaimPending(ctx context.Context, limit int) ([]Claimed, error) {
	rows, err := s.db.Query(ctx, `
		WITH claimed AS (
			SELECT id
			FROM broadcast_deliveries
			WHERE status = $1
			ORDER BY created_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), upd AS (
			UPDATE broadcast_deliveries bd
			SET status = $3, updated_at = now()
			FROM claimed
			WHERE bd.id = claimed.id
			RETURNING bd.id, bd.broadcast_id, bd.club_id, bd.email, bd.attempts
		)
		SELECT upd.id, upd.broadcast_id, upd.club_id, upd.email, upd.attempts,
		       COALESCE(b.source_address, ''), b.subject, b.body, COALESCE(b.body_html, '')
		FROM upd
		JOIN broadcasts b ON b.id = upd.broadcast_id`,
		DeliveryPending, limit, DeliverySending,
	)
	if err != nil {
		return nil, fmt.Errorf("claim pending: %w", err)
	}
	defer rows.Close()

	var out []Claimed
	for rows.Next() {
		var c Claimed
		if err := rows.Scan(&c.DeliveryID, &c.BroadcastID, &c.ClubID, &c.Email, &c.Attempts,
			&c.SourceAddress, &c.Subject, &c.BodyText, &c.BodyHTML); err != nil {
			return nil, fmt.Errorf("scan claimed: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Mark records the outcome of a delivery attempt. status is one of
// DeliverySent (success), DeliveryPending (transient — will be reclaimed),
// or DeliveryFailed (terminal). attempts is always incremented; sent_at is
// stamped only on success.
func (s *Store) Mark(ctx context.Context, deliveryID, status, errMsg string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE broadcast_deliveries
		SET status = $2,
		    attempts = attempts + 1,
		    error = $3,
		    sent_at = CASE WHEN $2 = $4 THEN now() ELSE sent_at END,
		    updated_at = now()
		WHERE id = $1`,
		deliveryID, status, errMsg, DeliverySent,
	)
	if err != nil {
		return fmt.Errorf("mark delivery: %w", err)
	}
	return nil
}

// FinalizeIfDone flips the parent to 'complete' once no delivery remains
// pending or sending. Per-recipient failures stay visible on their rows.
func (s *Store) FinalizeIfDone(ctx context.Context, broadcastID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE broadcasts b
		SET status = $2
		WHERE b.id = $1
		  AND b.status <> $2
		  AND NOT EXISTS (
		      SELECT 1 FROM broadcast_deliveries d
		      WHERE d.broadcast_id = b.id AND d.status IN ($3, $4)
		  )`,
		broadcastID, BroadcastComplete, DeliveryPending, DeliverySending,
	)
	if err != nil {
		return fmt.Errorf("finalize broadcast: %w", err)
	}
	return nil
}

// ResetOrphanedSending returns any rows stuck in 'sending' (a worker died
// mid-flight) back to 'pending' so they get reclaimed. Returns the count.
func (s *Store) ResetOrphanedSending(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE broadcast_deliveries
		SET status = $1, updated_at = now()
		WHERE status = $2`,
		DeliveryPending, DeliverySending,
	)
	if err != nil {
		return 0, fmt.Errorf("reset orphaned: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Summary is a broadcast with aggregate per-status delivery counts.
type Summary struct {
	ID            string    `json:"id"`
	Subject       string    `json:"subject"`
	Recipients    string    `json:"recipients"`
	SourceAddress string    `json:"source_address"`
	Status        string    `json:"status"`
	Total         int       `json:"total"`
	Sent          int       `json:"sent"`
	Failed        int       `json:"failed"`
	Pending       int       `json:"pending"`
	SentAt        time.Time `json:"sent_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// List returns the club's broadcasts newest-first with delivery counts.
func (s *Store) List(ctx context.Context, clubID string) ([]Summary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT b.id, b.subject, b.recipients, COALESCE(b.source_address, ''), b.status,
		       COUNT(d.id) AS total,
		       COUNT(*) FILTER (WHERE d.status = $2) AS sent,
		       COUNT(*) FILTER (WHERE d.status = $3) AS failed,
		       COUNT(*) FILTER (WHERE d.status IN ($4, $5)) AS pending,
		       b.sent_at, b.created_at
		FROM broadcasts b
		LEFT JOIN broadcast_deliveries d ON d.broadcast_id = b.id
		WHERE b.club_id = $1
		GROUP BY b.id
		ORDER BY b.created_at DESC`,
		clubID, DeliverySent, DeliveryFailed, DeliveryPending, DeliverySending,
	)
	if err != nil {
		return nil, fmt.Errorf("list broadcasts: %w", err)
	}
	defer rows.Close()

	out := make([]Summary, 0)
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.ID, &s.Subject, &s.Recipients, &s.SourceAddress, &s.Status,
			&s.Total, &s.Sent, &s.Failed, &s.Pending, &s.SentAt, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeliveryRow is one per-recipient delivery in a broadcast detail view.
type DeliveryRow struct {
	ID       string     `json:"id"`
	Email    string     `json:"email"`
	Status   string     `json:"status"`
	Attempts int        `json:"attempts"`
	Error    string     `json:"error"`
	SentAt   *time.Time `json:"sent_at"`
}

// Detail is a broadcast plus its per-recipient delivery rows.
type Detail struct {
	Summary
	BodyText   string        `json:"body_text"`
	BodyHTML   string        `json:"body_html"`
	Deliveries []DeliveryRow `json:"deliveries"`
}

// Get returns one broadcast (club-scoped) with its delivery rows, or
// pgx.ErrNoRows if it doesn't belong to the club.
func (s *Store) Get(ctx context.Context, clubID, id string) (*Detail, error) {
	var d Detail
	err := s.db.QueryRow(ctx, `
		SELECT b.id, b.subject, b.recipients, COALESCE(b.source_address, ''), b.status,
		       b.body, COALESCE(b.body_html, ''),
		       COUNT(dd.id) AS total,
		       COUNT(*) FILTER (WHERE dd.status = $3) AS sent,
		       COUNT(*) FILTER (WHERE dd.status = $4) AS failed,
		       COUNT(*) FILTER (WHERE dd.status IN ($5, $6)) AS pending,
		       b.sent_at, b.created_at
		FROM broadcasts b
		LEFT JOIN broadcast_deliveries dd ON dd.broadcast_id = b.id
		WHERE b.id = $1 AND b.club_id = $2
		GROUP BY b.id`,
		id, clubID, DeliverySent, DeliveryFailed, DeliveryPending, DeliverySending,
	).Scan(&d.ID, &d.Subject, &d.Recipients, &d.SourceAddress, &d.Status,
		&d.BodyText, &d.BodyHTML,
		&d.Total, &d.Sent, &d.Failed, &d.Pending, &d.SentAt, &d.CreatedAt)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, email, status, attempts, error, sent_at
		FROM broadcast_deliveries
		WHERE broadcast_id = $1
		ORDER BY email`,
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()

	d.Deliveries = make([]DeliveryRow, 0)
	for rows.Next() {
		var r DeliveryRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Status, &r.Attempts, &r.Error, &r.SentAt); err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		d.Deliveries = append(d.Deliveries, r)
	}
	return &d, rows.Err()
}

// RequeueFailed resets a broadcast's terminal-failed deliveries back to
// pending (and the parent back to pending) so the worker retries them.
// Returns the number of deliveries re-queued. Club-scoped.
func (s *Store) RequeueFailed(ctx context.Context, clubID, id string) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Guard club ownership before touching delivery rows.
	var owns bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM broadcasts WHERE id = $1 AND club_id = $2)`,
		id, clubID,
	).Scan(&owns); err != nil {
		return 0, fmt.Errorf("ownership check: %w", err)
	}
	if !owns {
		return 0, pgx.ErrNoRows
	}

	tag, err := tx.Exec(ctx, `
		UPDATE broadcast_deliveries
		SET status = $2, error = '', updated_at = now()
		WHERE broadcast_id = $1 AND status = $3`,
		id, DeliveryPending, DeliveryFailed,
	)
	if err != nil {
		return 0, fmt.Errorf("requeue failed: %w", err)
	}
	n := tag.RowsAffected()

	if n > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE broadcasts SET status = $2 WHERE id = $1`,
			id, BroadcastPending,
		); err != nil {
			return 0, fmt.Errorf("reset broadcast status: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return n, nil
}
