-- Per-club Vipps clearing + fee accounts for the per-charge reconciliation
-- model (BRY-225). The clearing account lets an individual Vipps charge be
-- booked independently of the batched bank payout, netting to ~0 per
-- settlement. Defaults match the codes the current single-bilag reconcile
-- already uses (2900 clearing, 7700 gebyr) so behaviour is unchanged until a
-- club customises them.
ALTER TABLE clubs
  ADD COLUMN vipps_clearing_account TEXT NOT NULL DEFAULT '2900',
  ADD COLUMN vipps_fee_account      TEXT NOT NULL DEFAULT '7700';
