package audit

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Action constants for audit events.
const (
	// Auth events
	ActionLoginSuccess = "auth.login_success"
	ActionLoginFailed  = "auth.login_failed"
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
	ActionInvoiceCreated              = "invoice.created"
	ActionInvoiceVoided               = "invoice.voided"
	ActionInvoicePDFGenerated         = "invoice.pdf_generated"
	ActionInvoiceEmailed              = "invoice.emailed"
	ActionInvoiceReminded             = "invoice.reminded"
	ActionInvoiceVippsLinkSet         = "invoice.vipps_link_set"
	ActionInvoiceCreditLinked         = "invoice.credit_linked"
	ActionInvoiceCreditUnlinked       = "invoice.credit_unlinked"
	ActionInvoiceRegenerated          = "invoice.pdf_regenerated"
	ActionInvoiceDeliveryLogViewed    = "invoice.delivery_log_viewed"
	ActionFinanceCSVExported          = "finance.csv_exported"
	ActionAccountingInvoicesResplit   = "accounting.invoices_resplit"
	ActionAccountingVippsDraftsPosted = "accounting.vipps_drafts_posted"
	ActionAccountingDraftDeleted      = "accounting.draft_deleted"
	ActionPaymentCreated              = "payment.created"
	ActionPaymentViewed               = "finance.payment_viewed"

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
