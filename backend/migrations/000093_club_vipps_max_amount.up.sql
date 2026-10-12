-- Per-club Vipps amount threshold (BRY-283).
--
-- When set, an invoice whose total_amount exceeds this value is offered bank
-- transfer (KID) only — no "Betal med Vipps" link — so the club avoids Vipps
-- transaction fees on large payments. NULL means no limit (Vipps offered for
-- any amount), which is the behaviour before this column existed. Enforced at
-- both link issuance (ensureFakturaOrderToken) and payment mint (mintOrderPayment).
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS vipps_max_amount NUMERIC(12, 2);
