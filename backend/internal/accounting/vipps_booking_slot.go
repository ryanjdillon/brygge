package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrVippsBookingBusy means another Vipps booking of the club still held the
// booking slot when the wait ran out. Nothing was booked. Handlers answer 409 vipps_booking_busy where
// the whole action is the booking, and report booking_deferred where the
// request had other work (an ingest, an upload) that stands.
var ErrVippsBookingBusy = errors.New("vipps booking is already running")

// defaultVippsBookingSlotWait is how long a person's request waits for the
// club's Vipps booking slot. The server's WriteTimeout is 30 s and the
// request still has its own work to do, so a person is never queued behind a
// long booking: they get ErrVippsBookingBusy, or booking_deferred.
const defaultVippsBookingSlotWait = 10 * time.Second

// SetVippsBookingSlotWait sets how long a person's request waits for the
// club's Vipps booking slot before it answers busy or defers its booking;
// d <= 0 restores the default. Handler tests shorten it.
func (s *Service) SetVippsBookingSlotWait(d time.Duration) {
	s.vippsBookingSlotWait = d
}

// humanVippsBookingWait is the bounded wait a person's request uses.
func (s *Service) humanVippsBookingWait() time.Duration {
	if s.vippsBookingSlotWait > 0 {
		return s.vippsBookingSlotWait
	}
	return defaultVippsBookingSlotWait
}

// acquireVippsBooking takes the club's Vipps booking slot, waiting at most
// wait (the person's default when wait <= 0). ErrVippsBookingBusy when it is
// still held; ctx's own error when the request itself ended first. A free
// slot is taken without looking at the clock, so a tiny wait never loses a
// race against an idle slot.
//
// The booking primitives (AutoReconcileOrderCharges, AutoAssignVippsCharges,
// ClearReadyVippsPayouts) never take the slot themselves: a holder calls them
// directly, so the slot is never re-entered. Anything that books on its own,
// outside BookVippsQueue and sweepVippsPayoutsInSlot, takes the slot first.
func (s *Service) acquireVippsBooking(ctx context.Context, clubID string, wait time.Duration) (func(), error) {
	if release, ok := s.vippsBooking.tryAcquire(clubID); ok {
		return release, nil
	}
	if wait <= 0 {
		wait = s.humanVippsBookingWait()
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	release, err := s.vippsBooking.acquire(waitCtx, clubID)
	if err == nil {
		return release, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, ErrVippsBookingBusy
}

// VippsBookingResult is what one hold of the Vipps booking slot did. A nil
// field means its step did not run (an earlier one failed).
type VippsBookingResult struct {
	Orders     *VippsOrderReconcileResult
	AutoAssign *AutoAssignResult
	Payouts    *VippsPayoutSweepResult
}

// BookVippsQueue is the one guarded entry point for the Vipps booking tail,
// in one hold of the club's booking slot: the order-linked charges, the
// auto-assign rules, then the payout sweep that the first two make ready. It
// waits for the slot at most wait (a person's bound when wait <= 0; a caller
// with no person waiting passes a longer one) and then returns
// ErrVippsBookingBusy with nothing done.
//
// A failing step does not stop the later ones, as in Stripe's booking pass:
// the sweep re-checks every settlement itself, so what an earlier step did
// book can still clear. The result holds what ran; the error is the first
// failing step's. Only a request that has ended stops the tail. The sweep never
// fails the tail (ClearReadyVippsPayoutsReported carries its error in the
// result). Callers decide for themselves whether the club is on the per-charge
// model; this does not look.
func (s *Service) BookVippsQueue(ctx context.Context, clubID, actor string, wait time.Duration) (*VippsBookingResult, error) {
	release, err := s.acquireVippsBooking(ctx, clubID, wait)
	if err != nil {
		return nil, err
	}
	defer release()

	res := &VippsBookingResult{}
	var first error
	note := func(err error, step string) {
		if err != nil && first == nil {
			first = fmt.Errorf("%s: %w", step, err)
		}
	}
	res.Orders, err = s.AutoReconcileOrderCharges(ctx, clubID, actor)
	note(err, "booking order-linked charges")
	if ctx.Err() == nil {
		res.AutoAssign, err = s.AutoAssignVippsCharges(ctx, clubID, actor)
		note(err, "auto-assigning charges")
	}
	if ctx.Err() == nil {
		res.Payouts = s.ClearReadyVippsPayoutsReported(ctx, clubID, actor)
	}
	return res, first
}

// sweepVippsPayoutsInSlot runs the Vipps payout sweep in the club's booking
// slot as the last step of a bank sync, a bank upload or a backlog apply.
// deferred is true when the slot stayed busy past the person's wait: the
// caller keeps its own work and reports booking_deferred, and the next sync
// clears the payouts. A request that ended while waiting is
// reported the same way (its work stands; the sweep did not run).
func (s *Service) sweepVippsPayoutsInSlot(ctx context.Context, clubID, actor string) (sweep *VippsPayoutSweepResult, deferred bool) {
	release, err := s.acquireVippsBooking(ctx, clubID, 0)
	if err != nil {
		if !errors.Is(err, ErrVippsBookingBusy) {
			s.log.Warn().Err(err).Str("club_id", clubID).Msg("vipps payout sweep: waiting for the booking slot failed")
		}
		return nil, true
	}
	defer release()
	return s.ClearReadyVippsPayoutsReported(ctx, clubID, actor), false
}
