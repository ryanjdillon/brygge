package accounting

import (
	"context"
	"fmt"
)

// anonymizedEmailSentinel marks a user row whose PII has already been scrubbed.
// Matches the value HandleDeleteUser writes so the purge and manual erasure
// converge on the same "no PII left" state.
const anonymizedEmailSentinel = "deleted@deleted"

// PurgeExpiredMemberPII anonymizes the PII of deactivated members whose
// financial records have all cleared the retention window (BRY-207). It never
// deletes the user row — invoices reference it (no ON DELETE cascade) and keep
// their snapshot identity (BRY-205) — it only scrubs name/email/phone/address
// in place. Idempotent: rows already scrubbed (sentinel email) are skipped, so
// it is safe to run on any schedule without duplicating work or audit entries.
// Returns the number of members purged this run.
func (s *Service) PurgeExpiredMemberPII(ctx context.Context) (int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, club_id FROM users u
		  WHERE u.deactivated_at IS NOT NULL
		    AND u.email <> $1
		    AND NOT EXISTS (
		        SELECT 1 FROM invoices i
		         WHERE i.user_id = u.id
		           AND make_date(EXTRACT(YEAR FROM i.issue_date)::int + $2, 1, 1) > now()
		    )`,
		anonymizedEmailSentinel, RetentionYears+1,
	)
	if err != nil {
		return 0, fmt.Errorf("select purge-eligible members: %w", err)
	}
	type target struct{ id, clubID string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.clubID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan purge target: %w", err)
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var purged int64
	for _, t := range targets {
		if err := s.purgeOneMember(ctx, t.id, t.clubID); err != nil {
			s.log.Error().Err(err).Str("user_id", t.id).Msg("failed to purge member PII")
			continue
		}
		purged++
	}
	return purged, nil
}

func (s *Service) purgeOneMember(ctx context.Context, userID, clubID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET email = $1, first_name = 'Deleted', last_name = 'User',
		        phone = '', address_line = '', postal_code = '', city = '',
		        updated_at = now()
		  WHERE id = $2`,
		anonymizedEmailSentinel, userID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, NULL, 'user.pii_purged', 'user', $2, '{"reason":"retention_cleared"}')`,
		clubID, userID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
