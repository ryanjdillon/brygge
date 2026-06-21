-- No-op on purpose. On fresh databases `revision`/`published_at` are
-- created by 000066, so this migration does not own them and must not
-- drop them on the way down — 000066's own down drops the whole table.
SELECT 1;
