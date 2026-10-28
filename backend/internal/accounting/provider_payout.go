package accounting

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// A provider payout deposit is a bank row that carries a payment provider's
// payout: a Vipps settlement ("Utb. <n> Vippsnr <msn>") or a Stripe payout
// (the club's stripe_payout_descriptor). Its amount is a sum of charges the
// provider's own postings already booked to a clearing account, so the only
// correct booking is the payout clearing (ClearVippsPayout,
// clearStripePayout). Every path that books or suggests bank rows on other
// evidence leaves such a row alone: partner hints, mapping rules, bank
// auto-assign and price-item suggestions all ask providerPayouts.
//
// The Vipps half also has a SQL form (vippsPayoutRowSQL), because its
// pattern is fixed and translates to Postgres exactly. The Stripe half is a
// per-club Go regular expression (RE2). Postgres' ARE dialect is not RE2
// (embedded flags such as (?i) only at the very start, locale-dependent
// classes like \d, back-references RE2 rejects), so the descriptor is never
// evaluated in SQL: the SQL sites select with the Vipps fragment and filter
// the rows they loaded through providerPayouts.

// Provider payout kinds (BankRowSummary.ProviderPayout).
const (
	ProviderPayoutVipps  = "vipps"
	ProviderPayoutStripe = "stripe"
)

// defaultStripePayoutDescriptor is clubs.stripe_payout_descriptor's
// default, used in its place when the stored pattern does not compile.
const defaultStripePayoutDescriptor = `(?i)stripe`

// vippsPayoutRowSQL is true for a Vipps settlement deposit, over the bank row
// alias a. It is VippsSettlementPattern in Postgres syntax.
func vippsPayoutRowSQL(a string) string {
	return a + `.description ~* 'Utb\.\s*[0-9]+\s+Vippsnr\s+[0-9]+'`
}

// providerPayouts recognises a club's provider payout deposits. The zero
// value recognises Vipps settlements only.
type providerPayouts struct {
	// stripe is the club's payout descriptor, or nil when the club does not
	// take payments through Stripe.
	stripe *regexp.Regexp
}

// kind returns which provider paid out the row ("" for none). A Stripe
// payout is incoming money whose description or counterpart matches the
// descriptor; an outgoing row naming Stripe (a debit for a negative
// balance, a bill paid to Stripe) is not a payout and stays bookable.
// The descriptor alone decides, not an open payout of the same amount: a
// deposit Stripe paid out must never be booked to anything but the
// clearing account, even before its payout is synced.
func (p providerPayouts) kind(description, counterpart string, cents int64) string {
	if VippsSettlementPattern.MatchString(description) {
		return ProviderPayoutVipps
	}
	if p.stripe != nil && cents > 0 &&
		(p.stripe.MatchString(description) || p.stripe.MatchString(counterpart)) {
		return ProviderPayoutStripe
	}
	return ""
}

// is reports whether the row is any provider's payout.
func (p providerPayouts) is(description, counterpart string, cents int64) bool {
	return p.kind(description, counterpart, cents) != ""
}

// stripeOnly reports a Stripe payout, for SQL sites whose query already
// left Vipps settlements out with vippsPayoutRowSQL.
func (p providerPayouts) stripeOnly(description, counterpart string, cents int64) bool {
	return p.kind(description, counterpart, cents) == ProviderPayoutStripe
}

// loadProviderPayouts reads the club's Stripe payout descriptor. Stripe is
// recognised only for a club that takes payments through it (a Stripe
// payment attempt, payout or balance row exists), so a club that never
// used Stripe keeps booking a deposit that merely mentions it.
func loadProviderPayouts(ctx context.Context, q pgxQuerier, clubID string) (providerPayouts, error) {
	var descriptor string
	var uses bool
	err := q.QueryRow(ctx,
		`SELECT c.stripe_payout_descriptor,
		        EXISTS (SELECT 1 FROM payment_attempts a WHERE a.club_id = c.id AND a.provider = 'stripe')
		     OR EXISTS (SELECT 1 FROM stripe_payouts p WHERE p.club_id = c.id)
		     OR EXISTS (SELECT 1 FROM stripe_balance_transactions t WHERE t.club_id = c.id)
		   FROM clubs c WHERE c.id = $1`,
		clubID,
	).Scan(&descriptor, &uses)
	if errors.Is(err, pgx.ErrNoRows) {
		return providerPayouts{}, errors.New("club not found")
	}
	if err != nil {
		return providerPayouts{}, fmt.Errorf("loading stripe payout descriptor: %w", err)
	}
	if !uses {
		return providerPayouts{}, nil
	}
	return providerPayouts{stripe: compileStripeDescriptor(descriptor)}, nil
}

// ErrProviderPayoutDeposit is matched (errors.Is) by every
// ProviderPayoutDepositError.
var ErrProviderPayoutDeposit = errors.New("bank row is a provider payout deposit")

// ProviderPayoutDepositError refuses a manual assign of a provider payout
// deposit: booking it to a faktura or an account would credit money the
// provider's own postings already credited, and the payout would never
// clear. Tildel offers no such assign; this is the server's half of that
// rule, for a stale page or a direct API call. There is no override: a
// deposit that is genuinely not a payout is dismissed, or the club's
// descriptor corrected.
type ProviderPayoutDepositError struct {
	RowID string
	// Kind is ProviderPayoutVipps or ProviderPayoutStripe.
	Kind string
}

func (e *ProviderPayoutDepositError) Error() string {
	return fmt.Sprintf("bank row %s is a %s payout deposit; only its payout clearing books it", e.RowID, e.Kind)
}

// Is makes errors.Is(err, ErrProviderPayoutDeposit) true.
func (e *ProviderPayoutDepositError) Is(target error) bool {
	return target == ErrProviderPayoutDeposit
}

// assignRowText is what refuseProviderPayouts needs of a loaded bank row.
type assignRowText struct {
	id, description, counterpart string
	cents                        int64
}

// refuseProviderPayouts is the manual assigns' guard: it loads the club's
// descriptor on q (the assign's own transaction, so a caller holding a lock
// never waits for a pool connection) and refuses the first row that is a
// provider payout deposit.
func refuseProviderPayouts(ctx context.Context, q pgxQuerier, clubID string, rows ...assignRowText) error {
	payouts, err := loadProviderPayouts(ctx, q, clubID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if kind := payouts.kind(r.description, r.counterpart, r.cents); kind != "" {
			return &ProviderPayoutDepositError{RowID: r.id, Kind: kind}
		}
	}
	return nil
}

// compileStripeDescriptor compiles a stored descriptor. The settings
// endpoint refuses an empty or invalid one, so the fallback only covers a
// value written some other way; falling back to the default keeps Stripe
// deposits out of the other bookings rather than letting every row (an
// empty pattern) or none match.
func compileStripeDescriptor(descriptor string) *regexp.Regexp {
	if strings.TrimSpace(descriptor) != "" {
		if re, err := regexp.Compile(descriptor); err == nil {
			return re
		}
	}
	return regexp.MustCompile(defaultStripePayoutDescriptor)
}
