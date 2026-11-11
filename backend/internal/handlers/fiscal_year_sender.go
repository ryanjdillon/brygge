package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
)

// errNoTreasurerAddress is a fiscal year notice with nowhere to go: the club
// has no treasurer address and no board notification address. The worker
// fails it at once with this note, and RequeueUnaddressed puts it back once
// the club has one, so the notice is not lost.
var errNoTreasurerAddress = fmt.Errorf("%w: no treasurer or board notification address configured", bcast.ErrUnsendable)

// errFiscalYearResolved drops a notice whose condition no longer holds by the
// time it is sent: the year was created or reopened, or the payments were
// booked or dismissed. Nothing is mailed. The hourly sweep re-arms exactly
// this failure when the condition comes back, so the text lives in accounting.
var errFiscalYearResolved = accounting.ErrFiscalYearResolved

// waitingYears is what the sender needs of accounting.Service.
type waitingYears interface {
	WaitingFiscalYears(ctx context.Context, clubID string) ([]accounting.WaitingFiscalYear, error)
}

// FiscalYearWaitingSender is the broadcast.Sender for
// accounting_fiscal_year_waiting (BRY-575): it tells the treasurer that
// payments wait for a fiscal year that is missing, closed or locked. The
// hourly sweep queues the row (accounting.NotifyWaitingFiscalYears); this
// reads the condition again when it sends and drops the row when it no
// longer holds, so the email always gives the live count.
type FiscalYearWaitingSender struct {
	db      *pgxpool.Pool
	cfg     *config.Config
	email   email.Sender
	waiting waitingYears
	log     zerolog.Logger
}

// NewFiscalYearWaitingSender returns the sender. mailer may be nil (no mail
// configured): the rows then fail at once with a note saying so.
func NewFiscalYearWaitingSender(db *pgxpool.Pool, cfg *config.Config, mailer email.Sender, waiting waitingYears, log zerolog.Logger) *FiscalYearWaitingSender {
	return &FiscalYearWaitingSender{db: db, cfg: cfg, email: mailer, waiting: waiting, log: log.With().Str("sender", "fiscal_year_waiting").Logger()}
}

// Send renders and sends one notice, or fails it with a note: no mail
// transport, nowhere to send it, or nothing waits any more.
func (s *FiscalYearWaitingSender) Send(ctx context.Context, c bcast.Claimed) error {
	if s.email == nil {
		return fmt.Errorf("%w: no mail transport configured", bcast.ErrUnsendable)
	}
	var p accounting.FiscalYearWaitingPayload
	if err := json.Unmarshal(c.Payload, &p); err != nil {
		return fmt.Errorf("%w: bad payload: %v", bcast.ErrUnsendable, err)
	}
	waiting, err := s.waiting.WaitingFiscalYears(ctx, c.ClubID)
	if err != nil {
		return fmt.Errorf("read waiting fiscal years: %w", err)
	}
	var live *accounting.WaitingFiscalYear
	for i := range waiting {
		if waiting[i].Ref(c.ClubID) == p.Ref {
			live = &waiting[i]
			break
		}
	}
	if live == nil {
		return errFiscalYearResolved
	}
	to, err := treasurerAddress(ctx, s.db, s.cfg, c.ClubID)
	if err != nil {
		return fmt.Errorf("resolve treasurer address: %w", err)
	}
	if to == "" {
		return errNoTreasurerAddress
	}
	var lang, clubName string
	if err := s.db.QueryRow(ctx,
		`SELECT COALESCE(default_language, ''), name FROM clubs WHERE id = $1`, c.ClubID,
	).Scan(&lang, &clubName); err != nil {
		return fmt.Errorf("load club: %w", err)
	}
	if s.cfg != nil && s.cfg.ClubName != "" {
		clubName = s.cfg.ClubName
	}
	n := email.FiscalYearWaiting{
		ClubName: clubName, Year: live.Year, State: live.State,
		Payments: live.Payments, Oldest: live.Oldest,
		URL: fiscalYearActionURL(publicSiteBase(s.cfg), *live),
	}
	locale := email.JoinLocale(lang)
	mailCtx := email.WithSendMeta(ctx, email.SendMeta{ClubID: c.ClubID, Kind: email.KindFiscalYearWaiting})
	return s.email.Send(mailCtx, to, email.FiscalYearWaitingSubject(locale, n), email.FiscalYearWaitingBody(locale, n))
}

// fiscalYearActionURL opens the Bilag page on the notice's action: the create
// confirm for a missing year, the reopen for a closed period, and only the
// period for a locked one, which cannot be reopened in the app.
func fiscalYearActionURL(base string, w accounting.WaitingFiscalYear) string {
	page := base + "/admin/accounting/journal"
	switch {
	case w.State == accounting.WaitingMissing || w.PeriodID == nil:
		return page + "?openPeriod=" + strconv.Itoa(w.Year)
	case w.State == accounting.WaitingClosed:
		return page + "?reopenPeriod=" + url.QueryEscape(*w.PeriodID)
	default:
		return page + "?period=" + url.QueryEscape(*w.PeriodID)
	}
}

// treasurerAddress is where the club's accounting notices go: the
// treasurer's address (clubs.treasurer_email, as for a card dispute), else
// the board notification address (boardNotificationAddress). "" when
// neither is set; an address that is not a bare mailbox counts as unset.
func treasurerAddress(ctx context.Context, q pgxQuerier, cfg *config.Config, clubID string) (string, error) {
	var treasurer string
	err := q.QueryRow(ctx, `SELECT COALESCE(treasurer_email, '') FROM clubs WHERE id = $1`, clubID).Scan(&treasurer)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if addr, ok := parseBareEmail(treasurer); ok {
		return addr, nil
	}
	return boardNotificationAddress(ctx, q, cfg, clubID)
}

// RequeueUnaddressed returns to the queue every fiscal year notice that failed
// for want of an address, in each club that has one now, and reports how
// many. The hourly sweep runs it.
func (s *FiscalYearWaitingSender) RequeueUnaddressed(ctx context.Context) (int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT club_id::text FROM deliveries
		  WHERE kind = $1 AND status = $2 AND error = $3`,
		bcast.KindAccountingFiscalYearWaiting, bcast.DeliveryFailed, errNoTreasurerAddress.Error(),
	)
	if err != nil {
		return 0, fmt.Errorf("list unaddressed fiscal year notices: %w", err)
	}
	clubs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("list unaddressed fiscal year notices: %w", err)
	}
	var n int64
	for _, clubID := range clubs {
		addr, err := treasurerAddress(ctx, s.db, s.cfg, clubID)
		if err != nil {
			return n, fmt.Errorf("resolve treasurer address: %w", err)
		}
		if addr == "" {
			continue
		}
		tag, err := s.db.Exec(ctx,
			`UPDATE deliveries SET status = $4, attempts = 0, error = '', updated_at = now()
			  WHERE club_id = $1 AND kind = $2 AND status = $3 AND error = $5`,
			clubID, bcast.KindAccountingFiscalYearWaiting, bcast.DeliveryFailed, bcast.DeliveryPending,
			errNoTreasurerAddress.Error(),
		)
		if err != nil {
			return n, fmt.Errorf("requeue fiscal year notices: %w", err)
		}
		n += tag.RowsAffected()
	}
	return n, nil
}
