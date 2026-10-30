-- Scrub the identity that older audit rows kept about members who are already
-- erased (BRY-536). The delete, deactivate, reactivate, create and update
-- actions stored the member's name and address in audit_log.details, and
-- nothing removed it when the member was erased. New rows hold ids only and
-- every erasure path scrubs (audit.ScrubMemberPII); this brings the rows
-- written before that into line.
--
-- Only members whose identity is already gone are touched: an anonymised
-- address (<uuid>@deleted.local, or the constant deleted@deleted earlier
-- versions wrote) or no users row at all (a member hard-deleted by the old
-- erasure path). An active or merely deactivated member is still named
-- legitimately; the erasure that follows scrubs them. The key list repeats
-- audit.MemberPIIKeys, as of this migration.
--
-- Idempotent: a second run finds no row that still carries one of the keys.
-- audit_log is append-only by convention (no trigger or privilege stops an
-- UPDATE), and this is the one place besides ScrubMemberPII that rewrites it.

UPDATE audit_log a
   SET details = a.details - ARRAY[
           'email', 'full_name', 'first_name', 'last_name',
           'old_email', 'old_full_name',
           'phone', 'address_line', 'postal_code', 'city',
           'admin_notes'
       ]::text[]
 WHERE jsonb_typeof(a.details) = 'object'
   AND a.details ?| ARRAY[
           'email', 'full_name', 'first_name', 'last_name',
           'old_email', 'old_full_name',
           'phone', 'address_line', 'postal_code', 'city',
           'admin_notes'
       ]::text[]
   AND (
        (a.resource = 'user'
         AND a.resource_id ~ '^[0-9a-fA-F-]{36}$'
         AND (EXISTS (SELECT 1 FROM users u
                       WHERE u.id::text = a.resource_id
                         AND (u.email LIKE '%@deleted.local' OR u.email = 'deleted@deleted'))
              OR NOT EXISTS (SELECT 1 FROM users u WHERE u.id::text = a.resource_id)))
        OR (a.details->>'user_id' ~ '^[0-9a-fA-F-]{36}$'
            AND (EXISTS (SELECT 1 FROM users u
                          WHERE u.id::text = a.details->>'user_id'
                            AND (u.email LIKE '%@deleted.local' OR u.email = 'deleted@deleted'))
                 OR NOT EXISTS (SELECT 1 FROM users u WHERE u.id::text = a.details->>'user_id')))
   );
