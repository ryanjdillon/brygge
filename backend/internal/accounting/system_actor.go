package accounting

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SystemActorEmail is the address of every club's system actor. It lives in the
// .invalid TLD (RFC 2606), which can never be delivered or registered, and no
// path that sets users.email accepts a .invalid address for a person, so it
// cannot collide with a member's.
const SystemActorEmail = "automatikk@system.invalid"

// SystemActor returns the id of the club's system actor, the non-person users
// row the automatic booking run acts as (ACC-13). It creates the row on first
// use: the unique index on one system user per club is the INSERT's conflict
// target, so a repeated or concurrent first call adds nothing and every caller
// reads back the same id.
//
// There is no cache. A run resolves the id once at its start (one indexed
// query), so a deleted or re-created row can never leave a stale id behind.
//
// An email collision (a person holding SystemActorEmail) is not swallowed by
// the conflict clause: no system row exists then, so the INSERT's error is
// returned instead of an empty id.
func (s *Service) SystemActor(ctx context.Context, clubID string) (string, error) {
	insert := s.insertSystemActor
	if s.systemActorInsert != nil {
		insert = s.systemActorInsert
	}
	insertErr := insert(ctx, clubID)
	var id string
	err := s.db.QueryRow(ctx,
		`SELECT id::text FROM users WHERE club_id = $1 AND kind = 'system'`,
		clubID,
	).Scan(&id)
	switch {
	case err == nil:
		// A losing concurrent first call can fail on the (club_id, email)
		// unique index before the conflict clause sees the winner. The
		// winner's row is the answer then. Any other INSERT failure is real.
		if insertErr != nil && !isUniqueViolation(insertErr) {
			return "", fmt.Errorf("create system actor: %w", insertErr)
		}
		return id, nil
	case insertErr != nil:
		return "", fmt.Errorf("create system actor: %w", insertErr)
	case errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("system actor for club %s: not found after insert", clubID)
	default:
		return "", fmt.Errorf("read system actor: %w", err)
	}
}

func (s *Service) insertSystemActor(ctx context.Context, clubID string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO users (club_id, email, first_name, last_name, kind)
		 VALUES ($1, $2, 'Automatisk', 'bokføring', 'system')
		 ON CONFLICT (club_id) WHERE kind = 'system' DO NOTHING`,
		clubID, SystemActorEmail,
	)
	return err
}
