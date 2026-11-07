package audit

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Action constants for audit events.
const (
	// Auth events. A login is recorded per attempt that presented a
	// credential (BRY-659); details name the method and, for a failure, the
	// reason, never the token, its hash, the session or the address.
	ActionLoginSuccess = "auth.login_success"
	ActionLoginFailed  = "auth.login_failed"
	ActionLogout       = "auth.logout"
	ActionTokenRevoked = "auth.token_revoked" // #nosec G101 -- not a credential
	ActionInvalidToken = "auth.invalid_token" // #nosec G101 -- not a credential

	// TOTP events
	ActionTOTPVerified         = "admin.totp_verified"
	ActionTOTPRecoveryRedeemed = "admin.totp_recovery_redeemed"
	ActionTOTPCodesRegenerated = "admin.totp_codes_regenerated"
	ActionTOTPAdminDisabled    = "admin.totp_disabled_by_admin"
	ActionPasskeyAdminReset    = "admin.passkey_reset_by_admin"

	// Admin user operations
	ActionUserRoleUpdated       = "user.role_updated"
	ActionUserDeleted           = "user.deleted"
	ActionUserDeactivated       = "user.deactivated"
	ActionUserReactivated       = "user.reactivated"
	ActionMemberPIIPurged       = "user.pii_purged"
	ActionUserMailProvisioned   = "user.mail_provisioned"
	ActionUserMailDeprovisioned = "user.mail_deprovisioned"
	// ActionUserRejoinBlockedSet is the board setting or clearing a member's
	// "can't rejoin" mark through the admin PATCH (BRY-476).
	ActionUserRejoinBlockedSet = "user.rejoin_blocked_set"

	// Harbor share import (BRY-139): one row per member it changed and a
	// summary of the counts. A PATCH logs the same change as update_user.
	ActionUserHarborShareSet       = "user.harbor_share_set"
	ActionUserHarborSharesImported = "user.harbor_shares_imported"

	// Admin slip operations
	ActionSlipCreated  = "slip.created"
	ActionSlipAssigned = "slip.assigned"
	ActionSlipReleased = "slip.released"

	// Content operations
	ActionEventCreated             = "event.created"
	ActionEventDeleted             = "event.deleted"
	ActionPricingCreated           = "pricing.created"
	ActionPricingUpdated           = "pricing.updated"
	ActionPricingArchived          = "pricing.archived"
	ActionPricingDeleted           = "pricing.deleted"
	ActionPricingExemptionsUpdated = "pricing.exemptions_updated"
	ActionBroadcastSent            = "broadcast.sent"
	ActionDocumentUploaded         = "document.uploaded"
	ActionDocumentDeleted          = "document.deleted"

	// Finance operations
	// ActionFinancialSettingsUpdated is the club settings PATCH. Its string
	// predates the <resource>.<verb> convention and is kept so stored rows
	// still match. The row holds the names of the fields changed, never values.
	ActionFinancialSettingsUpdated    = "update_financial_settings"
	ActionInvoiceCreated              = "invoice.created"
	ActionInvoiceVoided               = "invoice.voided"
	ActionInvoicePDFGenerated         = "invoice.pdf_generated"
	ActionInvoiceEmailed              = "invoice.emailed"
	ActionInvoiceReminded             = "invoice.reminded"
	ActionInvoiceOnlinePaymentSet     = "invoice.vipps_link_set" // value predates card payments; kept so stored rows still match
	ActionInvoiceCreditLinked         = "invoice.credit_linked"
	ActionInvoiceCreditUnlinked       = "invoice.credit_unlinked"
	ActionInvoiceRegenerated          = "invoice.pdf_regenerated"
	ActionInvoiceDeliveryLogViewed    = "invoice.delivery_log_viewed"
	ActionFinanceCSVExported          = "finance.csv_exported"
	ActionAccountingInvoicesResplit   = "accounting.invoices_resplit"
	ActionAccountingVippsDraftsPosted = "accounting.vipps_drafts_posted"
	ActionAccountingDraftDeleted      = "accounting.draft_deleted"
	ActionAccountingStripeSynced      = "accounting.stripe_balance_synced"
	ActionPaymentCreated              = "payment.created"
	ActionPaymentViewed               = "finance.payment_viewed"

	// A faktura recipient taken off the SES suppression list (BRY-590)
	ActionInvoiceRecipientUnsuppressed = "invoice.recipient_unsuppressed"

	// Posted Vipps charges moved off the 3900 fallback (BRY-557)
	ActionAccountingVippsFallbackReclassified = "accounting.vipps_fallback_reclassified"

	// Bank row reconciliation (DIL-392)
	ActionBankRowAssignedInvoice = "bank_row.assigned_invoice"
	ActionBankRowAutoAssigned    = "bank_row.auto_assigned"
	ActionBankRowAssignedAccount = "bank_row.assigned_account"
	ActionBankRowDismissed       = "bank_row.dismissed"
	ActionBankRowRefreshed       = "bank_row.refreshed"
	ActionBankRowUnassigned      = "bank_row.unassigned"
	ActionBankRowRefundPaired    = "bank_row.refund_paired"

	// Per-charge Vipps reconciliation (BRY-227)
	ActionVippsChargeAssignedInvoice = "vipps_charge.assigned_invoice"
	ActionVippsChargeBilag           = "vipps_charge.bilag"
	ActionVippsChargeDismissed       = "vipps_charge.dismissed"
	ActionVippsChargeAutoAssigned    = "vipps_charge.auto_assigned"
	ActionVippsChargeUnassigned      = "vipps_charge.unassigned"
	ActionVippsPayoutCleared         = "vipps_payout.cleared"

	// A Stripe payout cleared by a person (BRY-465)
	ActionStripePayoutCleared = "stripe_payout.cleared"

	// A Stripe balance transaction in review settled by a person (BRY-466)
	ActionStripeTxnAssignedInvoice = "stripe_txn.assigned_invoice"
	ActionStripeTxnAssignedAccount = "stripe_txn.assigned_account"
	ActionStripeTxnDismissed       = "stripe_txn.dismissed"
	ActionStripeTxnUndismissed     = "stripe_txn.undismissed"

	// A Stripe booking undone, and a held row handed back to the booking
	// pass (BRY-467)
	ActionStripeTxnUnassigned = "stripe_txn.unassigned"
	ActionStripeTxnRebooked   = "stripe_txn.rebooked"

	// A card refund (or refund failure) booked by the Stripe booking pass,
	// a duplicate capture booked with the refund that returned it, and a
	// credit note a refund issued (BRY-446)
	ActionStripeTxnRefundBooked      = "stripe_txn.refund_booked"
	ActionStripeTxnDuplicateRefunded = "stripe_txn.duplicate_refunded"
	ActionInvoiceRefundCreditIssued  = "invoice.refund_credit_issued"

	// A refund of a card payment requested from the refund button (BRY-497)
	ActionOrderRefundRequested = "order.refund_requested"

	// A new faktura issued to collect again after a refund credited the
	// original in full (BRY-498)
	ActionInvoiceReissuedAfterRefund = "invoice.reissued_after_refund"

	// A card dispute (or its reversal) booked by the Stripe booking pass
	// (BRY-446)
	ActionStripeTxnDisputeBooked = "stripe_txn.dispute_booked"

	// The payment overview's filter groups and their price items (BRY-522)
	ActionPaymentFilterGroupCreated      = "payment_filter_group.created"
	ActionPaymentFilterGroupUpdated      = "payment_filter_group.updated"
	ActionPaymentFilterGroupItemsUpdated = "payment_filter_group.items_updated"
	ActionPaymentFilterGroupsReordered   = "payment_filter_group.reordered"
	ActionPaymentFilterGroupDeleted      = "payment_filter_group.deleted"

	// Payer aliases taught from bank Tildel or the Vipps queue (BRY-383)
	ActionPayerAliasTaught  = "payer_alias.taught"
	ActionPayerAliasRemoved = "payer_alias.removed"

	// Developer / DB inspection (DIL-365, BRY-115)
	ActionDeveloperQuery        = "developer.db_query"
	ActionDeveloperTokenCreated = "developer.token_created"
	ActionDeveloperTokenRevoked = "developer.token_revoked"

	// Booking operations
	ActionBookingConfirmed = "booking.confirmed"

	// Shared-inbox ACL reconciler (DIL-275/276)
	ActionInboxACLChanged    = "inbox.acl_changed"
	ActionInboxACLSyncFailed = "inbox.acl_sync_failed"

	// Shared-inbox read path (DIL-277)
	ActionInboxThreadViewed   = "inbox.thread_viewed"
	ActionInboxThreadArchived = "inbox.thread_archived"
	ActionInboxMarkRead       = "inbox.mark_read"

	// Shared-inbox compose path (DIL-278)
	ActionInboxMessageSent = "inbox.message_sent"

	// Reference years (BRY-418, ACC-12)
	ActionAccountingPeriodReferenceSet     = "accounting.period_reference_set"
	ActionAccountingPeriodReferenceCleared = "accounting.period_reference_cleared"
	ActionBankRowUndismissed               = "bank_row.undismissed"

	// Self-service membership join (BRY-629). The request is logged with a
	// hash of the address only; the confirmation names the user it created.
	// Settling the join payment (BRY-630) is logged as the grant, naming its
	// source (online, kid or sweep); a refund or credit of the payment is
	// recorded for the board and never revokes the membership; a paid join
	// the integrity check withholds is recorded for the board too. A join
	// left unpaid for 60 days expires (BRY-631): the applicant is deactivated,
	// with the join faktura credited when one was issued; one with part of
	// the money in is left for the board's review instead.
	ActionMembershipGrantWithheld     = "membership.grant_withheld"
	ActionMembershipGranted           = "membership.granted"
	ActionMembershipJoinConfirmed     = "membership.join_confirmed"
	ActionMembershipJoinRefunded      = "membership.join_refunded"
	ActionMembershipJoinRequested     = "membership.join_requested"
	ActionMembershipJoinExpired       = "membership.join_expired"
	ActionMembershipJoinExpiredReview = "membership.join_expired_review"

	// Cancelling online (BRY-633): the member's cancellation and its undo,
	// and the daily job deactivating the member once the cancellation's end
	// date has passed.
	ActionMembershipCancelled    = "membership.cancelled"
	ActionMembershipCancelUndone = "membership.cancel_undone"
	ActionMembershipEnded        = "membership.ended"

	// GDPR operations
	ActionGDPRExportRequested       = "gdpr.export_requested"
	ActionGDPRDeletionRequested     = "gdpr.deletion_requested"
	ActionGDPRDeletionCancelled     = "gdpr.deletion_cancelled"
	ActionGDPRDeletionProcessed     = "gdpr.deletion_processed"
	ActionLegalDocumentCreated      = "legal_document.created"
	ActionNotificationConfigUpdated = "notification_config.updated"
)

type Entry struct {
	ClubID     *string
	ActorID    *string
	ActorIP    string
	Action     string
	Resource   string
	ResourceID string
	Details    any
}

type Service struct {
	db  *pgxpool.Pool
	log zerolog.Logger

	// clubIDs caches ClubIDForSlug's successful lookups; a club's id never
	// changes, so one read per slug per process is enough.
	clubIDsMu sync.Mutex
	clubIDs   map[string]string
}

func NewService(db *pgxpool.Pool, log zerolog.Logger) *Service {
	return &Service{
		db:  db,
		log: log.With().Str("component", "audit").Logger(),
	}
}

// LogAction is a convenience method for handler code that has claims and request context.
func (s *Service) LogAction(ctx context.Context, clubID, actorID, actorIP, action, resource, resourceID string, details any) {
	s.Log(ctx, Entry{
		ClubID:     strPtr(clubID),
		ActorID:    strPtr(actorID),
		ActorIP:    actorIP,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		Details:    details,
	})
}

// ClubIDForSlug returns the id of the club with slug, or "" when it cannot be
// read. It is for a row with no user to take the club from, such as a login
// with an unknown token, so the row is still scoped to the deployment's club.
// A found id is cached; a failed lookup is not, so the next row tries again.
func (s *Service) ClubIDForSlug(ctx context.Context, slug string) string {
	if s == nil || s.db == nil || slug == "" {
		return ""
	}
	s.clubIDsMu.Lock()
	id, ok := s.clubIDs[slug]
	s.clubIDsMu.Unlock()
	if ok {
		return id
	}
	if err := s.db.QueryRow(ctx, `SELECT id::text FROM clubs WHERE slug = $1`, slug).Scan(&id); err != nil {
		s.log.Warn().Err(err).Msg("audit: resolve club by slug")
		return ""
	}
	s.clubIDsMu.Lock()
	if s.clubIDs == nil {
		s.clubIDs = make(map[string]string)
	}
	s.clubIDs[slug] = id
	s.clubIDsMu.Unlock()
	return id
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Service) Log(ctx context.Context, entry Entry) {
	if s.db == nil {
		return
	}

	var detailsJSON []byte
	if entry.Details != nil {
		var err error
		detailsJSON, err = json.Marshal(entry.Details)
		if err != nil {
			s.log.Error().Err(err).Str("action", entry.Action).Msg("failed to marshal audit details")
			return
		}
	}

	_, err := s.db.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, actor_ip, action, resource, resource_id, details)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		entry.ClubID, entry.ActorID, entry.ActorIP,
		entry.Action, entry.Resource, entry.ResourceID, detailsJSON,
	)
	if err != nil {
		s.log.Error().Err(err).Str("action", entry.Action).Msg("failed to write audit log")
	}
}
