-- No-op down. Reverting numbers back to strings would silently break
-- tier matching again and isn't useful; keep the corrected types.
SELECT 1;
