-- Public-facing contact details surfaced on the Contact page. Address
-- already exists; phone and vhf_channel are added so the contact card
-- can show "+47 ..." and "Ch 73" without hard-coded fallbacks.
ALTER TABLE clubs ADD COLUMN phone        TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN vhf_channel  TEXT NOT NULL DEFAULT '';
