package accounting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BRY-672: Tildel's «Par som overføring». A person selects the two rows of a
// transfer between the club's own bank accounts (one out, one in, the same
// amount) and books them as one posted bilag. It is the way back after a
// person undid an automatic transfer pair (ACC-13(b): the undo declines both
// rows, so transfer pairing never pairs them again) and for a transfer the
// detector did not pair (different dates, no account numbers).

// transferMaxDaysApart is the widest gap between a transfer's two dates a
// person may pair (BRY-380's reverse window).
const transferMaxDaysApart = 14

// ErrTransferRowCount refuses a pair request that does not name exactly two
// distinct rows. Handlers answer 400.
var ErrTransferRowCount = errors.New("vel nøyaktig to rader")

// Refusal codes of PairBankTransfer. The SPA words them in the treasurer's
// locale (admin.bankReconcile.transfer.refused.<code>).
const (
	TransferRowBooked       = "row_booked"
	TransferRowDismissed    = "row_dismissed"
	TransferRowRefundPaired = "row_refund_paired"
	TransferRowProvisional  = "row_provisional"
	TransferRowHasKID       = "row_has_kid"
	TransferSameAccount     = "transfer_same_account"
	TransferSameSign        = "transfer_same_sign"
	TransferAmountMismatch  = "transfer_amount_mismatch"
	TransferDatesApart      = "transfer_dates_apart"
	TransferCrossesPeriod   = "transfer_crosses_period"
	TransferPeriodClosed    = "period_closed"
	TransferPeriodLocked    = "period_locked"
)

// transferRefusalMsg is each code's Norwegian (nynorsk, like the group
// booking's errors) message.
var transferRefusalMsg = map[string]string{
	TransferRowBooked:       "ei av radene er alt bokført",
	TransferRowDismissed:    "ei av radene er avvist; avviste rader kan ikkje parast som overføring",
	TransferRowRefundPaired: "ei av radene er para som tilbakebetaling",
	TransferRowProvisional:  "ei av radene er foreløpig; eksporter kontoutskrifta på nytt",
	TransferRowHasKID:       "innbetalinga har KID-en til ein av fakturaene til klubben; tildel ho til fakturaen",
	TransferSameAccount:     "radene må høyra til ulike bankkontoar",
	TransferSameSign:        "den eine rada må gå ut og den andre inn",
	TransferAmountMismatch:  "beløpa må vera like store",
	TransferDatesApart:      fmt.Sprintf("radene ligg meir enn %d dagar frå kvarandre", transferMaxDaysApart),
	TransferCrossesPeriod:   "radene ligg i ulike rekneskapsår; bokfør dei kvar for seg",
	TransferPeriodClosed:    "rekneskapsperioden er lukka",
	TransferPeriodLocked:    "rekneskapsperioden er låst",
}

// TransferRefusalError is a pair request PairBankTransfer refuses. Handlers
// answer 409 {error: Msg, code: Code}.
type TransferRefusalError struct {
	Code string
	Msg  string
}

func (e *TransferRefusalError) Error() string { return e.Msg }

func transferRefusal(code string) *TransferRefusalError {
	return &TransferRefusalError{Code: code, Msg: transferRefusalMsg[code]}
}

// TransferPairResult is a transfer a person paired.
type TransferPairResult struct {
	JournalEntryID string `json:"journal_entry_id"`
	OutboundRowID  string `json:"outbound_row_id"`
	InboundRowID   string `json:"inbound_row_id"`
	// DebitAccount is the receiving bank account, CreditAccount the
	// sending one.
	DebitAccount  string  `json:"debit_account"`
	CreditAccount string  `json:"credit_account"`
	EntryDate     string  `json:"entry_date"`
	Amount        float64 `json:"amount"`
	// LiftedDecline is the rows whose auto_declined_at this booking cleared.
	LiftedDecline []string `json:"lifted_decline"`
}

// transferRow is a bank row as PairBankTransfer validates it.
type transferRow struct {
	id, desc, counterpart, kid, bankAcct          string
	date                                          time.Time
	cents                                         int64
	booked, dismissed, refundPaired, declined, pv bool
	// ourKID: the row carries the KID of one of the club's fakturas.
	ourKID bool
}

// PairBankTransfer books two bank rows as one transfer between the club's
// own bank accounts: debit the receiving account, credit the sending one,
// for the amount, dated on the later date. It is a person's booking: it
// ignores and clears a decline (auto_declined_at), may open the year
// (resolvePeriodIn), requires the period to be open, and posts at once.
//
// Everything runs in one transaction that locks both rows (FOR UPDATE OF
// bir, in id order) before any check, and reads every fact on it, so it
// books once against a concurrent detector, assign or KID cascade. A
// refusal is *TransferRefusalError (or ErrTransferRowCount,
// ErrBankRowNotFound, *ProviderPayoutDepositError, ErrReferencePeriod) and
// keeps nothing, not even a year it created on the way.
func (s *Service) PairBankTransfer(ctx context.Context, clubID, actorID string, rowIDs []string, description string) (*TransferPairResult, error) {
	ids, err := transferRowIDs(rowIDs)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	loaded, err := lockTransferRowsTx(ctx, tx, clubID, ids)
	if err != nil {
		return nil, err
	}
	if err := refuseProviderPayouts(ctx, tx, clubID,
		assignRowText{id: loaded[0].id, description: loaded[0].desc, counterpart: loaded[0].counterpart, cents: loaded[0].cents},
		assignRowText{id: loaded[1].id, description: loaded[1].desc, counterpart: loaded[1].counterpart, cents: loaded[1].cents},
	); err != nil {
		return nil, err
	}
	out, in, err := checkTransferRows(loaded[0], loaded[1])
	if err != nil {
		return nil, err
	}
	return s.bookPersonTransferTx(ctx, tx, clubID, actorID, loaded, out, in, description)
}

// transferRowIDs canonicalises the requested ids (uuid.Parse, so one id in
// two spellings counts once) and returns the two distinct ids in order. A
// malformed id is ErrBankRowNotFound, a count other than two
// ErrTransferRowCount.
func transferRowIDs(rowIDs []string) ([]string, error) {
	canonical := make([]string, 0, len(rowIDs))
	for _, id := range rowIDs {
		u, err := uuid.Parse(id)
		if err != nil {
			return nil, ErrBankRowNotFound
		}
		canonical = append(canonical, u.String())
	}
	ids := uniqueStrings(canonical)
	if len(ids) != 2 {
		return nil, ErrTransferRowCount
	}
	sort.Strings(ids)
	return ids, nil
}

// lockTransferRowsTx locks both rows (FOR UPDATE OF bir in id order, the
// import FOR SHARE) and loads them on tx, with whether an incoming row
// carries a faktura's KID. Fewer than two of the club's rows is
// ErrBankRowNotFound.
func lockTransferRowsTx(ctx context.Context, tx pgx.Tx, clubID string, ids []string) ([]transferRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT bir.id::text, bir.row_date, round(bir.amount * 100)::bigint, bir.description,
		        COALESCE(bir.counterpart, ''), COALESCE(bir.kid_number, ''), bi.bank_account_code,
		        bir.journal_entry_id IS NOT NULL,
		        bir.dismissed_at IS NOT NULL,
		        bir.refund_paired_with IS NOT NULL,
		        bir.auto_declined_at IS NOT NULL,
		        COALESCE((`+provisionalRowSQL("bir")+`), false)
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.club_id = $1 AND bir.id = ANY($2::uuid[])
		  ORDER BY bir.id
		    FOR UPDATE OF bir FOR SHARE OF bi`,
		clubID, ids,
	)
	if err != nil {
		return nil, fmt.Errorf("locking transfer rows: %w", err)
	}
	var loaded []transferRow
	for rows.Next() {
		var r transferRow
		if err := rows.Scan(&r.id, &r.date, &r.cents, &r.desc, &r.counterpart, &r.kid, &r.bankAcct,
			&r.booked, &r.dismissed, &r.refundPaired, &r.declined, &r.pv); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan transfer row: %w", err)
		}
		loaded = append(loaded, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("transfer rows: %w", err)
	}
	if len(loaded) != 2 {
		return nil, ErrBankRowNotFound
	}
	for i := range loaded {
		if loaded[i].ourKID, err = rowCarriesClubKIDIn(ctx, tx, clubID, loaded[i]); err != nil {
			return nil, err
		}
	}
	return loaded, nil
}

// bookPersonTransferTx resolves the period for the checked pair, books and
// posts it through bookTransferPairTx (transferLinkPerson) and commits tx.
func (s *Service) bookPersonTransferTx(
	ctx context.Context, tx pgx.Tx, clubID, actorID string, loaded []transferRow, out, in transferRow, description string,
) (*TransferPairResult, error) {
	earlier, later := out.date, in.date
	if earlier.After(later) {
		earlier, later = later, earlier
	}
	periodID, err := transferPeriodIn(ctx, tx, clubID, earlier, later)
	if err != nil {
		return nil, err
	}

	text := strings.TrimSpace(description)
	if text == "" {
		text = out.desc
	}
	pair := intraBankPair{
		r1ID: out.id, r2ID: in.id,
		debitCode: in.bankAcct, creditCode: out.bankAcct,
		desc: text, date: later, amount: float64(abs64(out.cents)) / 100,
	}
	entryID, err := bookTransferPairTx(ctx, tx, clubID, actorID, pair, periodID, transferLinkPerson,
		"Intern overføring: "+truncate(text, 200))
	switch {
	case errors.Is(err, errTransferRowTaken), errors.Is(err, errBankRowRaced):
		return nil, transferRefusal(TransferRowBooked)
	case errors.Is(err, ErrPeriodClosed):
		// Closed between the status read and the entry insert.
		return nil, transferRefusal(TransferPeriodClosed)
	case err != nil:
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing transfer: %w", err)
	}

	res := &TransferPairResult{
		JournalEntryID: entryID,
		OutboundRowID:  out.id,
		InboundRowID:   in.id,
		DebitAccount:   in.bankAcct,
		CreditAccount:  out.bankAcct,
		EntryDate:      later.Format("2006-01-02"),
		Amount:         pair.amount,
		LiftedDecline:  []string{},
	}
	for _, r := range loaded {
		if r.declined {
			res.LiftedDecline = append(res.LiftedDecline, r.id)
		}
	}
	return res, nil
}

// checkTransferRows refuses two rows that can't be one transfer and returns
// them as (outgoing, incoming). The order of the checks is the refusal
// order the SPA and the docs list.
func checkTransferRows(a, b transferRow) (out, in transferRow, err error) {
	for _, r := range []transferRow{a, b} {
		switch {
		case r.booked:
			return out, in, transferRefusal(TransferRowBooked)
		case r.dismissed:
			return out, in, transferRefusal(TransferRowDismissed)
		case r.refundPaired:
			return out, in, transferRefusal(TransferRowRefundPaired)
		case r.pv:
			return out, in, transferRefusal(TransferRowProvisional)
		}
	}
	if a.bankAcct == b.bankAcct {
		return out, in, transferRefusal(TransferSameAccount)
	}
	if (a.cents < 0) == (b.cents < 0) || a.cents == 0 || b.cents == 0 {
		return out, in, transferRefusal(TransferSameSign)
	}
	out, in = a, b
	if a.cents > 0 {
		out, in = b, a
	}
	// Money carrying the KID of one of the club's fakturas is a faktura
	// payment, never a transfer between the club's accounts.
	if in.ourKID {
		return out, in, transferRefusal(TransferRowHasKID)
	}
	if abs64(out.cents) != abs64(in.cents) {
		return out, in, transferRefusal(TransferAmountMismatch)
	}
	if daysBetween(out.date, in.date) > transferMaxDaysApart {
		return out, in, transferRefusal(TransferDatesApart)
	}
	return out, in, nil
}

// rowCarriesClubKIDIn reports whether an incoming row carries, in its KID
// column or its text, a valid Brygge KID that belongs to one of the club's
// fakturas (whatever its state). A valid check digit alone is not enough:
// a transfer's text can hold a number that happens to pass it.
func rowCarriesClubKIDIn(ctx context.Context, q dbtx, clubID string, r transferRow) (bool, error) {
	if r.cents <= 0 {
		return false, nil
	}
	var kids []string
	for _, k := range []string{strings.TrimSpace(r.kid), ExtractKIDFromDescription(r.desc)} {
		if isBryggeKID(k) {
			kids = append(kids, k)
		}
	}
	if len(kids) == 0 {
		return false, nil
	}
	var found bool
	if err := q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM invoices WHERE club_id = $1 AND kid_number = ANY($2::text[]))`,
		clubID, kids,
	).Scan(&found); err != nil {
		return false, fmt.Errorf("looking up the row's KID: %w", err)
	}
	return found, nil
}

// transferPeriodIn is the period a person's transfer books into, read on the
// pair's transaction: the later date's period, created when missing (a
// person's booking may open the year), which must also cover the earlier
// date and be open. A refusal rolls back a period created here with the
// transaction.
func transferPeriodIn(ctx context.Context, q dbtx, clubID string, earlier, later time.Time) (string, error) {
	periodID, status, err := resolvePeriodIn(ctx, q, clubID, later, "")
	if err != nil {
		return "", fmt.Errorf("resolve period: %w", err)
	}
	var covers bool
	if err := q.QueryRow(ctx,
		`SELECT $3::date BETWEEN start_date AND end_date FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		periodID, clubID, earlier.Format("2006-01-02"),
	).Scan(&covers); err != nil {
		return "", fmt.Errorf("reading period dates: %w", err)
	}
	if !covers {
		return "", transferRefusal(TransferCrossesPeriod)
	}
	switch status {
	case "open":
		return periodID, nil
	case "locked":
		return "", transferRefusal(TransferPeriodLocked)
	default:
		return "", transferRefusal(TransferPeriodClosed)
	}
}
