package accounting

import (
	"context"
	"fmt"

	"github.com/brygge-klubb/brygge/internal/audit"
)

// legacyAnonymizedEmail is the constant address earlier versions wrote when
// anonymising a member. users carries UNIQUE (club_id, email), so only one
// member per club could ever hold it. The sweep still skips rows that have it;
// nothing writes it any more.
const legacyAnonymizedEmail = "deleted@deleted"

// ErasedEmailSuffix marks every user whose identity has been erased, by the
// purge sweep, the admin delete, or a processed GDPR request. The address is
// gen_random_uuid()::text plus this suffix, so it is unique per member and
// never collides on UNIQUE (club_id, email). The sweep skips rows that carry
// it, which is what makes erasure idempotent.
const ErasedEmailSuffix = "@deleted.local"

// PurgeExpiredMemberPII anonymizes the PII of deactivated members whose
// financial records have all cleared the retention window (BRY-207). It never
// deletes the user row — invoices reference it (no ON DELETE cascade) and keep
// their snapshot identity (BRY-205) — it only scrubs name/email/phone/address
// in place, and the same identity from the member's audit_log rows
// (audit.ScrubMemberPII). Idempotent: rows already scrubbed (erased or legacy sentinel email) are skipped, so
// it is safe to run on any schedule without duplicating work or audit entries.
// Returns the number of members purged this run.
func (s *Service) PurgeExpiredMemberPII(ctx context.Context) (int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, club_id FROM users u
		  WHERE u.deactivated_at IS NOT NULL
		    AND u.email <> $1
		    AND u.email NOT LIKE '%' || $3
		    AND NOT EXISTS (
		        SELECT 1 FROM invoices i
		         WHERE i.user_id = u.id
		           AND make_date(EXTRACT(YEAR FROM i.issue_date)::int + $2, 1, 1) > now()
		    )`,
		legacyAnonymizedEmail, RetentionYears+1, ErasedEmailSuffix,
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
		done, err := s.purgeOneMember(ctx, t.id, t.clubID)
		if err != nil {
			s.log.Error().Err(err).Str("user_id", t.id).Msg("failed to purge member PII")
			continue
		}
		if done {
			purged++
		}
	}
	return purged, nil
}

// PurgeExpiredRecipientPII scrubs the PII of external billing recipients
// (BRY-234) once they go dormant past the retention window. It only touches
// kind='private_nonmember' rows: organizations are legal persons, not personal
// data (GDPR N/A), so their identity is retained indefinitely for re-use (e.g.
// invoice auto-fill). A recipient is eligible only when it has NO invoice that
// is still open OR still within the bokføringsloven window — so an actively
// re-invoiced payer is never scrubbed. Idempotent (pii_purged_at gate); the
// invoice snapshot keeps the accounting record intelligible afterward.
//
// The dormancy match spans invoices linked by recipient_id AND by the snapshot
// identity (member_name / recipient_email) so an open, not-yet-linked invoice
// still protects its recipient. See BRY-234.
func (s *Service) PurgeExpiredRecipientPII(ctx context.Context) (int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT br.id, br.club_id
		   FROM billing_recipients br
		  WHERE br.kind = 'private_nonmember'
		    AND br.pii_purged_at IS NULL
		    AND (br.contact_person <> '' OR br.email <> '')
		    AND NOT EXISTS (
		        SELECT 1 FROM invoices i
		         WHERE (i.recipient_id = br.id
		                OR (br.contact_person <> '' AND i.member_name = br.contact_person)
		                OR (br.email <> '' AND i.recipient_email = br.email))
		           AND i.club_id = br.club_id
		           AND (
		               i.payment_id IS NULL
		               OR make_date(EXTRACT(YEAR FROM i.issue_date)::int + $1, 1, 1) > now()
		           )
		    )`,
		RetentionYears+1,
	)
	if err != nil {
		return 0, fmt.Errorf("select purge-eligible recipients: %w", err)
	}
	type target struct{ id, clubID string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.clubID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan recipient purge target: %w", err)
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var purged int64
	for _, t := range targets {
		if err := s.purgeOneRecipient(ctx, t.id, t.clubID); err != nil {
			s.log.Error().Err(err).Str("recipient_id", t.id).Msg("failed to purge recipient PII")
			continue
		}
		purged++
	}
	return purged, nil
}

func (s *Service) purgeOneRecipient(ctx context.Context, recipientID, clubID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE billing_recipients
		    SET contact_person = '', email = '', pii_purged_at = now()
		  WHERE id = $1 AND club_id = $2`,
		recipientID, clubID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, NULL, 'recipient.pii_purged', 'billing_recipient', $2, '{"reason":"retention_cleared"}')`,
		clubID, recipientID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// purgeOneMember scrubs one member's PII and reports whether it did. The
// update repeats the sweep's eligibility filters, so a member reactivated (or
// already erased) between the sweep's SELECT and this statement is left alone:
// no row changes, no audit row is written, and it is not an error.
func (s *Service) purgeOneMember(ctx context.Context, userID, clubID string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE users SET email = gen_random_uuid()::text || $1,
		        first_name = 'Deleted', last_name = 'User',
		        phone = '', address_line = '', postal_code = '', city = '',
		        updated_at = now()
		  WHERE id = $2
		    AND deactivated_at IS NOT NULL
		    AND email <> $3
		    AND email NOT LIKE '%' || $1`,
		ErasedEmailSuffix, userID, legacyAnonymizedEmail,
	)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := audit.ScrubMemberPII(ctx, tx, userID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, NULL, 'user.pii_purged', 'user', $2, '{"reason":"retention_cleared"}')`,
		clubID, userID,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
