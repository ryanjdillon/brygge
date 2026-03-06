-- name: CreateBooking :one
INSERT INTO bookings (
    resource_id, user_id, club_id, start_date, end_date,
    status, guest_name, guest_email, guest_phone, payment_id, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: GetBookingByID :one
SELECT * FROM bookings
WHERE id = $1 AND club_id = $2;

-- name: ListBookingsByResource :many
SELECT * FROM bookings
WHERE resource_id = $1 AND club_id = $2
  AND status != 'cancelled'
ORDER BY start_date;

-- name: ListBookingsByUser :many
SELECT b.*, r.name AS resource_name, r.type AS resource_type
FROM bookings b
JOIN resources r ON r.id = b.resource_id
WHERE b.user_id = $1 AND b.club_id = $2
ORDER BY b.start_date DESC;

-- name: UpdateBookingStatus :one
UPDATE bookings SET
    status     = $3,
    updated_at = now()
WHERE id = $1 AND club_id = $2
RETURNING *;

-- name: CheckAvailability :many
SELECT * FROM bookings
WHERE resource_id = $1
  AND status != 'cancelled'
  AND start_date < $3
  AND end_date > $2;
