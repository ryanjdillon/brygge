package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
)

// The automatic booking run (ACC-13, BRY-554 sub 3c, BRY-567). One deep entry
// point, RunAutomaticBooking, that cmd/api schedules at every start of the API
// and every 24 h after. It ingests the Vipps Report API for the last days
// (whatever the club's switch), then, when the club has turned on
// clubs.automatic_booking, books what a person's Synkroniser and Synk Vipps
// would book without guessing, as the club's system actor:
//
//  1. the exact bank passes: the KID cascade without the counterpart-name +
//     amount step, intra-bank transfer pairing (as draft bilags), the payment
//     back-link heals, and bank auto-assign when the club has opted in;
//  2. the Vipps booking tail (order-linked charges, the auto-assign rules,
//     the payout sweep) in one hold of the club's Vipps booking slot, on the
//     per-charge model only.
//
// It never takes an actor from a request and never calls a person-only undo.

// What started an automatic run. The run passes it on as the trigger of the
// auto-assign audit rows it writes. BRY-570 adds a Stripe event trigger.
const (
	AutoBookingTriggerBoot    = "boot"
	AutoBookingTriggerNightly = "nightly"
)

const (
	// autoBookingDeadline bounds one run, ingest and booking together.
	autoBookingDeadline = 10 * time.Minute
	// defaultAutoBookingSlotWait is how long the run waits for the club's
	// Vipps booking slot; still busy, it skips the Vipps step for this tick.
	defaultAutoBookingSlotWait = 2 * time.Minute
	// vippsReportDays is how many days back (d-1 .. d-n) each run pulls from
	// the Report API. The pull is idempotent, so the overlap covers reports
	// that finalise late or over a weekend.
	vippsReportDays = 4
	// autoBookingAuditListCap caps each list in the summary audit row; the
	// rest is counted in <list>_truncated.
	autoBookingAuditListCap = 20
)

// AutoBookingProviderVipps is the provider of the Vipps and bank run; the
// Stripe run (BRY-570) reports as "stripe".
const AutoBookingProviderVipps = "vipps"

// AutoBookingObserver is told what the automatic runs did, for the operator's
// metrics (BRY-571). Calls are synchronous and must not block.
type AutoBookingObserver interface {
	// VippsReportSynced is called when every date of a run's Report API
	// pull for the merchant answered (a report not finalised yet counts).
	VippsReportSynced(msn string, at time.Time)
	// AutoBookingRun is called once at the end of every run, failed or not,
	// also when the club's switch is off (failed false then).
	AutoBookingRun(provider string, failed bool)
}

// SetAutoBookingObserver wires the observer of the automatic runs; nil tells
// no one. Set it before the run's goroutine starts.
func (s *Service) SetAutoBookingObserver(o AutoBookingObserver) {
	s.autoBookingObs = o
}

// SetAutoBookingSlotWait sets how long the automatic run waits for the club's
// Vipps booking slot; d <= 0 restores the default. Tests shorten it.
func (s *Service) SetAutoBookingSlotWait(d time.Duration) {
	s.autoBookingSlotWait = d
}

func (s *Service) autoBookingWait() time.Duration {
	if s.autoBookingSlotWait > 0 {
		return s.autoBookingSlotWait
	}
	return defaultAutoBookingSlotWait
}

func (s *Service) autoBookingClock() time.Time {
	if s.autoBookingNow != nil {
		return s.autoBookingNow()
	}
	return time.Now()
}

// AutoBookingOpts says how RunAutomaticBooking runs.
type AutoBookingOpts struct {
	// Source is the Vipps Report API; nil skips the ingest (no Vipps
	// credentials). Build it with NewVippsReportSource.
	Source VippsReportSource
	// Trigger is AutoBookingTriggerBoot or AutoBookingTriggerNightly.
	Trigger string
	// Accounting is cfg.Features.Accounting. When false the run only
	// ingests.
	Accounting bool
}

// VippsIngestSummary is what a run's Report API pull did.
type VippsIngestSummary struct {
	MSN string
	// Imported counts the rows newly inserted over every date.
	Imported int
	// TryLaterDates were not finalised yet; that is an answer, not a failure.
	TryLaterDates []string
	// FailedDates returned an error.
	FailedDates []string
	// LedgerFailed is set when the merchant's ledger could not be resolved;
	// no date was pulled then.
	LedgerFailed bool
	// OK is true when the ledger resolved and every date answered.
	OK bool
}

// AutoBookingResult is what one automatic run did.
type AutoBookingResult struct {
	Trigger string
	// Ingest is nil when the run had no Report API source.
	Ingest *VippsIngestSummary
	// Booked is true when the club's switch was on and the booking ran.
	Booked bool
	// Busy: the Vipps booking slot stayed held past the run's wait, so the
	// Vipps step was skipped this tick (the bank passes still ran).
	Busy bool
	// Aborted: the run's context ended before the booking finished.
	Aborted bool
	// IngestFailed: the Report API pull did not answer for every date.
	IngestFailed bool
	// Vipps is the Vipps booking tail's result; nil when it did not run.
	Vipps *VippsBookingResult
	// VippsSkippedLegacy: the club is not on the per-charge model, so its
	// Vipps rows are never booked unattended.
	VippsSkippedLegacy bool
	KIDMatched         int
	KIDSkippedSettled  int
	// TransfersLinked counts the transfer pairs the run prepared as draft
	// bilags (a person posts them).
	TransfersLinked int
	// PaymentsLinked counts the payment back-links the heal passes set.
	PaymentsLinked int
	// BankAutoAssign is nil when the club has bank auto-assign off.
	BankAutoAssign *AutoAssignResult
	// MissingPeriods and ClosedPeriods list the years payments wait for.
	MissingPeriods []string
	ClosedPeriods  []string
	// FiscalYearNotices counts the treasurer notices the run queued.
	FiscalYearNotices int
	// Errors are the steps that failed.
	Errors   []string
	Duration time.Duration
}

// Changed reports whether the run booked or prepared anything: an order or
// auto-assigned charge, a cleared payout, a KID match, a transfer draft or a
// bank auto-assign. Repairs and payment back-link heals do not count.
func (r *AutoBookingResult) Changed() bool {
	if r.KIDMatched > 0 || r.TransfersLinked > 0 {
		return true
	}
	if r.BankAutoAssign != nil && len(r.BankAutoAssign.Assigned) > 0 {
		return true
	}
	if v := r.Vipps; v != nil {
		if v.Orders != nil && v.Orders.Booked > 0 {
			return true
		}
		if v.AutoAssign != nil && len(v.AutoAssign.Assigned) > 0 {
			return true
		}
		if v.Payouts != nil && len(v.Payouts.Cleared) > 0 {
			return true
		}
	}
	return false
}

// Failed reports whether a step of the run failed or the run was aborted.
// Busy and IngestFailed are not failures here (the operator's
// last-run-failed metric stays narrow); they still make NeedsAudit true.
func (r *AutoBookingResult) Failed() bool {
	if len(r.Errors) > 0 || r.Aborted {
		return true
	}
	if r.BankAutoAssign != nil && r.BankAutoAssign.Error != "" {
		return true
	}
	return r.Vipps != nil && r.Vipps.Payouts != nil && r.Vipps.Payouts.Error != ""
}

// NeedsAudit reports whether the run writes its summary audit row: it
// changed something, healed a payment back-link, failed, was busy, aborted
// or could not ingest, or a
// payout failed to clear (posting refused, or a settlement that does not net
// to 0, which the sweep reports as skipped).
func (r *AutoBookingResult) NeedsAudit() bool {
	if r.Changed() || r.Failed() || r.Busy || r.IngestFailed || r.PaymentsLinked > 0 {
		return true
	}
	return len(payoutsFailed(r.Vipps)) > 0
}

// payoutsFailed is the payouts the run could not clear for a reason a person
// must fix: the sweep's failed settlements and the ones that do not net to 0
// (BRY-560, which the sweep lists as skipped).
func payoutsFailed(v *VippsBookingResult) []VippsPayoutSweepItem {
	if v == nil || v.Payouts == nil {
		return nil
	}
	out := make([]VippsPayoutSweepItem, 0, len(v.Payouts.Failed)+len(v.Payouts.notNetting))
	out = append(out, v.Payouts.Failed...)
	return append(out, v.Payouts.notNetting...)
}

// RunAutomaticBooking is the automatic booking run for one club. It ingests
// the Vipps Report API for d-1..d-4 when opts.Source is set, whatever the
// club's switch, and then, when accounting is on and the club has turned on
// clubs.automatic_booking, books as the club's system actor: the exact bank
// passes (KID cascade without the name step, transfer pairing as drafts, the
// back-link heals, bank auto-assign when opted in), then the Vipps booking
// tail on the per-charge model. A failed ingest does not stop the booking.
// The run is bounded by autoBookingDeadline; a run whose context ends stops,
// reports Aborted and still writes its summary row. The summary audit row
// (accounting.auto_booking_run) is written when NeedsAudit; the observer is
// told at the end of every run.
func (s *Service) RunAutomaticBooking(ctx context.Context, clubID string, opts AutoBookingOpts) *AutoBookingResult { //nolint:cyclop // one ordered run over the passes; each step is a few lines
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, autoBookingDeadline)
	defer cancel()
	res := &AutoBookingResult{Trigger: opts.Trigger}

	if opts.Source != nil {
		res.Ingest = s.ingestVippsReportDays(ctx, opts.Source, clubID, s.autoBookingClock())
		res.IngestFailed = !res.Ingest.OK
		if res.Ingest.OK && s.autoBookingObs != nil {
			s.autoBookingObs.VippsReportSynced(res.Ingest.MSN, time.Now())
		}
	}
	// A run whose context ended during the ingest books nothing. It still
	// reads the switches and records itself when the club has booking on.
	res.Aborted = ctx.Err() != nil

	if !opts.Accounting {
		s.log.Info().Str("club_id", clubID).Str("trigger", opts.Trigger).Bool("ingest_failed", res.IngestFailed).
			Msg("automatic booking: accounting is off; ingest only")
		s.finishAutoBooking(clubID, res, start)
		return res
	}

	var on, perCharge, bankAuto bool
	// Read on postCommitContext, so a run cancelled during its ingest still
	// knows whether to record its abort.
	swCtx, swCancel := postCommitContext(ctx)
	err := s.db.QueryRow(swCtx,
		`SELECT automatic_booking, COALESCE(vipps_per_charge_enabled, false), bank_auto_assign FROM clubs WHERE id = $1`,
		clubID,
	).Scan(&on, &perCharge, &bankAuto)
	swCancel()
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = fmt.Errorf("club not found")
		}
		// Whether the club wants booking is unknown, so nothing is booked
		// and no audit row written; the failure reaches the operator.
		s.log.Error().Err(err).Str("club_id", clubID).Msg("automatic booking: reading the club's switches failed")
		res.Errors = append(res.Errors, "reading the club's switches: "+err.Error())
		s.finishAutoBooking(clubID, res, start)
		return res
	}
	if !on {
		s.log.Info().Str("club_id", clubID).Str("trigger", opts.Trigger).Bool("ingest_failed", res.IngestFailed).
			Msg("automatic booking is off for the club; ingest only")
		s.finishAutoBooking(clubID, res, start)
		return res
	}

	if res.Aborted {
		// Cancelled during the ingest: nothing is booked, the abort is
		// recorded (as the system actor when it can still be resolved).
		actx, acancel := postCommitContext(ctx)
		actor, aerr := s.SystemActor(actx, clubID)
		acancel()
		if aerr != nil {
			actor = ""
		}
		s.writeAutoBookingAudit(ctx, clubID, actor, res, start)
		s.finishAutoBooking(clubID, res, start)
		return res
	}

	actor, err := s.SystemActor(ctx, clubID)
	if err != nil {
		res.Errors = append(res.Errors, "resolving the system actor: "+err.Error())
		res.Aborted = ctx.Err() != nil
		s.writeAutoBookingAudit(ctx, clubID, "", res, start)
		s.finishAutoBooking(clubID, res, start)
		return res
	}
	res.Booked = true

	closed, missing := map[int]bool{}, map[int]bool{}
	s.runAutomaticBankPasses(ctx, clubID, actor, bankAuto, opts.Trigger, res, closed, missing)

	switch {
	case res.Aborted:
	case !perCharge:
		res.VippsSkippedLegacy = true
	default:
		booked, err := s.BookVippsQueue(ctx, clubID, actor, VippsBookingOpts{Wait: s.autoBookingWait(), Trigger: opts.Trigger})
		res.Vipps = booked
		switch {
		case errors.Is(err, ErrVippsBookingBusy):
			res.Busy = true
		case ctx.Err() != nil:
			res.Aborted = true
		case err != nil:
			res.Errors = append(res.Errors, "vipps booking: "+err.Error())
		}
		if booked != nil {
			addYears(missing, booked.AutoAssign.noPeriodYears())
			addYears(missing, booked.Payouts.noPeriodYears())
		}
	}
	res.ClosedPeriods = yearLabels(closed)
	res.MissingPeriods = yearLabels(missing)

	if !res.Aborted {
		n, err := s.NotifyWaitingFiscalYears(ctx, clubID, time.Now())
		res.FiscalYearNotices = n
		switch {
		case err != nil && ctx.Err() != nil:
			res.Aborted = true
		case err != nil:
			res.Errors = append(res.Errors, "fiscal year notice: "+err.Error())
		}
		if n > 0 && s.kickDeliveries != nil {
			s.kickDeliveries()
		}
	}

	if res.NeedsAudit() {
		s.writeAutoBookingAudit(ctx, clubID, actor, res, start)
	}
	s.finishAutoBooking(clubID, res, start)
	return res
}

// runAutomaticBankPasses runs the run's bank passes in BankSync's order,
// outside the Vipps booking slot (as an upload and Synkroniser run them): the
// KID cascade without the name step, transfer pairing, the payment back-link
// heals and, when the club opted in, bank auto-assign. Between passes it
// checks the context and marks the run Aborted when it has ended.
func (s *Service) runAutomaticBankPasses(ctx context.Context, clubID, actor string, bankAuto bool, trigger string, res *AutoBookingResult, closed, missing map[int]bool) {
	stop := func() bool {
		if ctx.Err() != nil {
			res.Aborted = true
			return true
		}
		return false
	}

	// The exact references only: KID, arkivref, faktura number + amount.
	// The counterpart-name + amount step is the guess bank auto-assign
	// exists to replace, so the run never uses it.
	kid, err := s.syncKIDMatches(ctx, clubID, actor, false)
	res.KIDMatched, res.KIDSkippedSettled = kid.matched, kid.skippedSettled
	addYears(closed, kid.closedYears)
	addYears(missing, kid.missingYears)
	if err != nil && ctx.Err() == nil {
		res.Errors = append(res.Errors, "kid matching: "+err.Error())
	}
	if stop() {
		return
	}

	linked, closedT, missingT := s.pairIntraBankTransfersAll(ctx, clubID, actor)
	res.TransfersLinked = linked
	addYears(closed, closedT)
	addYears(missing, missingT)
	if stop() {
		return
	}

	// The payment back-link heals (BankSync passes 4 and 4b): no postings,
	// idempotent; they set invoices.payment_id where a booking's back-link
	// was lost.
	n, err := s.backfillUnlinkedPayments(ctx, clubID)
	res.PaymentsLinked += n
	if err != nil && ctx.Err() == nil {
		s.log.Warn().Err(err).Msg("automatic booking: backfill unlinked payments failed")
	}
	if stop() {
		return
	}
	n, err = s.backfillInvoiceNumberPayments(ctx, clubID)
	res.PaymentsLinked += n
	if err != nil && ctx.Err() == nil {
		s.log.Warn().Err(err).Msg("automatic booking: backfill invoice-number payments failed")
	}
	if stop() {
		return
	}

	if bankAuto {
		res.BankAutoAssign = s.AutoAssignBankDepositsReported(ctx, clubID, actor, BankAutoAssignOpts{Trigger: trigger})
		addYears(missing, res.BankAutoAssign.noPeriodYears())
		stop()
	}
}

// ingestVippsReportDays pulls d-1..d-vippsReportDays from the Report API into
// the Vipps queue as an automated import (no actor: ingest and repair only,
// never a booking).
func (s *Service) ingestVippsReportDays(ctx context.Context, src VippsReportSource, clubID string, now time.Time) *VippsIngestSummary {
	sum := &VippsIngestSummary{MSN: src.MSN(), TryLaterDates: []string{}, FailedDates: []string{}}
	ledgers, err := src.LedgersParsed(ctx)
	if err == nil && len(ledgers) == 0 {
		err = errors.New("no vipps ledgers for merchant")
	}
	if err != nil {
		s.log.Warn().Err(err).Str("msn", sum.MSN).Msg("vipps report sync: resolve ledger failed")
		sum.LedgerFailed = true
		return sum
	}
	ledgerID := ledgers[0].LedgerID
	for d := 1; d <= vippsReportDays; d++ {
		date := now.AddDate(0, 0, -d).Format("2006-01-02")
		r, err := s.SyncVippsReport(ctx, src, clubID, "", ledgerID, date)
		switch {
		case err != nil:
			s.log.Warn().Err(err).Str("date", date).Msg("vipps report sync failed")
			sum.FailedDates = append(sum.FailedDates, date)
		case r.TryLater:
			s.log.Debug().Str("date", date).Msg("vipps report not finalised yet")
			sum.TryLaterDates = append(sum.TryLaterDates, date)
		default:
			sum.Imported += r.Imported
			if r.Imported > 0 {
				s.log.Info().Str("date", date).Int("imported", r.Imported).
					Int("skipped", r.SkippedDup).Msg("vipps report synced")
			}
		}
	}
	sum.OK = len(sum.FailedDates) == 0
	return sum
}

// finishAutoBooking tells the observer and logs the run's one Info line.
func (s *Service) finishAutoBooking(clubID string, res *AutoBookingResult, start time.Time) {
	res.Duration = time.Since(start)
	if s.autoBookingObs != nil {
		s.autoBookingObs.AutoBookingRun(AutoBookingProviderVipps, res.Failed())
	}
	ev := s.log.Info()
	if res.Failed() {
		ev = s.log.Warn()
	}
	ev = ev.Str("club_id", clubID).Str("trigger", res.Trigger).Bool("booked", res.Booked).
		Bool("busy", res.Busy).Bool("aborted", res.Aborted).Bool("ingest_failed", res.IngestFailed).
		Int("kid_matched", res.KIDMatched).Int("transfers_linked", res.TransfersLinked).
		Int("payments_linked", res.PaymentsLinked).Int("fiscal_year_notices", res.FiscalYearNotices).
		Strs("errors", res.Errors).Dur("duration", res.Duration)
	if res.Ingest != nil {
		ev = ev.Int("imported", res.Ingest.Imported).Strs("failed_dates", res.Ingest.FailedDates)
	}
	if res.BankAutoAssign != nil {
		ev = ev.Int("bank_auto_assigned", len(res.BankAutoAssign.Assigned))
	}
	if v := res.Vipps; v != nil {
		if v.Orders != nil {
			ev = ev.Int("orders_booked", v.Orders.Booked)
		}
		if v.AutoAssign != nil {
			ev = ev.Int("auto_assigned", len(v.AutoAssign.Assigned))
		}
		if v.Payouts != nil {
			ev = ev.Int("payouts_cleared", len(v.Payouts.Cleared)).Int("payouts_failed", len(v.Payouts.Failed))
		}
	}
	ev.Msg("automatic booking run")
}

// writeAutoBookingAudit writes the run's summary row. It runs on
// postCommitContext, so a run whose context ended still records itself.
func (s *Service) writeAutoBookingAudit(ctx context.Context, clubID, actor string, res *AutoBookingResult, start time.Time) {
	if s.audit == nil {
		return
	}
	res.Duration = time.Since(start)
	actx, cancel := postCommitContext(ctx)
	defer cancel()
	s.audit.LogAction(actx, clubID, actor, "", audit.ActionAccountingAutoBookingRun, "accounting", clubID, autoBookingAuditDetails(res))
}

// autoBookingAuditDetails is the summary row's details: snake_case, ids and
// counts only, each list capped at autoBookingAuditListCap with the rest
// counted in <list>_truncated.
func autoBookingAuditDetails(res *AutoBookingResult) map[string]any {
	d := map[string]any{
		"provider":            AutoBookingProviderVipps,
		"trigger":             res.Trigger,
		"busy":                res.Busy,
		"aborted":             res.Aborted,
		"ingest_failed":       res.IngestFailed,
		"duration_ms":         res.Duration.Milliseconds(),
		"fiscal_year_notices": res.FiscalYearNotices,
	}
	capList(d, "missing_periods", res.MissingPeriods)
	capList(d, "closed_periods", res.ClosedPeriods)
	capList(d, "errors", res.Errors)
	if in := res.Ingest; in != nil {
		ing := map[string]any{"msn": in.MSN, "imported": in.Imported}
		capList(ing, "failed_dates", in.FailedDates)
		if in.LedgerFailed {
			ing["ledger_failed"] = true
		}
		d["ingest"] = ing
	}
	if res.VippsSkippedLegacy {
		d["vipps_skipped_legacy"] = true
	}
	if v := res.Vipps; v != nil {
		d["vipps"] = vippsAuditDetails(v)
	}
	bank := map[string]any{
		"kid_matched":         res.KIDMatched,
		"kid_skipped_settled": res.KIDSkippedSettled,
		"transfers_linked":    res.TransfersLinked,
		"payments_linked":     res.PaymentsLinked,
	}
	if b := res.BankAutoAssign; b != nil {
		bank["auto_assigned"] = len(b.Assigned)
		bank["by_reason"] = b.ByReason
		bank["declined_skipped"] = b.DeclinedSkipped
		if b.Error != "" {
			bank["error"] = b.Error
		}
	}
	d["bank"] = bank
	return d
}

func vippsAuditDetails(v *VippsBookingResult) map[string]any {
	out := map[string]any{}
	declined := 0
	if o := v.Orders; o != nil {
		out["repaired"] = o.Repaired
		out["orders_booked"] = o.Booked
		out["orders_skipped"] = len(o.SkippedDetails)
		declined += o.DeclinedSkipped
	}
	if a := v.AutoAssign; a != nil {
		out["auto_assigned"] = len(a.Assigned)
		out["needs_review"] = a.NeedsReview
		declined += a.DeclinedSkipped
	}
	if p := v.Payouts; p != nil {
		cleared := make([]string, 0, len(p.Cleared))
		for _, c := range p.Cleared {
			cleared = append(cleared, c.SettlementNumber)
		}
		capList(out, "payouts_cleared", cleared)
		out["payouts_waiting"] = len(p.Waiting)
		out["payouts_skipped"] = len(p.Skipped)
		pf := payoutsFailed(v)
		failed := make([]map[string]string, 0, len(pf))
		for _, f := range pf {
			failed = append(failed, map[string]string{"settlement": f.SettlementNumber, "reason": f.Reason})
		}
		capList(out, "payouts_failed", failed)
		if p.Error != "" {
			out["payouts_error"] = p.Error
		}
		declined += p.DeclinedSkipped
	}
	out["declined_skipped"] = declined
	return out
}

// capList sets m[key] to list, cut at autoBookingAuditListCap; the number cut
// is m[key+"_truncated"]. A nil list is written as empty.
func capList[T any](m map[string]any, key string, list []T) {
	if list == nil {
		list = []T{}
	}
	if len(list) > autoBookingAuditListCap {
		m[key+"_truncated"] = len(list) - autoBookingAuditListCap
		list = list[:autoBookingAuditListCap]
	}
	m[key] = list
}
