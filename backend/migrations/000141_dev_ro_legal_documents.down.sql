DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'REVOKE SELECT ON legal_documents FROM brygge_dev_ro';
    END IF;
END
$$;
