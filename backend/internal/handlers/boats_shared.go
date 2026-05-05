package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// boatErrNotFound is returned by the shared boat upsert helpers when the
// row doesn't exist (for the given owner + club). Callers translate it
// into a 404 response.
var boatErrNotFound = errors.New("boat not found")

// createBoatForUser inserts a new boat row for ownerID. When approve is
// true (admin path), measurements_confirmed is set unconditionally with
// confirmed_by=actorID. When approve is false (member path), the
// existing model-match heuristic decides confirmation.
//
// This is the single source of truth for boat-create semantics; both
// MembersHandler.HandleCreateBoat and AdminUsersHandler.HandleCreateUserBoat
// dispatch through here.
func createBoatForUser(
	ctx context.Context,
	db *pgxpool.Pool,
	log zerolog.Logger,
	ownerID, clubID, actorID string,
	req createBoatRequest,
	approve bool,
) (*boat, error) {
	confirmed := false
	var confirmedBy *string
	var confirmedAt *time.Time

	switch {
	case approve:
		confirmed = true
		actor := actorID
		now := time.Now()
		confirmedBy = &actor
		confirmedAt = &now
	case req.BoatModelID != nil:
		var mLength, mBeam, mDraft *float64
		if err := db.QueryRow(ctx,
			`SELECT length_m, beam_m, draft_m FROM boat_models WHERE id = $1`,
			*req.BoatModelID,
		).Scan(&mLength, &mBeam, &mDraft); err == nil {
			confirmed = dimsMatch(req.LengthM, mLength) &&
				dimsMatch(req.BeamM, mBeam) &&
				dimsMatch(req.DraftM, mDraft)
		}
	}

	var b boat
	err := db.QueryRow(ctx,
		`INSERT INTO boats (user_id, club_id, name, type, manufacturer, model,
		                    length_m, beam_m, draft_m, weight_kg, registration_number,
		                    mmsi, call_sign,
		                    boat_model_id, measurements_confirmed, confirmed_by, confirmed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		 RETURNING id, user_id, club_id, name, type, manufacturer, model,
		           length_m, beam_m, draft_m, weight_kg, registration_number,
		           mmsi, call_sign,
		           boat_model_id, measurements_confirmed, confirmed_by, confirmed_at,
		           created_at, updated_at`,
		ownerID, clubID, req.Name, req.Type, req.Manufacturer, req.Model,
		req.LengthM, req.BeamM, req.DraftM, req.WeightKg, req.RegistrationNumber,
		req.MMSI, req.CallSign,
		req.BoatModelID, confirmed, confirmedBy, confirmedAt,
	).Scan(
		&b.ID, &b.UserID, &b.ClubID, &b.Name, &b.Type, &b.Manufacturer, &b.Model,
		&b.LengthM, &b.BeamM, &b.DraftM, &b.WeightKg, &b.RegistrationNumber,
		&b.MMSI, &b.CallSign,
		&b.BoatModelID, &b.MeasurementsConfirmed, &b.ConfirmedBy, &b.ConfirmedAt,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		log.Error().Err(err).Msg("createBoatForUser: insert")
		return nil, err
	}
	return &b, nil
}

// updateBoatForUser fetches the boat scoped to (boatID, ownerID, clubID),
// applies the partial update, recomputes measurement-confirmation status
// (or stamps it if approve=true), and writes the row back. Returns
// boatErrNotFound if no row matched.
func updateBoatForUser(
	ctx context.Context,
	db *pgxpool.Pool,
	log zerolog.Logger,
	boatID, ownerID, clubID, actorID string,
	req updateBoatRequest,
	approve bool,
) (*boat, error) {
	var current boat
	err := db.QueryRow(ctx,
		`SELECT id, user_id, club_id, name, type, manufacturer, model,
		        length_m, beam_m, draft_m, weight_kg, registration_number,
		        mmsi, call_sign,
		        boat_model_id, measurements_confirmed, confirmed_by, confirmed_at,
		        created_at, updated_at
		 FROM boats WHERE id = $1 AND user_id = $2 AND club_id = $3`,
		boatID, ownerID, clubID,
	).Scan(
		&current.ID, &current.UserID, &current.ClubID, &current.Name, &current.Type,
		&current.Manufacturer, &current.Model,
		&current.LengthM, &current.BeamM, &current.DraftM, &current.WeightKg,
		&current.RegistrationNumber,
		&current.MMSI, &current.CallSign,
		&current.BoatModelID, &current.MeasurementsConfirmed, &current.ConfirmedBy, &current.ConfirmedAt,
		&current.CreatedAt, &current.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, boatErrNotFound
	}
	if err != nil {
		log.Error().Err(err).Msg("updateBoatForUser: fetch")
		return nil, err
	}

	oldLength, oldBeam, oldDraft := current.LengthM, current.BeamM, current.DraftM
	applyBoatUpdates(&current, req)

	var confirmed bool
	var confirmedBy *string
	var confirmedAt *time.Time
	if approve {
		confirmed = true
		actor := actorID
		now := time.Now()
		confirmedBy = &actor
		confirmedAt = &now
	} else {
		// reuse the existing dimension-confirmation heuristic
		dimsChanged := !dimsMatch(current.LengthM, oldLength) ||
			!dimsMatch(current.BeamM, oldBeam) ||
			!dimsMatch(current.DraftM, oldDraft)
		if !dimsChanged {
			confirmed = current.MeasurementsConfirmed
			confirmedBy = current.ConfirmedBy
			confirmedAt = current.ConfirmedAt
		} else if current.BoatModelID != nil {
			var mLength, mBeam, mDraft *float64
			if mErr := db.QueryRow(ctx,
				`SELECT length_m, beam_m, draft_m FROM boat_models WHERE id = $1`,
				*current.BoatModelID,
			).Scan(&mLength, &mBeam, &mDraft); mErr == nil {
				if dimsMatch(current.LengthM, mLength) &&
					dimsMatch(current.BeamM, mBeam) &&
					dimsMatch(current.DraftM, mDraft) {
					confirmed = true
				}
			}
		}
	}

	var b boat
	err = db.QueryRow(ctx,
		`UPDATE boats
		 SET name = $4, type = $5, manufacturer = $6, model = $7,
		     length_m = $8, beam_m = $9, draft_m = $10, weight_kg = $11,
		     registration_number = $12, boat_model_id = $13,
		     measurements_confirmed = $14, confirmed_by = $15, confirmed_at = $16,
		     mmsi = $17, call_sign = $18,
		     updated_at = now()
		 WHERE id = $1 AND user_id = $2 AND club_id = $3
		 RETURNING id, user_id, club_id, name, type, manufacturer, model,
		           length_m, beam_m, draft_m, weight_kg, registration_number,
		           mmsi, call_sign,
		           boat_model_id, measurements_confirmed, confirmed_by, confirmed_at,
		           created_at, updated_at`,
		boatID, ownerID, clubID,
		current.Name, current.Type, current.Manufacturer, current.Model,
		current.LengthM, current.BeamM, current.DraftM, current.WeightKg,
		current.RegistrationNumber, current.BoatModelID,
		confirmed, confirmedBy, confirmedAt,
		current.MMSI, current.CallSign,
	).Scan(
		&b.ID, &b.UserID, &b.ClubID, &b.Name, &b.Type, &b.Manufacturer, &b.Model,
		&b.LengthM, &b.BeamM, &b.DraftM, &b.WeightKg, &b.RegistrationNumber,
		&b.MMSI, &b.CallSign,
		&b.BoatModelID, &b.MeasurementsConfirmed, &b.ConfirmedBy, &b.ConfirmedAt,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		log.Error().Err(err).Msg("updateBoatForUser: write")
		return nil, err
	}
	return &b, nil
}

// deleteBoatForUser scopes deletion to (boatID, ownerID, clubID) so an
// admin can't accidentally delete another club's boat via a stale URL.
func deleteBoatForUser(
	ctx context.Context,
	db *pgxpool.Pool,
	log zerolog.Logger,
	boatID, ownerID, clubID string,
) error {
	tag, err := db.Exec(ctx,
		`DELETE FROM boats WHERE id = $1 AND user_id = $2 AND club_id = $3`,
		boatID, ownerID, clubID,
	)
	if err != nil {
		log.Error().Err(err).Msg("deleteBoatForUser")
		return err
	}
	if tag.RowsAffected() == 0 {
		return boatErrNotFound
	}
	return nil
}
