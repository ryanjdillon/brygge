-- Slip numbers are only unique within a dock/section. A1 and B1 should
-- coexist; (A,1) and (A,1) should not.
ALTER TABLE slips DROP CONSTRAINT IF EXISTS slips_club_id_number_key;
ALTER TABLE slips ADD CONSTRAINT slips_club_id_section_number_key UNIQUE (club_id, section, number);
