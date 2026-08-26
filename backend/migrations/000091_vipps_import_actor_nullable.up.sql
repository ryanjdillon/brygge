-- The nightly Vipps Report settlement sync (BRY-4) ingests vipps_imports with
-- no human actor. Allow imported_by to be NULL and treat NULL as an automated /
-- system import. Operator-triggered imports still record the acting user.
ALTER TABLE vipps_imports ALTER COLUMN imported_by DROP NOT NULL;
