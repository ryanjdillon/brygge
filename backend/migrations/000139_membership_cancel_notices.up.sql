-- Cancelling membership online (BRY-633) queues the member's receipt and the
-- board's notice, an undo queues the board's notice of it, and the daily
-- membership-end job queues the board's notice when it may not deactivate
-- the club's only active admin. They are membership_* kinds, which the
-- deliveries_parent_by_kind check (000138) already admits with neither parent.
--
-- Each is queued once per event: payload->>'ref' names the cancellation
-- (cancel:<user>:<cancelled_at>), the undo of one, or the end that was
-- refused (end_refused:<user>:<ends_on>), so a repeated request or the next
-- daily run finds the row and queues nothing.
CREATE UNIQUE INDEX IF NOT EXISTS idx_deliveries_membership_cancel_ref
    ON deliveries (kind, (payload->>'ref'))
    WHERE kind IN ('membership_cancel_receipt', 'membership_cancel_board_notice',
                   'membership_cancel_undone', 'membership_end_refused');
