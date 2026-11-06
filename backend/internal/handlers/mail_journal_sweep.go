package handlers

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/sesevents"
)

// JournalSweep writes Stalwart's delivery outcomes into email_sends. Only
// the SES event worker writes outcomes for SES sends; a Stalwart send would
// otherwise stay "submitted" forever, and the faktura badge and the member
// bounce flag would never see its bounce. It folds the same journal the
// delivery log reads (mail_journal.go), so it is legacy with it: remove this
// file with the brygge-mail-log units after the SES cutover (BRY-594).
type JournalSweep struct {
	db      *pgxpool.Pool
	journal mailJournal
	log     zerolog.Logger
	outcome metric.Int64Counter
}

const (
	// JournalSweepInterval is how often the sweep runs.
	JournalSweepInterval = 5 * time.Minute
	// journalSweepMaxRecipients caps the helper calls per run: each scans
	// 35 days of journal with a 20 s timeout, so five keep a run well inside
	// the interval.
	journalSweepMaxRecipients = 5
	// journalSweepWindow covers Stalwart's queue expiry (5 days by default,
	// not overridden in host.nix) with room to see the final event.
	journalSweepWindow = 7 * 24 * time.Hour
	// journalSweepMinAge leaves a fresh send to the sender: the row is
	// written before the relay is dialled and finished after it answers.
	journalSweepMinAge = 2 * time.Minute
	// journalSweepGiveUp drops a row the journal never matched: by then the
	// journal would have it, and the delivery log shows it as no_record.
	journalSweepGiveUp = time.Hour
	// maxOutcomeDetail caps the stored remote answer, like the sender's
	// error text.
	maxOutcomeDetail = 500
)

// journalSweepCandidateSQL selects the Stalwart rows still worth a journal
// read: no terminal outcome yet, inside the window, past their back-off
// (a quarter of the row's age, at most an hour), and not a row the journal
// has already failed to match for an hour.
const journalSweepCandidateSQL = `
	provider = 'stalwart' AND outcome_final_at IS NULL AND status <> 'failed'
	AND created_at >= now() - make_interval(secs => $1)
	AND created_at <= now() - make_interval(secs => $2)
	AND (outcome_checked_at IS NULL
	     OR outcome_checked_at < now() - least(interval '1 hour', (now() - created_at) / 4))
	AND NOT (status IN ('submitted', 'unknown') AND outcome_checked_at IS NOT NULL
	         AND created_at < now() - make_interval(secs => $3))`

// NewJournalSweep returns the sweep, or nil when this host has no mail-log
// helper (MailLogSocket unset). Call after telemetry.Setup.
func NewJournalSweep(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *JournalSweep {
	j := mailJournalFromConfig(cfg)
	if j == nil {
		return nil
	}
	return newJournalSweep(db, j, log, otel.Meter("brygge-api"))
}

func newJournalSweep(db *pgxpool.Pool, j mailJournal, log zerolog.Logger, meter metric.Meter) *JournalSweep {
	s := &JournalSweep{db: db, journal: j, log: log.With().Str("worker", "journal_sweep").Logger()}
	var err error
	// A separate counter from brygge.email.delivery, which stays SES-only so
	// SesDeliveryFailed keeps meaning what it says.
	if s.outcome, err = meter.Int64Counter("brygge.email.stalwart_outcome",
		metric.WithDescription("Delivery outcomes the journal sweep wrote for Stalwart sends, by kind, status and recipient domain class"),
		metric.WithUnit("{send}"),
	); err != nil {
		otel.Handle(err)
	}
	return s
}

// Run sweeps once at start, so a deploy does not leave outcomes waiting a
// full interval, then every interval until ctx ends. Runs are serial: a
// slow helper delays the next run instead of overlapping it.
func (s *JournalSweep) Run(ctx context.Context, interval time.Duration) {
	s.runLogged(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runLogged(ctx)
		}
	}
}

func (s *JournalSweep) runLogged(ctx context.Context) {
	if err := s.runOnce(ctx); err != nil && ctx.Err() == nil {
		s.log.Error().Err(err).Msg("journal sweep failed")
	}
}

// runOnce sweeps the recipients with candidate rows, never-checked and
// least recently checked first, then oldest first.
func (s *JournalSweep) runOnce(ctx context.Context) error {
	rows, err := s.db.Query(ctx,
		`SELECT lower(recipient)
		   FROM email_sends
		  WHERE `+journalSweepCandidateSQL+`
		  GROUP BY lower(recipient)
		  ORDER BY min(COALESCE(outcome_checked_at, '-infinity'::timestamptz)), min(created_at)
		  LIMIT $4`,
		journalSweepWindow.Seconds(), journalSweepMinAge.Seconds(), journalSweepGiveUp.Seconds(),
		journalSweepMaxRecipients)
	if err != nil {
		return fmt.Errorf("select sweep recipients: %w", err)
	}
	recipients := []string{}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return fmt.Errorf("scan sweep recipient: %w", err)
		}
		recipients = append(recipients, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read sweep recipients: %w", err)
	}
	for _, r := range recipients {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.sweepRecipient(ctx, r); err != nil {
			s.log.Error().Err(err).Msg("journal sweep: recipient skipped")
		}
	}
	return nil
}

// sweepRow is one Stalwart email_sends row of the recipient.
type sweepRow struct {
	id, kind, domain, status string
	createdAt                time.Time
	candidate                bool
}

func (s *JournalSweep) sweepRecipient(ctx context.Context, recipient string) error {
	// Every Stalwart row to the address takes part in the pairing, not only
	// the candidates: a finished send still owns its submission. A failed row
	// never does: the relay refused it before taking it, so it would only
	// steal the next send's submission.
	rows, err := s.db.Query(ctx,
		`SELECT id::text, kind, recipient_domain, status, created_at,
		        (`+journalSweepCandidateSQL+`)
		   FROM email_sends
		  WHERE provider = 'stalwart' AND status <> 'failed' AND lower(recipient) = $4
		    AND created_at >= now() - make_interval(secs => $1) - make_interval(secs => $5)
		  ORDER BY created_at, id`,
		journalSweepWindow.Seconds(), journalSweepMinAge.Seconds(), journalSweepGiveUp.Seconds(),
		recipient, sendMatchWindow.Seconds())
	if err != nil {
		return fmt.Errorf("load sends: %w", err)
	}
	var sends []sweepRow
	for rows.Next() {
		var r sweepRow
		if err := rows.Scan(&r.id, &r.kind, &r.domain, &r.status, &r.createdAt, &r.candidate); err != nil {
			rows.Close()
			return fmt.Errorf("scan send: %w", err)
		}
		r.createdAt = r.createdAt.UTC()
		sends = append(sends, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read sends: %w", err)
	}

	// A helper error leaves every row unstamped, so the next run retries it.
	events, err := s.journal.Events(ctx, recipient)
	if err != nil {
		return fmt.Errorf("read mail journal: %w", err)
	}
	matched := pairSweepRows(sends, groupSubmissions(events))

	var unmatched []string
	for i, row := range sends {
		if !row.candidate {
			continue
		}
		sub, ok := matched[i]
		if !ok {
			unmatched = append(unmatched, row.id)
			continue
		}
		if err := s.writeOutcome(ctx, row, sub); err != nil {
			return err
		}
	}
	if len(unmatched) > 0 {
		if _, err := s.db.Exec(ctx,
			`UPDATE email_sends SET outcome_checked_at = now() WHERE id = ANY($1::uuid[])`,
			unmatched); err != nil {
			return fmt.Errorf("stamp unmatched sends: %w", err)
		}
	}
	return nil
}

// pairSweepRows pairs rows with submissions in order: each row, oldest
// first, takes the earliest unclaimed submission from a second before its
// created_at to sendMatchWindow after it. Not matchSends' closest-pair rule:
// created_at is stamped before the relay is dialled, so a second send to the
// same address lands just after the first one's submission and would steal
// it. Submissions the app did not make (board replies from a shared
// mailbox) stay unclaimed.
func pairSweepRows(rows []sweepRow, subs []*mailSubmission) map[int]*mailSubmission {
	sorted := append([]*mailSubmission(nil), subs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].SubmittedAt.Before(sorted[j].SubmittedAt) })
	claimed := make([]bool, len(sorted))
	out := map[int]*mailSubmission{}
	for i, row := range rows {
		from, to := row.createdAt.Add(-time.Second), row.createdAt.Add(sendMatchWindow)
		for j, sub := range sorted {
			if claimed[j] || sub.SubmittedAt.Before(from) {
				continue
			}
			if sub.SubmittedAt.After(to) {
				break
			}
			claimed[j] = true
			out[i] = sub
			break
		}
	}
	return out
}

// writeOutcome stores the fold's result for a matched row. The write is a
// compare-and-set on the status read, not a rank advance: the fold re-reads
// the whole history, so a 5xx attempt that a later MX accepts must move the
// row from rejected to accepted. A row whose status moved in the meantime is
// left for the next run.
func (s *JournalSweep) writeOutcome(ctx context.Context, row sweepRow, sub *mailSubmission) error {
	out := sub.outcome()
	status := out.Status
	if status == deliveryQueued && row.status != deliverySubmitted && row.status != deliveryUnknown {
		// The match proves the relay has the message, which only says more
		// than "submitted" or "unknown"; over anything else it says nothing,
		// so the row keeps its status, time and detail.
		if _, err := s.db.Exec(ctx,
			`UPDATE email_sends SET outcome_checked_at = now() WHERE id = $1 AND status = $2`,
			row.id, row.status); err != nil {
			return fmt.Errorf("stamp send: %w", err)
		}
		return nil
	}
	at := sub.SubmittedAt
	if out.At != nil {
		at = *out.At
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE email_sends
		    SET status             = $3,
		        last_event_at      = $4,
		        outcome_detail     = NULLIF($5, ''),
		        outcome_final_at   = CASE WHEN $6 THEN now() ELSE outcome_final_at END,
		        outcome_checked_at = now(),
		        updated_at         = now()
		  WHERE id = $1 AND status = $2`,
		row.id, row.status, status, at, outcomeDetail(sub, out), out.Final)
	if err != nil {
		return fmt.Errorf("write send outcome: %w", err)
	}
	if tag.RowsAffected() == 1 && status != row.status && s.outcome != nil {
		s.outcome.Add(ctx, 1, metric.WithAttributes(
			attribute.String("kind", metricKind(row.kind)),
			attribute.String("result", status),
			attribute.String("recipient_domain_class", sesevents.DomainClass(row.domain)),
		))
	}
	return nil
}

// outcomeDetail is the remote server's answer as "<code> <detail>". A DSN
// or failure event only paraphrases the refusal, so a failure without a code
// of its own takes the latest attempt error's.
func outcomeDetail(sub *mailSubmission, out mailOutcome) string {
	code, detail := out.SMTPCode, out.Detail
	if code == 0 && out.Status != deliveryAccepted && out.Status != deliveryQueued {
		if e, ok := sub.lastAttemptError(); ok {
			code, detail = e.code(), e.detail()
		}
	}
	text := detail
	if code != 0 {
		text = strconv.Itoa(code) + " " + detail
	}
	return truncateUTF8(strings.TrimSpace(text), maxOutcomeDetail)
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary, so the stored
// text stays valid UTF-8.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

// metricKind keeps the kind attribute inside sesevents' closed set.
func metricKind(kind string) string {
	for _, k := range sesevents.Kinds {
		if kind == k {
			return kind
		}
	}
	return "other"
}
