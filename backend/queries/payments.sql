-- name: CreatePayment :one
INSERT INTO payments (
    club_id, user_id, type, amount, currency, vipps_reference, status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetPaymentByID :one
SELECT * FROM payments
WHERE id = $1 AND club_id = $2;

-- name: UpdatePaymentStatus :one
UPDATE payments SET
    status  = $3,
    paid_at = CASE WHEN $3 = 'completed' THEN now() ELSE paid_at END
WHERE id = $1 AND club_id = $2
RETURNING *;

-- name: ListPaymentsByUser :many
SELECT * FROM payments
WHERE user_id = $1 AND club_id = $2
ORDER BY created_at DESC;

-- name: GetPaymentByVippsReference :one
SELECT * FROM payments
WHERE vipps_reference = $1 AND club_id = $2;
