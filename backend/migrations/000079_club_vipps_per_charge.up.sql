-- Per-club switch onto the per-charge Vipps reconciliation model (BRY-228).
-- Default false: existing clubs keep the single-bilag behaviour until they are
-- cut over. When true, the on-demand bank-sync stops auto-booking batched Vipps
-- payouts (the per-charge Tildel flow + payout auto-clear own them instead), and
-- the Tildel UI surfaces individual charges. The cutover command regenerates
-- open-period reconciliations before flipping this on.
ALTER TABLE clubs
  ADD COLUMN vipps_per_charge_enabled BOOLEAN NOT NULL DEFAULT false;
