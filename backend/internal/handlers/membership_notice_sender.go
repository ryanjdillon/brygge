package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/mail"
)

// errNoBoardAddress is a board notice with nowhere to go: the club has no
// notification address, no general shared mailbox and no contact address.
// The worker fails it at once with this note, and RequeueUnaddressed puts it
// back in the queue once the club has an address, so no notice is lost.
var errNoBoardAddress = fmt.Errorf("%w: no board notification address configured", bcast.ErrUnsendable)

// boardKinds are the membership kinds that go to the board.
var boardKinds = []string{bcast.KindMembershipBoardNotice, bcast.KindMembershipJoinRefunded, bcast.KindMembershipGrantWithheld}

// MembershipNoticeSender is the broadcast.Sender for the membership kinds
// (BRY-630): the new member's welcome, the board's new-member notice, and the
// board's notices of a refunded join payment and of a withheld grant. The grant or the refund queued
// the row in its own transaction; this runs after the commit and renders the
// email from the member's current row, so nothing personal is copied into the
// queue.
type MembershipNoticeSender struct {
	db    *pgxpool.Pool
	cfg   *config.Config
	email email.Sender
	log   zerolog.Logger
}

// NewMembershipNoticeSender returns the sender. mailer may be nil (no mail
// configured): the rows then fail at once with a note saying so, so a club
// that turns mail on later does not get a backlog of stale welcomes.
func NewMembershipNoticeSender(db *pgxpool.Pool, cfg *config.Config, mailer email.Sender, log zerolog.Logger) *MembershipNoticeSender {
	return &MembershipNoticeSender{db: db, cfg: cfg, email: mailer, log: log.With().Str("sender", "membership").Logger()}
}

// Send renders and sends one membership email. A row that cannot be sent (the
// member was erased since, the member has no address, or no mail is
// configured) fails with a note saying why; it is never recorded as sent.
func (s *MembershipNoticeSender) Send(ctx context.Context, c bcast.Claimed) error {
	// A withheld grant with no payer (BRY-631: a capture on a join order
	// whose user is gone) is still the board's to see; it names no member.
	userless := c.UserID == nil && c.Kind == bcast.KindMembershipGrantWithheld
	if c.UserID == nil && !userless {
		return fmt.Errorf("%w: the member no longer exists", bcast.ErrUnsendable)
	}
	if s.email == nil {
		return fmt.Errorf("%w: no mail transport configured", bcast.ErrUnsendable)
	}
	firstName, name, addr, lang, clubName, err := s.loadRecipient(ctx, c, userless)
	if err != nil {
		return err
	}
	locale := email.JoinLocale(lang)
	base := publicSiteBase(s.cfg)
	memberURL := noticeMemberURL(base, c.UserID)

	var (
		to, subject, body string
		kind              email.Kind
	)
	switch c.Kind {
	case bcast.KindMembershipWelcome:
		if addr == "" {
			return fmt.Errorf("%w: the member has no email address", bcast.ErrUnsendable)
		}
		to, kind = addr, email.KindMembershipWelcome
		subject = email.MembershipWelcomeSubject(locale, clubName)
		body = email.MembershipWelcomeBody(locale, clubName, firstName, base+"/portal", base+"/portal/waiting-list")
	case bcast.KindMembershipBoardNotice, bcast.KindMembershipJoinRefunded, bcast.KindMembershipGrantWithheld:
		to, err = boardNotificationAddress(ctx, s.db, s.cfg, c.ClubID)
		if err != nil {
			return fmt.Errorf("resolve board address: %w", err)
		}
		if to == "" {
			return errNoBoardAddress
		}
		switch c.Kind {
		case bcast.KindMembershipBoardNotice:
			kind = email.KindMembershipBoardNotice
			subject = email.MembershipBoardNoticeSubject(locale, name)
			body = email.MembershipBoardNoticeBody(locale, clubName, name, memberURL)
		case bcast.KindMembershipJoinRefunded:
			kind = email.KindMembershipJoinRefunded
			subject = email.MembershipJoinRefundedSubject(locale, name)
			body = email.MembershipJoinRefundedBody(locale, clubName, name, memberURL)
		default:
			var p struct {
				Reason string `json:"reason"`
			}
			if len(c.Payload) > 0 {
				if err := json.Unmarshal(c.Payload, &p); err != nil {
					return fmt.Errorf("decode withheld payload: %w", err)
				}
			}
			kind = email.KindMembershipGrantWithheld
			subject = email.MembershipGrantWithheldSubject(locale, name)
			body = email.MembershipGrantWithheldBody(locale, clubName, name, p.Reason, memberURL)
		}
	default:
		return fmt.Errorf("membership sender cannot send kind %q", c.Kind)
	}
	meta := email.SendMeta{ClubID: c.ClubID, Kind: kind}
	if c.UserID != nil {
		meta.UserID = *c.UserID
	}
	mailCtx := email.WithSendMeta(ctx, meta)
	return s.email.Send(mailCtx, to, subject, body)
}

// loadRecipient reads the member a notice is about and the club's language
// and name; a user-less notice reads the club only.
func (s *MembershipNoticeSender) loadRecipient(ctx context.Context, c bcast.Claimed, userless bool) (firstName, name, addr, lang, clubName string, err error) {
	if userless {
		err = s.db.QueryRow(ctx,
			`SELECT COALESCE(default_language, ''), name FROM clubs WHERE id = $1`, c.ClubID,
		).Scan(&lang, &clubName)
	} else {
		err = s.db.QueryRow(ctx,
			`SELECT COALESCE(u.first_name, ''),
			        COALESCE(NULLIF(trim(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''), u.full_name, ''),
			        COALESCE(u.email, ''), COALESCE(c.default_language, ''), c.name
			   FROM users u JOIN clubs c ON c.id = u.club_id
			  WHERE u.id = $1 AND u.club_id = $2`,
			*c.UserID, c.ClubID,
		).Scan(&firstName, &name, &addr, &lang, &clubName)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", "", "", fmt.Errorf("%w: the member no longer exists", bcast.ErrUnsendable)
	}
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("load member: %w", err)
	}
	if s.cfg != nil && s.cfg.ClubName != "" {
		clubName = s.cfg.ClubName
	}
	return firstName, name, addr, lang, clubName, nil
}

// noticeMemberURL opens the member in the admin member list, or the list when
// the notice names none.
func noticeMemberURL(base string, userID *string) string {
	if userID == nil {
		return base + "/admin/users"
	}
	return base + "/admin/users?user=" + url.QueryEscape(*userID)
}

// boardNotificationAddress is where the club's membership notices go, first
// match wins:
//  1. the club's own setting, clubs.board_notification_email;
//  2. the deployment's general shared mailbox: the board-mailbox spec entry
//     marked from_brand_only (post@ by convention), for the deployment's own
//     club only;
//  3. the contact-form recipient: the secretary, else the chair.
//
// "" when none is set. An address that is not a bare mailbox counts as unset,
// as for the contact form.
func boardNotificationAddress(ctx context.Context, q pgxQuerier, cfg *config.Config, clubID string) (string, error) {
	var slug, setting, secretary, chairman string
	err := q.QueryRow(ctx,
		`SELECT slug, board_notification_email, COALESCE(secretary_email, ''), COALESCE(chairman_email, '')
		   FROM clubs WHERE id = $1`,
		clubID,
	).Scan(&slug, &setting, &secretary, &chairman)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if addr, ok := parseBareEmail(setting); ok {
		return addr, nil
	}
	if addr := generalBoardMailbox(cfg, slug); addr != "" {
		return addr, nil
	}
	return pickContactRecipient(secretary, chairman), nil
}

// generalBoardMailbox is the deployment's general shared mailbox from the
// board-mailbox spec (BRYGGE_MAILBOXES_PATH), "" when there is none or the
// spec cannot be read. The spec is deployment-wide and a deployment serves
// one club (CLUB_SLUG), so the mailbox is that club's alone: another club in
// the same database gets "" and falls through to its own addresses.
func generalBoardMailbox(cfg *config.Config, clubSlug string) string {
	if cfg == nil || cfg.BoardMailboxesPath == "" || cfg.ClubSlug == "" || cfg.ClubSlug != clubSlug {
		return ""
	}
	specs, err := mail.LoadSpec(cfg.BoardMailboxesPath)
	if err != nil {
		return ""
	}
	for _, sp := range specs {
		if !sp.FromBrandOnly {
			continue
		}
		if addr, ok := parseBareEmail(sp.Address); ok {
			return addr
		}
	}
	return ""
}

// RequeueUnaddressed returns to the queue every board notice that failed for
// want of an address, in each club that has one now, and reports how many.
// The hourly sweep runs it, so a notice queued before the board's address was
// set is sent once it is.
func (s *MembershipNoticeSender) RequeueUnaddressed(ctx context.Context) (int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT club_id::text FROM deliveries
		  WHERE kind = ANY($1) AND status = $2 AND error = $3`,
		boardKinds, bcast.DeliveryFailed, errNoBoardAddress.Error(),
	)
	if err != nil {
		return 0, fmt.Errorf("list unaddressed board notices: %w", err)
	}
	clubs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("list unaddressed board notices: %w", err)
	}
	var n int64
	for _, clubID := range clubs {
		addr, err := boardNotificationAddress(ctx, s.db, s.cfg, clubID)
		if err != nil {
			return n, fmt.Errorf("resolve board address: %w", err)
		}
		if addr == "" {
			continue
		}
		tag, err := s.db.Exec(ctx,
			`UPDATE deliveries SET status = $4, attempts = 0, error = '', updated_at = now()
			  WHERE club_id = $1 AND kind = ANY($2) AND status = $3 AND error = $5`,
			clubID, boardKinds, bcast.DeliveryFailed, bcast.DeliveryPending, errNoBoardAddress.Error(),
		)
		if err != nil {
			return n, fmt.Errorf("requeue board notices: %w", err)
		}
		n += tag.RowsAffected()
	}
	return n, nil
}
