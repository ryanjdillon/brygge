package accounting

import (
	"context"
	"fmt"

	"github.com/brygge-klubb/brygge/internal/audit"
)

// LegacyAnonymizedEmail is the constant address earlier versions wrote when
// anonymising a member. users carries UNIQUE (club_id, email), so only one
// member per club could ever hold it. The sweep still skips rows that have it,
// as does anything that must not match an erased member; nothing writes it any
// more.
const LegacyAnonymizedEmail = "deleted@deleted"

// ErasedEmailSuffix marks every user whose identity has been erased, by the
// purge sweep, the admin delete, or a processed GDPR request. The address is
// gen_random_uuid()::text plus this suffix, so it is unique per member and
// never collides on UNIQUE (club_id, email). The sweep skips rows that carry
// it, which is what makes erasure idempotent.
const ErasedEmailSuffix = "@deleted.local"

// DeleteMemberMailLog deletes a member's outgoing-mail log: email_sends rows
// that name the member or went to their current address in their club (their
// delivery events cascade), and SES delivery events that matched no send but
// carry that address. Recipient addresses are personal data and the log has no
// retention obligation, so every path that erases a member's identity calls
// this, in its transaction and before the address is overwritten.
func DeleteMemberMailLog(ctx context.Context, db audit.Execer, userID string) error {
	if _, err := db.Exec(ctx,
		`DELETE FROM email_sends s USING users u
		  WHERE u.id = $1 AND s.club_id = u.club_id
		    AND (s.user_id = u.id OR lower(s.recipient) = lower(u.email))`,
		userID,
	); err != nil {
		return fmt.Errorf("delete member email sends: %w", err)
	}
	if _, err := db.Exec(ctx,
		`DELETE FROM mail_delivery_events e USING users u
		  WHERE u.id = $1 AND e.send_id IS NULL AND lower(e.recipient) = lower(u.email)`,
		userID,
	); err != nil {
		return fmt.Errorf("delete member mail events: %w", err)
	}
	return nil
}

// EraseMemberMembershipData clears what the join and cancel flows hold about a
// member outside the users identity columns: the member's own free-text reason
// for cancelling, the register check of their home address (its municipality
// and when it was checked), and every join request made with their current
// address in their club. Neither has a retention obligation, so every path that erases a
// member's identity calls this, in its transaction and before the address is
// overwritten.
func EraseMemberMembershipData(ctx context.Context, db audit.Execer, userID string) error {
	if _, err := db.Exec(ctx,
		`DELETE FROM membership_applications a USING users u
		  WHERE u.id = $1 AND a.club_id = u.club_id AND lower(a.email) = lower(u.email)`,
		userID,
	); err != nil {
		return fmt.Errorf("delete member join requests: %w", err)
	}
	if _, err := db.Exec(ctx,
		`UPDATE users
		    SET membership_cancel_reason = NULL,
		        address_verified = NULL, address_kommunenummer = NULL, address_verified_at = NULL
		  WHERE id = $1`,
		userID,
	); err != nil {
		return fmt.Errorf("clear cancellation reason and address check: %w", err)
	}
	return nil
}

// NonRetainedMemberRecords names what EraseNonRetainedMemberData erases, for
// the audit trail of an erasure. Keep it in step with the three steps below.
func NonRetainedMemberRecords() []string {
	return []string{
		// DeleteMemberMailLog
		"email_sends", "mail_delivery_events",
		// EraseMemberMembershipData
		"membership_applications", "membership_cancel_reason", "address_verification",
		// EraseMemberProfileData
		"push_subscriptions", "notification_preferences", "communication_preferences",
		"newsletter_consent", "admin_notes", "boats",
	}
}

// EraseNonRetainedMemberData runs every erasure step for data with no
// retention obligation: the mail log, the membership data and the profile
// data. Every path that erases a member calls it in its transaction, before
// the address is overwritten (the first two match on it), and a GDPR request
// whose identity erasure is deferred calls it at once.
func EraseNonRetainedMemberData(ctx context.Context, db audit.Execer, userID string) error {
	if err := DeleteMemberMailLog(ctx, db, userID); err != nil {
		return err
	}
	if err := EraseMemberMembershipData(ctx, db, userID); err != nil {
		return err
	}
	return EraseMemberProfileData(ctx, db, userID)
}

// EraseMemberProfileData removes what the club holds about a member, outside
// the identity columns, that no bookkeeping rule requires it to keep: push
// subscriptions, notification and email preferences, the newsletter consent,
// the board's admin_notes, and the member's boats. It runs as part of
// EraseNonRetainedMemberData; none of it waits for retention.
//
// Consents to the club's legal documents (terms, privacy, statutes, harbour
// rules, terms of sale) stay until the member's financial retention ends
// (BRD-18): they prove what the member accepted (GDPR Art. 5(2) and 7(1)) and
// that the terms of sale were agreed for sales the club still holds under
// bokføringsloven. They name no one once the member is anonymised.
// purgeOneMember and PurgeRetentionClearedConsents delete them.
//
// A boat is deleted unless a retained row still points at it: an invoice
// line (bookkeeping) or an active slip assignment, which must name a boat
// (active_assignment_has_boat). Such a boat stays with its identifying
// columns blanked (name, registration number, MMSI, call sign); its model and
// dimensions stay, because they explain the price on the line and identify
// no one. Released slip assignments and waiting-list entries lose the link
// (ON DELETE SET NULL). Invoice line text is a bookkeeping document and is
// never rewritten.
func EraseMemberProfileData(ctx context.Context, db audit.Execer, userID string) error {
	for _, stmt := range []string{
		`DELETE FROM push_subscriptions WHERE user_id = $1`,
		`DELETE FROM notification_preferences WHERE user_id = $1`,
		`DELETE FROM communication_preferences WHERE user_id = $1`,
		`DELETE FROM user_consents WHERE user_id = $1 AND consent_type = 'newsletter'`,
		`UPDATE users SET admin_notes = '' WHERE id = $1 AND admin_notes <> ''`,
	} {
		if _, err := db.Exec(ctx, stmt, userID); err != nil {
			return fmt.Errorf("erase member profile data: %w", err)
		}
	}
	if _, err := db.Exec(ctx,
		`UPDATE boats b SET name = '', registration_number = '', mmsi = '', call_sign = '', updated_at = now()
		  WHERE b.user_id = $1 AND (`+boatRetainedSQL+`)
		    AND (b.name <> '' OR b.registration_number <> '' OR b.mmsi <> '' OR b.call_sign <> '')`,
		userID,
	); err != nil {
		return fmt.Errorf("anonymise retained boats: %w", err)
	}
	if _, err := db.Exec(ctx,
		`DELETE FROM boats b WHERE b.user_id = $1 AND NOT (`+boatRetainedSQL+`)`,
		userID,
	); err != nil {
		return fmt.Errorf("delete member boats: %w", err)
	}
	return nil
}

// boatRetainedSQL is true for a boat b that a kept row still references.
const boatRetainedSQL = `EXISTS (SELECT 1 FROM invoice_lines il WHERE il.boat_id = b.id)
	OR EXISTS (SELECT 1 FROM slip_assignments sa WHERE sa.boat_id = b.id AND sa.released_at IS NULL)`

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
		  WHERE u.kind = 'person'
		    AND u.deactivated_at IS NOT NULL
		    AND u.email <> $1
		    AND u.email NOT LIKE '%' || $3
		    AND NOT EXISTS (
		        SELECT 1 FROM invoices i
		         WHERE i.user_id = u.id
		           AND make_date(EXTRACT(YEAR FROM i.issue_date)::int + $2, 1, 1) > now()
		    )`,
		LegacyAnonymizedEmail, RetentionYears+1, ErasedEmailSuffix,
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

// PurgeRetentionClearedConsents deletes the legal-document consents of
// members whose identity is already erased (an admin delete or a GDPR request
// processed before retention cleared) once their financial retention has
// ended (MemberFinancialRetention). Erasure keeps those consents as proof of
// what the member accepted while the club still holds their sales records
// (BRD-18); after retention nothing needs them. Members erased by the sweep
// itself lose them in purgeOneMember. Idempotent; returns the rows deleted.
func (s *Service) PurgeRetentionClearedConsents(ctx context.Context) (int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT u.id, u.club_id FROM users u JOIN user_consents c ON c.user_id = u.id
		  WHERE u.kind = 'person' AND (u.email LIKE '%' || $1 OR u.email = $2)`,
		ErasedEmailSuffix, LegacyAnonymizedEmail,
	)
	if err != nil {
		return 0, fmt.Errorf("select erased members with consents: %w", err)
	}
	type target struct{ id, clubID string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.clubID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan consent purge target: %w", err)
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var deleted int64
	for _, t := range targets {
		ret, err := MemberFinancialRetention(ctx, s.db, t.clubID, t.id)
		if err != nil {
			return deleted, fmt.Errorf("evaluate retention of %s: %w", t.id, err)
		}
		if !ret.Eligible {
			continue
		}
		tag, err := s.db.Exec(ctx, `DELETE FROM user_consents WHERE user_id = $1`, t.id)
		if err != nil {
			return deleted, fmt.Errorf("delete consents of %s: %w", t.id, err)
		}
		deleted += tag.RowsAffected()
	}
	return deleted, nil
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

	// Before the update overwrites the address it matches on. An ineligible
	// member returns below without committing, so this rolls back with it.
	if err := EraseNonRetainedMemberData(ctx, tx, userID); err != nil {
		return false, err
	}

	tag, err := tx.Exec(ctx,
		`UPDATE users SET email = gen_random_uuid()::text || $1,
		        first_name = 'Deleted', last_name = 'User',
		        phone = '', address_line = '', postal_code = '', city = '',
		        updated_at = now()
		  WHERE id = $2
		    AND kind = 'person'
		    AND deactivated_at IS NOT NULL
		    AND email <> $3
		    AND email NOT LIKE '%' || $1`,
		ErasedEmailSuffix, userID, LegacyAnonymizedEmail,
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
	// The sweep selects only members past retention, so the legal-document
	// consents kept through erasure go now too.
	if _, err := tx.Exec(ctx, `DELETE FROM user_consents WHERE user_id = $1`, userID); err != nil {
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
