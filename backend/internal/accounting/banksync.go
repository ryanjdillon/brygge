package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/finance"
)

// isBryggeKID is `true` only for strings that match Brygge's own KID
// shape: exactly 12 ASCII digits with a valid check digit under EITHER
// scheme (mod-10 for historical KIDs, mod-11 for current ones). This is
// intentionally narrower than a bare check-digit assert because the bank's
// Melding column carries lots of free-text garbage that happens to fail
// length but might fluke a check digit — easier to reject the whole class.
func isBryggeKID(s string) bool {
	if len(s) != 12 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return finance.ValidateKIDAny(s)
}

// findInvoiceByCounterpartAndAmount is the tertiary auto-match path
// for the bank-sync loop: when neither KID nor invoice-number caught
// the row, look up the open unpaid invoice that belongs to the
// member whose normalized full name equals the bank counterpart and
// whose total matches the bank-row amount within 0.5 NOK. Returns
// "" when the lookup is ambiguous (2+ candidates) or finds no match
// — both cases leave the row for manual review.
//
// It runs only for clubs that have not opted into bank auto-assign
// (BRY-366); for those that have, AutoAssignBankDeposits replaces it.
func (s *Service) findInvoiceByCounterpartAndAmount(ctx context.Context, clubID, counterpart string, amount float64) string {
	needle := normalizeName(counterpart)
	if needle == "" {
		return ""
	}
	rows, err := s.db.Query(ctx,
		`SELECT i.id, u.full_name
		   FROM invoices i
		   JOIN users u ON u.id = i.user_id
		  WHERE i.club_id = $1
		    AND `+finance.OwedByMemberSQL("i")+`
		    AND ABS(i.total_amount - $2) < 0.005
		    AND NOT EXISTS (
		      SELECT 1 FROM bank_import_rows bir
		      WHERE bir.club_id = i.club_id AND bir.kid_number = i.kid_number
		        AND bir.journal_entry_id IS NOT NULL
		    )`,
		clubID, amount,
	)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var firstMatch string
	matches := 0
	for rows.Next() {
		var id, fullName string
		if err := rows.Scan(&id, &fullName); err != nil {
			continue
		}
		if normalizeName(fullName) != needle {
			continue
		}
		matches++
		if matches == 1 {
			firstMatch = id
		}
		if matches > 1 {
			return "" // ambiguous — bail
		}
	}
	if matches == 1 {
		return firstMatch
	}
	return ""
}

// BankSyncResult summarizes one full-scope bank sync run.
type BankSyncResult struct {
	KIDMatched int `json:"kid_matched"`
	// KIDSkippedSettled counts rows the KID cascade matched to a faktura
	// that turned out to be settled once it was locked (most likely a
	// double payment); they stay in Tildel.
	KIDSkippedSettled int      `json:"kid_skipped_settled"`
	VippsReconciled   int      `json:"vipps_reconciled"`
	VippsUnbalanced   int      `json:"vipps_unbalanced"`
	TransfersLinked   int      `json:"transfers_linked"`
	PaymentsLinked    int      `json:"payments_linked"`
	DraftsReleased    int      `json:"drafts_released"`
	ClosedPeriods     []string `json:"closed_periods"`
	// VippsPayouts reports the per-charge payout sweep (pass 5); its cleared
	// settlements are also counted in VippsReconciled. Nil on the legacy model.
	VippsPayouts *VippsPayoutSweepResult `json:"vipps_payouts,omitempty"`
	// ProvisionalSuperseded counts provisional rows dismissed because their
	// settled row is imported (pass 0b); ProvisionalAmbiguous lists those
	// that could not be paired one to one.
	ProvisionalSuperseded int                  `json:"provisional_superseded"`
	ProvisionalAmbiguous  []ProvisionalBankRow `json:"provisional_ambiguous"`
	// AutoAssign reports the KID-less deposits booked by the bank
	// auto-assign rule (pass 4c); nil when the club has not opted in.
	AutoAssign *AutoAssignResult `json:"auto_assign,omitempty"`
}

// BankSync re-runs auto-correlation across every unmatched bank import row
// for the club, in three passes (after releasing rows a crashed KID match
// left linked to an unposted draft; see healStrandedKIDDrafts, and
// dismissing provisional rows whose settled row has arrived; see
// SupersedeProvisionalBankRows):
//
//  1. KID matching against open invoices.
//  2. Vipps payout reconciliation for rows whose description matches the
//     Utb./Vippsnr pattern (only when a balanced bilag can be produced
//     against an open fiscal period).
//  3. Intra-bank transfer pairing across all of the club's imports.
//
// On the per-charge Vipps model pass 2 stands down and a final pass clears
// every payout deposit whose settlement has all its charges booked
// (ClearReadyVippsPayouts).
//
// This is the on-demand counterpart to the per-upload auto-match that
// already runs at import time. It lets the treasurer pull in matches that
// only became possible after subsequent uploads (e.g. bank first, Vipps
// later) or after fixing data (e.g. registering a member by name).
func (s *Service) BankSync(ctx context.Context, clubID, createdBy string) (*BankSyncResult, error) {
	res := &BankSyncResult{}
	closedYears := map[int]bool{}

	// Pass 0: release rows a crashed KID match left linked to its unposted
	// draft, so pass 1 can match them afresh.
	healed, herr := s.healStrandedKIDDrafts(ctx, clubID)
	if herr != nil {
		s.log.Warn().Err(herr).Msg("releasing stranded KID drafts failed")
	}
	res.DraftsReleased = healed

	// Pass 0b: dismiss provisional rows whose settled row has arrived, so
	// no later pass books an in-flight placeholder.
	sup := s.SupersedeProvisionalBankRowsReported(ctx, clubID, createdBy)
	res.ProvisionalSuperseded = sup.Superseded
	res.ProvisionalAmbiguous = sup.Ambiguous

	// With bank auto-assign on, the cascade's name+amount step stands down
	// and pass 4c books KID-less deposits instead.
	bankAuto, err := s.BankAutoAssignEnabled(ctx, clubID)
	if err != nil {
		return nil, err
	}

	// Pass 1: KID auto-match on all unmatched rows.
	kid, err := s.syncKIDMatches(ctx, clubID, createdBy, !bankAuto)
	if err != nil {
		return nil, fmt.Errorf("kid sync: %w", err)
	}
	res.KIDMatched = kid.matched
	res.KIDSkippedSettled = kid.skippedSettled
	for _, y := range kid.closedYears {
		closedYears[y] = true
	}

	// Pass 2: Vipps reconciliation on all unmatched bank rows that match
	// the Utb./Vippsnr pattern. Only confirm when balanced and the
	// target period is open.
	reconciled, unbalanced, vippsClosed, err := s.syncVippsReconciliations(ctx, clubID, createdBy)
	if err != nil {
		return nil, fmt.Errorf("vipps sync: %w", err)
	}
	res.VippsReconciled = reconciled
	res.VippsUnbalanced = unbalanced
	for _, y := range vippsClosed {
		closedYears[y] = true
	}

	// Pass 3: re-run intra-bank transfer detection across ALL of the
	// club's imports (the import-time detector only runs against the
	// current import).
	var importIDs []string
	importRows, err := s.db.Query(ctx,
		`SELECT id FROM bank_imports WHERE club_id = $1`, clubID,
	)
	if err == nil {
		for importRows.Next() {
			var id string
			if err := importRows.Scan(&id); err == nil {
				importIDs = append(importIDs, id)
			}
		}
		importRows.Close()
	}
	for _, importID := range importIDs {
		n, skipped, _ := s.detectIntraBankTransfers(ctx, clubID, importID, "", createdBy)
		res.TransfersLinked += n
		for _, y := range skipped {
			closedYears[y] = true
		}
	}

	// Pass 4: backfill payment back-links for invoices whose bank row
	// was KID-matched at the GL level but never got the
	// `linkInvoicePayment` step — typically because the row was
	// imported before DIL-363 shipped. Self-healing: "Run sync" now
	// also retroactively reconciles these legacy mismatches.
	backfilled, berr := s.backfillUnlinkedPayments(ctx, clubID)
	if berr != nil {
		s.log.Warn().Err(berr).Msg("backfill unlinked payments failed")
	}
	res.PaymentsLinked = backfilled

	// Pass 4b: same heal, but for rows matched by invoice NUMBER in the
	// description ("Faktura 44") rather than KID — the case where a member
	// paid by typing the invoice number because their KID was rejected. The
	// KID-keyed pass above can't reach these (no KID on the row, and reissuing
	// the KID moves the invoice's KID out from under any later match), so they
	// would otherwise stay "unpaid" forever and generate false purringer.
	numBackfilled, nerr := s.backfillInvoiceNumberPayments(ctx, clubID)
	if nerr != nil {
		s.log.Warn().Err(nerr).Msg("backfill invoice-number payments failed")
	}
	res.PaymentsLinked += numBackfilled

	// Pass 4c: book the unambiguous KID-less deposits (BRY-366), after the
	// KID passes have taken every row that carries a reference.
	res.AutoAssign = s.AutoAssignBankDepositsReported(ctx, clubID, createdBy, BankAutoAssignOpts{Trigger: BankAutoAssignTriggerSync})

	// Pass 5: per-charge model only — clear every Vipps payout deposit whose
	// settlement has all its charges booked. Legacy clubs get an empty sweep.
	if perCharge, perr := s.VippsPerChargeEnabled(ctx, clubID); perr != nil {
		return nil, fmt.Errorf("checking vipps model: %w", perr)
	} else if perCharge {
		sweep := s.ClearReadyVippsPayoutsReported(ctx, clubID, createdBy)
		res.VippsPayouts = sweep
		res.VippsReconciled += len(sweep.Cleared)
	}

	for y := range closedYears {
		res.ClosedPeriods = append(res.ClosedPeriods, fmt.Sprintf("%d", y))
	}
	return res, nil
}

// strandedDraftGrace is how old a linked KID draft must be before bank sync
// treats it as stranded rather than as an import or sync still in flight.
const strandedDraftGrace = 15 * time.Minute

// healStrandedKIDDrafts finds bank rows auto-matched to a payment-match
// draft (the KID / invoice-number / counterpart cascade of bank import and
// bank sync) that never got posted. Those paths link the row before posting
// so a posted entry never exists without its row; a crash between the two
// leaves the row looking booked while the draft counts nowhere. Such rows
// are unlinked (with the faktura the link recorded) and the draft deleted,
// in one transaction, and pass 1 then
// matches them again against the invoices as they are now.
//
// Only entries the payment-match paths created qualify: tied to the row by
// source (per row, or per import for drafts written before both paths used
// the row), crediting 1500, and carrying exactly kidMatchDescription for
// that row. Mapping-rule drafts from AutoMatchImport, which may also credit
// 1500 and are meant to wait for a gated post, never have that description;
// transfer drafts and manual matches are left alone too.
func (s *Service) healStrandedKIDDrafts(ctx context.Context, clubID string) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`SELECT je.id FROM journal_entries je
		  WHERE je.club_id = $1 AND je.status = 'draft' AND je.source = 'bank_import'
		    AND je.created_at < now() - $3::interval
		    AND EXISTS (SELECT 1 FROM bank_import_rows b
		                 WHERE b.club_id = je.club_id AND b.journal_entry_id = je.id AND b.auto_matched
		                   AND ((je.source_table = 'bank_import_rows' AND je.source_id = b.id::text)
		                     OR (je.source_table = 'bank_import' AND je.source_id = b.bank_import_id::text))
		                   AND je.description = `+kidMatchDescriptionSQL+`)
		    AND EXISTS (SELECT 1 FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		                 WHERE jl.journal_entry_id = je.id AND a.code = $2 AND jl.credit > 0)
		  FOR UPDATE OF je SKIP LOCKED`,
		clubID, receivablesAccountCode, fmt.Sprintf("%d seconds", int(strandedDraftGrace.Seconds())),
	)
	if err != nil {
		return 0, fmt.Errorf("finding stranded drafts: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("scanning stranded drafts: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = NULL, invoice_id = NULL, auto_matched = false
		  WHERE club_id = $1 AND journal_entry_id = ANY($2::uuid[])`,
		clubID, ids,
	); err != nil {
		return 0, fmt.Errorf("unlinking stranded rows: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM journal_entries WHERE club_id = $1 AND status = 'draft' AND id = ANY($2::uuid[])`,
		clubID, ids,
	); err != nil {
		return 0, fmt.Errorf("deleting stranded drafts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing: %w", err)
	}
	s.log.Warn().Int("count", len(ids)).Str("club_id", clubID).Msg("bank sync released rows linked to unposted KID drafts")
	return len(ids), nil
}

// backfillUnlinkedPayments fixes up invoices whose KID-matching bank
// row already has a journal entry (so the GL is correct) but whose
// `invoices.payment_id` was never set — leaving the dashboard,
// FakturaList "paid" filter, and priceItemSummary all reading them as
// unpaid. The common cause is bank rows imported before DIL-363
// wired `linkInvoicePayment` into the per-row match.
//
// Only full-amount matches are backfilled (bir.amount = i.total_amount).
// Partial payments would require a "partial" status on the payments
// table that doesn't exist yet, so those rows are left alone.
//
// An empty KID is "no KID", not a key: imported fakturas and anonymous
// bank rows all carry the empty string, so matching on it marked imported fakturas paid
// by any same-amount row, several by one (BRY-415). A non-empty KID is
// unique per club (idx_invoices_kid), so each row then names one faktura.
// The KID on a bank row is whatever the payer typed, though: a row already
// assigned to another faktura (a member paying with last year's KID) is
// that faktura's payment, not this one's.
//
// The row's own booking must be a posted payment of a receivable (CR 1500),
// as in backfillInvoiceNumberPayments: a KID row a mapping rule booked to
// another account, or whose entry is still a draft or was voided, paid
// nothing off the faktura (BRY-417).
func (s *Service) backfillUnlinkedPayments(ctx context.Context, clubID string) (int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT ON (i.id) i.id, bir.row_date
		   FROM invoices i
		   JOIN bank_import_rows bir ON bir.kid_number = i.kid_number
		   JOIN bank_imports bi      ON bi.id = bir.bank_import_id
		   JOIN journal_entries je   ON je.id = bir.journal_entry_id
		                            AND je.club_id = $1 AND je.status = 'posted'
		  WHERE bi.club_id = $1
		    AND i.club_id  = $1
		    AND i.kid_number <> ''
		    AND (bir.invoice_id IS NULL OR bir.invoice_id = i.id)
		    AND i.payment_id IS NULL
		    AND i.status = 'open'
		    AND bir.amount > 0
		    AND bir.amount = i.total_amount
		    AND EXISTS (
		      SELECT 1 FROM journal_lines jl
		        JOIN accounts a ON a.id = jl.account_id
		       WHERE jl.journal_entry_id = je.id AND a.code = $2 AND jl.credit > 0)
		  ORDER BY i.id, bir.row_date ASC`,
		clubID, receivablesAccountCode,
	)
	if err != nil {
		return 0, err
	}
	type pending struct {
		invoiceID string
		paidAt    time.Time
	}
	var pendings []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.invoiceID, &p.paidAt); err != nil {
			rows.Close()
			return 0, err
		}
		pendings = append(pendings, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, p := range pendings {
		if err := s.linkInvoicePayment(ctx, clubID, p.invoiceID, p.paidAt); err != nil {
			s.log.Warn().Err(err).
				Str("invoice_id", p.invoiceID).
				Msg("backfill linkInvoicePayment failed")
			continue
		}
		n++
	}
	return n, nil
}

// backfillInvoiceNumberPayments links payments for invoices that are still
// open+unpaid but whose matched bank row references them by invoice number in
// the description (or via an explicit bank_import_rows.invoice_id) and whose
// journal entry credits receivables — i.e. the payment is booked at the GL but
// invoices.payment_id was never set. Only exact-amount matches on a genuine
// receivables (1500) credit are healed, so the (invoice_number, amount) key is
// high-confidence. Only a posted entry counts: a draft or voided booking paid
// nothing (BRY-417). Idempotent: linkInvoicePayment no-ops if already linked.
//
// This is the invoice-number counterpart to backfillUnlinkedPayments (which
// keys on KID). It exists because members whose KID was rejected paid by
// typing "Faktura NN", and a later KID reissue removed any KID linkage. See
// BRY-233.
func (s *Service) backfillInvoiceNumberPayments(ctx context.Context, clubID string) (int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT ON (i.id) i.id, bir.row_date
		   FROM invoices i
		   JOIN bank_import_rows bir
		     ON bir.club_id = i.club_id
		    AND bir.journal_entry_id IS NOT NULL
		    AND bir.amount > 0
		    AND bir.amount = i.total_amount
		    AND (bir.invoice_id = i.id
		         OR bir.description ~* ('faktura\D*' || i.invoice_number::text || '\y'))
		   JOIN journal_entries je ON je.id = bir.journal_entry_id
		                          AND je.club_id = i.club_id AND je.status = 'posted'
		   JOIN journal_lines jl ON jl.journal_entry_id = je.id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE i.club_id = $1
		    AND i.payment_id IS NULL
		    AND i.status = 'open'
		    AND a.code = $2
		    AND jl.credit > 0
		  ORDER BY i.id, bir.row_date ASC`,
		clubID, receivablesAccountCode,
	)
	if err != nil {
		return 0, err
	}
	type pending struct {
		invoiceID string
		paidAt    time.Time
	}
	var pendings []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.invoiceID, &p.paidAt); err != nil {
			rows.Close()
			return 0, err
		}
		pendings = append(pendings, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, p := range pendings {
		if err := s.linkInvoicePayment(ctx, clubID, p.invoiceID, p.paidAt); err != nil {
			s.log.Warn().Err(err).
				Str("invoice_id", p.invoiceID).
				Msg("backfill invoice-number linkInvoicePayment failed")
			continue
		}
		n++
	}
	return n, nil
}

// kidSyncResult is what syncKIDMatches booked and left.
type kidSyncResult struct {
	matched int
	// skippedSettled counts rows whose faktura was already settled once
	// the faktura lock was held; they stay in Tildel.
	skippedSettled int
	closedYears    []int
}

// syncKIDMatches books unmatched incoming rows by KID, then by invoice
// number; nameMatch adds the older counterpart-name + amount step, which
// runs only while the club has bank auto-assign off. Only rows passing
// kidCascadeEligibleSQL are considered.
func (s *Service) syncKIDMatches(ctx context.Context, clubID, createdBy string, nameMatch bool) (kidSyncResult, error) {
	var res kidSyncResult
	// Drop the kid_number <> '' filter so we can ALSO process rows
	// that lost their KID at import time because the bank's CSV
	// left the KID column blank and embedded the reference in the
	// description string instead (DNB online-bank rows of the
	// "Fra: <name> Betalt: <date> · <kid>" shape). Per-row we then
	// extract the KID + invoice-number from the description and
	// persist them so future passes — and the "0 received" tile
	// in oversikt — pick the row up.
	rows, err := s.db.Query(ctx,
		`SELECT bir.id, bir.row_date, bir.amount,
		        COALESCE(bir.kid_number, ''),
		        COALESCE(bir.reference, ''),
		        COALESCE(bir.counterpart, ''),
		        bir.description, bi.bank_account_code
		 FROM bank_import_rows bir
		 JOIN bank_imports bi ON bi.id = bir.bank_import_id
		 WHERE bir.club_id = $1
		   AND bir.amount > 0
		   AND `+kidCascadeEligibleSQL("bir"),
		clubID,
	)
	if err != nil {
		return res, fmt.Errorf("listing unmatched rows: %w", err)
	}
	defer rows.Close()

	type pending struct {
		rowID, kid, reference, counterpart, desc, bankAccount string
		date                                                  time.Time
		amount                                                float64
	}
	var work []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.rowID, &p.date, &p.amount, &p.kid, &p.reference, &p.counterpart, &p.desc, &p.bankAccount); err != nil {
			continue
		}
		work = append(work, p)
	}
	rows.Close()

	closedYears := map[int]bool{}
	for _, p := range work {
		// Rejected-on-receiving-account rows ("KID … ble ikke
		// akseptert på denne kontoen") are notification rows the bank
		// posts when a transfer to a savings/høyrente account bounces
		// at the inbound-cap. The substance is "money never arrived;
		// payer still owes." Skip entirely — neither auto-match nor
		// journal.
		if IsRejectedKIDDescription(p.desc) {
			continue
		}

		// Recover KID + payer from description if missing. The
		// Melding column from the bank carries free text for many
		// rows ("Medlemskontingent Olav Alpen", "Faktura 44", ".")
		// which is NOT a KID. Drop anything that isn't our exact
		// shape (12 digits + Luhn) and let the description-extractor
		// have a try.
		newKID := strings.TrimSpace(p.kid)
		if newKID != "" && !isBryggeKID(newKID) {
			newKID = ""
		}
		if newKID == "" {
			newKID = ExtractKIDFromDescription(p.desc)
		}
		newCounterpart := p.counterpart
		if payer := ExtractPayerFromDescription(p.desc); payer != "" {
			newCounterpart = payer
		}
		if newKID != p.kid || newCounterpart != p.counterpart {
			_, _ = s.db.Exec(ctx,
				`UPDATE bank_import_rows SET kid_number = $1, counterpart = $2 WHERE id = $3`,
				newKID, newCounterpart, p.rowID,
			)
			p.kid = newKID
			p.counterpart = newCounterpart
		}

		invoiceID := s.findCascadeInvoice(ctx, clubID, BankRow{
			KID: p.kid, Reference: p.reference, Description: p.desc,
			Counterpart: p.counterpart, Amount: p.amount,
		}, nameMatch)
		if invoiceID == "" {
			continue
		}

		err := s.bookKIDCascadeMatch(ctx, clubID, kidCascadeMatch{
			rowID: p.rowID, invoiceID: invoiceID,
			actorID: createdBy, bankAccount: p.bankAccount,
			kid: p.kid, desc: p.desc, date: p.date, amount: p.amount,
		})
		switch {
		case err == nil:
			res.matched++
		case errors.Is(err, errInvoiceSettled):
			res.skippedSettled++
		case errors.Is(err, errKIDPeriodClosed):
			closedYears[p.date.Year()] = true
		case errors.Is(err, errKIDRowTaken), errors.Is(err, errInvoiceNotOpen):
			// A concurrent booking or a dismissal won the row, or the
			// faktura was voided or credited meanwhile; the row stays in
			// Tildel for the treasurer.
		default:
			// Most often a bank_account_code missing from the chart of
			// accounts (a bank role whose gl_code was never seeded); logged
			// loudly so the chart gets fixed instead of the row sitting
			// unmatched in silence.
			s.log.Warn().Err(err).
				Str("bank_row_id", p.rowID).
				Str("bank_account_code", p.bankAccount).
				Str("kid", p.kid).
				Msg("KID match failed; row stays unmatched")
		}
	}

	for y := range closedYears {
		res.closedYears = append(res.closedYears, y)
	}
	return res, nil
}

func (s *Service) syncVippsReconciliations(ctx context.Context, clubID, createdBy string) (matched, unbalanced int, closed []int, err error) {
	// Clubs on the per-charge model own Vipps payout reconciliation through the
	// Tildel per-charge flow + payout auto-clear (BRY-226/227). The legacy
	// single-bilag auto-match must stand down so it doesn't pre-empt them.
	perCharge, ferr := s.VippsPerChargeEnabled(ctx, clubID)
	if ferr != nil {
		return 0, 0, nil, fmt.Errorf("checking vipps model: %w", ferr)
	}
	if perCharge {
		return 0, 0, nil, nil
	}

	rows, qerr := s.db.Query(ctx,
		`SELECT id FROM bank_import_rows
		 WHERE club_id = $1
		   AND journal_entry_id IS NULL
		   AND description ~* '^utb\.\s*[0-9]+\s+vippsnr\s+[0-9]+'`,
		clubID,
	)
	if qerr != nil {
		return 0, 0, nil, fmt.Errorf("listing candidate rows: %w", qerr)
	}
	defer rows.Close()

	var rowIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			rowIDs = append(rowIDs, id)
		}
	}
	rows.Close()

	closedYears := map[int]bool{}
	for _, rowID := range rowIDs {
		preview, perr := s.ReconcileVippsPreview(ctx, clubID, rowID)
		if perr != nil {
			if perr == pgx.ErrNoRows {
				continue
			}
			continue
		}
		if !preview.Balanced {
			unbalanced++
			continue
		}
		if preview.PeriodClosed {
			closedYears[preview.PeriodYear] = true
			continue
		}
		if _, cerr := s.ReconcileVippsConfirm(ctx, clubID, rowID, "", createdBy, preview.Lines); cerr != nil {
			unbalanced++
			continue
		}
		matched++
	}

	for y := range closedYears {
		closed = append(closed, y)
	}
	return matched, unbalanced, closed, nil
}
