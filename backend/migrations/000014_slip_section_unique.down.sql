ALTER TABLE slips DROP CONSTRAINT IF EXISTS slips_club_id_section_number_key;
ALTER TABLE slips ADD CONSTRAINT slips_club_id_number_key UNIQUE (club_id, number);
