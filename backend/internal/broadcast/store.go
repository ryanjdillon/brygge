// Package broadcast holds the persistence layer for the shared bulk-send
// delivery queue: one row per recipient in the `deliveries` table that a
// background worker drains, throttled and retried. It is shared by two
// producers keyed on a delivery `kind`: broadcasts (a parent `broadcasts`
// row fanned out to members) and invoice reminders (each pointing at an
// `invoices` row with a per-row JSONB payload). Despite the package name,
// the store and worker are kind-agnostic; the broadcast surface just
// happens to be the original tenant.
package broadcast

import (
	"context"
	"encoding/json"
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

// Delivery kinds — the discriminator the worker dispatches on.
const (
	KindBroadcast       = "broadcast"
	KindInvoiceReminder = "invoice_reminder"
)

// Broadcast (parent) statuses.
const (
	BroadcastPending  = "pending"
	BroadcastComplete = "complete"
)

// Store wraps the pool with delivery-queue queries.
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
	// Attachments is the combined JMAP attach body parts (attachments +
	// inline images) re-attached to every fanned-out delivery. Nil if none.
	Attachments []map[string]any
}

// Recipient is one resolved target for a bulk send. UserID is nil for
// ad-hoc addresses that don't map to a member.
type Recipient struct {
	UserID *string
	Email  string
}

// Enqueue inserts the parent broadcast plus one pending broadcast-kind
// delivery per recipient in a single transaction and returns the
// broadcast id.
func (s *Store) Enqueue(ctx context.Context, n New, recipients []Recipient) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var attachmentsJSON []byte
	if len(n.Attachments) > 0 {
		attachmentsJSON, err = json.Marshal(n.Attachments)
		if err != nil {
			return "", fmt.Errorf("marshal attachments: %w", err)
		}
	}

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO broadcasts (club_id, subject, body, body_html, recipients, source_address, sent_by, status, attachments)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`,
		n.ClubID, n.Subject, n.BodyText, n.BodyHTML, n.Recipients, n.SourceAddress, n.SentBy, BroadcastPending, attachmentsJSON,
	).Scan(&id); err != nil {
		return "", fmt.Errorf("insert broadcast: %w", err)
	}

	batch := &pgx.Batch{}
	for _, r := range recipients {
		batch.Queue(`
			INSERT INTO deliveries (kind, broadcast_id, club_id, user_id, email, status)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			KindBroadcast, id, n.ClubID, r.UserID, r.Email, DeliveryPending)
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

// InvoiceDelivery is one invoice-reminder row to enqueue. Payload carries
// the send context (locale, club name, actor, ...) the worker needs once
// the originating request is gone; it is opaque to the store.
type InvoiceDelivery struct {
	InvoiceID string
	Email     string
	UserID    *string
	Payload   json.RawMessage
}

// reminderResendCooldown is how long a successfully-sent reminder blocks a
// re-send of the SAME invoice. It exists only to swallow accidental
// double-clicks / batch re-runs where the queue already drained — a deliberate
// reminder later (the normal case: chasing an invoice that stayed unpaid) must
// go through. In-flight rows (pending/sending) always block regardless of age.
const reminderResendCooldown = "10 minutes"

// EnqueueInvoiceReminders inserts one pending 'invoice_reminder' delivery per
// item. Idempotent against accidental re-runs: an invoice with an in-flight
// (pending/sending) reminder, or one sent within reminderResendCooldown, is
// skipped so a double-click can't double-send. A reminder sent longer ago no
// longer blocks — otherwise an invoice could only ever be reminded once.
// Returns the count enqueued and the invoice ids that were skipped.
func (s *Store) EnqueueInvoiceReminders(ctx context.Context, clubID string, items []InvoiceDelivery) (enqueued int, skipped []string, err error) {
	for _, it := range items {
		tag, xerr := s.db.Exec(ctx, `
			INSERT INTO deliveries (kind, invoice_id, club_id, user_id, email, status, payload)
			SELECT $1, $2, $3, $4, $5, $6, $7
			WHERE NOT EXISTS (
				SELECT 1 FROM deliveries
				WHERE invoice_id = $2 AND kind = $1
				  AND (
				    status IN ($8, $9)
				    OR (status = $10 AND COALESCE(sent_at, updated_at, created_at) > now() - $11::interval)
				  )
			)`,
			KindInvoiceReminder, it.InvoiceID, clubID, it.UserID, it.Email, DeliveryPending, it.Payload,
			DeliveryPending, DeliverySending, DeliverySent, reminderResendCooldown,
		)
		if xerr != nil {
			return enqueued, skipped, fmt.Errorf("insert reminder delivery: %w", xerr)
		}
		if tag.RowsAffected() == 1 {
			enqueued++
		} else {
			skipped = append(skipped, it.InvoiceID)
		}
	}
	return enqueued, skipped, nil
}

// Claimed is one pending delivery flipped to 'sending'. It carries only the
// generic queue fields plus the kind-specific parent ids and payload; the
// per-kind Sender loads whatever else it needs (broadcast content, invoice
// row) itself.
type Claimed struct {
	DeliveryID string
	Kind       string
	ClubID     string
	Email      string
	Attempts   int
	// UserID is the recipient's member id, nil for ad-hoc addresses.
	UserID *string
	// Exactly one parent id is set per kind (enforced by a CHECK).
	BroadcastID *string
	InvoiceID   *string
	// Payload is the opaque per-row JSONB (invoice reminders use it; nil for
	// broadcasts).
	Payload json.RawMessage
}

// ClaimPending atomically claims up to limit pending deliveries of ANY
// kind, flipping them to 'sending' so a concurrent worker can't grab the
// same rows (FOR UPDATE SKIP LOCKED).
func (s *Store) ClaimPending(ctx context.Context, limit int) ([]Claimed, error) {
	rows, err := s.db.Query(ctx, `
		WITH claimed AS (
			SELECT id
			FROM deliveries
			WHERE status = $1
			ORDER BY created_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), upd AS (
			UPDATE deliveries d
			SET status = $3, updated_at = now()
			FROM claimed
			WHERE d.id = claimed.id
			RETURNING d.id, d.kind, d.broadcast_id, d.invoice_id, d.club_id, d.user_id, d.email, d.attempts, d.payload
		)
		SELECT id, kind, broadcast_id, invoice_id, club_id, user_id, email, attempts, payload
		FROM upd`,
		DeliveryPending, limit, DeliverySending,
	)
	if err != nil {
		return nil, fmt.Errorf("claim pending: %w", err)
	}
	defer rows.Close()

	var out []Claimed
	for rows.Next() {
		var c Claimed
		var payload []byte
		if err := rows.Scan(&c.DeliveryID, &c.Kind, &c.BroadcastID, &c.InvoiceID,
			&c.ClubID, &c.UserID, &c.Email, &c.Attempts, &payload); err != nil {
			return nil, fmt.Errorf("scan claimed: %w", err)
		}
		c.Payload = payload
		out = append(out, c)
	}
	return out, rows.Err()
}

// BroadcastContent is the parent-broadcast content a broadcast-kind
// delivery renders into an outgoing message.
type BroadcastContent struct {
	SourceAddress string
	Subject       string
	BodyText      string
	BodyHTML      string
	Attachments   []map[string]any
}

// LoadBroadcastContent fetches the parent broadcast's send content. The
// broadcast Sender calls this per delivery instead of the old ClaimPending
// JOIN (which no longer applies now that invoice rows have no parent).
func (s *Store) LoadBroadcastContent(ctx context.Context, broadcastID string) (BroadcastContent, error) {
	var bc BroadcastContent
	var attachmentsJSON []byte
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(source_address, ''), subject, body, COALESCE(body_html, ''), attachments
		FROM broadcasts WHERE id = $1`,
		broadcastID,
	).Scan(&bc.SourceAddress, &bc.Subject, &bc.BodyText, &bc.BodyHTML, &attachmentsJSON)
	if err != nil {
		return bc, fmt.Errorf("load broadcast content: %w", err)
	}
	if len(attachmentsJSON) > 0 {
		if err := json.Unmarshal(attachmentsJSON, &bc.Attachments); err != nil {
			return bc, fmt.Errorf("unmarshal attachments: %w", err)
		}
	}
	return bc, nil
}

// Mark records the outcome of a delivery attempt. status is one of
// DeliverySent (success), DeliveryPending (transient — will be reclaimed),
// or DeliveryFailed (terminal). attempts is always incremented; sent_at is
// stamped only on success.
func (s *Store) Mark(ctx context.Context, deliveryID, status, errMsg string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE deliveries
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

// FinalizeIfDone flips the parent broadcast to 'complete' once no delivery
// remains pending or sending. Broadcast-specific; the worker calls it only
// for broadcast-kind deliveries. Per-recipient failures stay visible on
// their rows.
func (s *Store) FinalizeIfDone(ctx context.Context, broadcastID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE broadcasts b
		SET status = $2
		WHERE b.id = $1
		  AND b.status <> $2
		  AND NOT EXISTS (
		      SELECT 1 FROM deliveries d
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
// mid-flight) back to 'pending' so they get reclaimed. Kind-agnostic.
// Returns the count.
func (s *Store) ResetOrphanedSending(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE deliveries
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
// The join is broadcast-scoped (invoice-kind rows have no broadcast parent
// and are filtered out explicitly) so they never leak into the history UI.
func (s *Store) List(ctx context.Context, clubID string) ([]Summary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT b.id, b.subject, b.recipients, COALESCE(b.source_address, ''), b.status,
		       COUNT(d.id) AS total,
		       COUNT(*) FILTER (WHERE d.status = $2) AS sent,
		       COUNT(*) FILTER (WHERE d.status = $3) AS failed,
		       COUNT(*) FILTER (WHERE d.status IN ($4, $5)) AS pending,
		       b.sent_at, b.created_at
		FROM broadcasts b
		LEFT JOIN deliveries d ON d.broadcast_id = b.id AND d.kind = 'broadcast'
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
		LEFT JOIN deliveries dd ON dd.broadcast_id = b.id AND dd.kind = 'broadcast'
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
		FROM deliveries
		WHERE broadcast_id = $1 AND kind = 'broadcast'
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
		UPDATE deliveries
		SET status = $2, error = '', updated_at = now()
		WHERE broadcast_id = $1 AND kind = 'broadcast' AND status = $3`,
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
