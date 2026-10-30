-- Rewrite the audit rows of the club settings update to the names of the
-- fields set. PATCH /admin/settings/site wrote its whole request into
-- audit_log.details, under "new": the Anthropic API key (the one secret field
-- of that request) in clear text, and the board members' email addresses,
-- phone and address. New rows hold {"fields":[...]} and never a value; this
-- brings the rows written before that into line.
--
-- This deliberately rewrites history: such a row keeps who did it, when, and
-- which fields were set, but no longer what they were set to. The names are
-- the keys of "new" (or of the row itself, for a row without one), sorted
-- bytewise as the handler sorts them.
--
-- Data only. Idempotent: a row already of the form {"fields":[...]}, and a row
-- whose details are not an object, is left alone, so a second run changes
-- nothing. audit_log is append-only by convention (no trigger or privilege
-- stops an UPDATE); like audit.ScrubMemberPII and 000124, this rewrites it to
-- remove data that must not be kept.

UPDATE audit_log a
   SET details = jsonb_build_object('fields', COALESCE(
           (SELECT jsonb_agg(k ORDER BY k COLLATE "C")
              FROM jsonb_object_keys(
                       CASE WHEN jsonb_typeof(a.details->'new') = 'object'
                            THEN a.details->'new'
                            ELSE a.details - 'new' END) AS k),
           '[]'::jsonb))
 WHERE a.action = 'update_financial_settings'
   AND jsonb_typeof(a.details) = 'object'
   AND a.details IS DISTINCT FROM jsonb_build_object('fields', a.details->'fields');
