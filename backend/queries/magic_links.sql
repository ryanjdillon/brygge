-- name: CreateMagicLink :exec
INSERT INTO magic_links (token, email, club_id, expires_at)
VALUES ($1, $2, $3, $4);

-- name: ConsumeMagicLink :one
UPDATE magic_links
SET used = true
WHERE token = $1
  AND used = false
  AND expires_at > NOW()
RETURNING email, club_id;

-- name: PurgeExpiredMagicLinks :execresult
DELETE FROM magic_links
WHERE expires_at < NOW();
