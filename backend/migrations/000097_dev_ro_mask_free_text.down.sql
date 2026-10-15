DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON '
             || 'journal_entries, journal_lines, payments, invoice_lines, '
             || 'account_mapping_rules, vipps_message_rules, slips, dock_fingers '
             || 'TO brygge_dev_ro';
    END IF;
END
$$;
