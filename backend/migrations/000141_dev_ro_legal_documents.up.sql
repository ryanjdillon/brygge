-- Privacy policy, statutes and terms text, versions and publish dates hold
-- no personal data (the table has no author or actor column), so the
-- read-only developer role reads it raw. Granted only where the role exists,
-- as on a deployed host.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON legal_documents TO brygge_dev_ro';
    END IF;
END
$$;
