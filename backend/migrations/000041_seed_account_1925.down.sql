-- No-op: removing an account that may already be referenced by journal
-- entries or bank imports could break referential integrity. If a club
-- truly wants 1925 gone, they should delete it manually after detaching
-- any references.
SELECT 1;
