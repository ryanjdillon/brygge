-- Per-club KID check-digit method (BRY faktura "feil KID" fix). Norwegian OCR
-- (KID) agreements register either Modulo-10 (Luhn) or Modulo-11; the payer's
-- bank rejects a KID that doesn't validate under the registered method.
-- brygge historically generated mod-10 KIDs, which the club's mod-11 agreement
-- rejected as "feil KID". Default mod11 (the common Norwegian OCR default);
-- historical mod-10 KIDs still validate via finance.ValidateKIDAny.
ALTER TABLE clubs ADD COLUMN kid_check_method TEXT NOT NULL DEFAULT 'mod11';
