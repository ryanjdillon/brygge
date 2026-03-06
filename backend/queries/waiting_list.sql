-- name: AddToWaitingList :one
INSERT INTO waiting_list_entries (
    user_id, club_id, position, is_local, status
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetWaitingListPosition :one
SELECT * FROM waiting_list_entries
WHERE user_id = $1 AND club_id = $2 AND status = 'active';

-- name: ListWaitingListByClub :many
SELECT wle.*, u.full_name, u.email, u.phone
FROM waiting_list_entries wle
JOIN users u ON u.id = wle.user_id
WHERE wle.club_id = $1
  AND wle.status IN ('active', 'offered')
ORDER BY wle.position;

-- name: UpdateWaitingListEntry :one
UPDATE waiting_list_entries SET
    position       = COALESCE(sqlc.narg('position'), position),
    status         = COALESCE(sqlc.narg('status'), status),
    offer_deadline = sqlc.narg('offer_deadline'),
    updated_at     = now()
WHERE id = $1 AND club_id = $2
RETURNING *;

-- name: OfferSlip :one
UPDATE waiting_list_entries SET
    status         = 'offered',
    offer_deadline = $3,
    updated_at     = now()
WHERE id = $1 AND club_id = $2
RETURNING *;

-- name: NextWaitingListPosition :one
SELECT COALESCE(MAX(position), 0) + 1 AS next_position
FROM waiting_list_entries
WHERE club_id = $1;
