-- Postgres does not support removing values from an enum without
-- rebuilding the type and rewriting every column that uses it. The
-- safe rollback for this change is to stop issuing the new values
-- from the application — the unused enum members are harmless.
SELECT 1;
