-- name: CreateSlip :one
INSERT INTO slips (
    club_id, number, section, length_m, width_m, depth_m,
    status, map_x, map_y
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
RETURNING *;

-- name: GetSlipByID :one
SELECT * FROM slips
WHERE id = $1 AND club_id = $2;

-- name: ListSlipsByClub :many
SELECT * FROM slips
WHERE club_id = $1
ORDER BY section, number;

-- name: UpdateSlipStatus :one
UPDATE slips SET
    status     = $3,
    updated_at = now()
WHERE id = $1 AND club_id = $2
RETURNING *;

-- name: AssignSlip :one
INSERT INTO slip_assignments (
    slip_id, user_id, club_id, andel_amount, andel_paid_at
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: ReleaseSlip :one
UPDATE slip_assignments SET
    released_at = now()
WHERE id = $1 AND club_id = $2 AND released_at IS NULL
RETURNING *;

-- name: GetActiveAssignment :one
SELECT * FROM slip_assignments
WHERE slip_id = $1 AND released_at IS NULL;

-- name: ListAssignmentsByClub :many
SELECT sa.*, s.number AS slip_number, s.section AS slip_section,
       u.full_name AS user_name
FROM slip_assignments sa
JOIN slips s ON s.id = sa.slip_id
JOIN users u ON u.id = sa.user_id
WHERE sa.club_id = $1 AND sa.released_at IS NULL
ORDER BY s.section, s.number;
