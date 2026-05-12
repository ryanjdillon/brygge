-- Coerce price_items.metadata.beam_min / beam_max / length_min / length_max
-- from JSON strings to JSON numbers where applicable.
--
-- Background: an earlier version of the PricingAdminView form serialised
-- tier bounds as JSON strings (e.g. {"beam_min": "0"}). The backend
-- unmarshals these into *float64 and silently fails, leaving the tier
-- with a nil bound — which the bulk-invoice matcher then skips. The
-- form has been corrected to send numbers; this migration normalises
-- the data that was written by the old form.
UPDATE price_items
   SET metadata = (
     SELECT jsonb_object_agg(
       k,
       CASE
         WHEN k IN ('beam_min', 'beam_max', 'length_min', 'length_max')
              AND jsonb_typeof(v) = 'string'
              AND (v #>> '{}') ~ '^-?[0-9]+(\.[0-9]+)?$'
           THEN to_jsonb(((v #>> '{}')::numeric))
         ELSE v
       END
     )
       FROM jsonb_each(metadata) AS pairs(k, v)
   )
 WHERE metadata IS NOT NULL
   AND EXISTS (
     SELECT 1 FROM jsonb_each(metadata) AS m(k, v)
      WHERE k IN ('beam_min', 'beam_max', 'length_min', 'length_max')
        AND jsonb_typeof(v) = 'string'
   );
