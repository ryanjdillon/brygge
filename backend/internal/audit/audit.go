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
	ActionLoginSuccess    = "auth.login_success"
	ActionLoginFailed     = "auth.login_failed"
	ActionTokenRevoked    = "auth.token_revoked"
	ActionInvalidToken    = "auth.invalid_token"

	// Admin user operations
	ActionUserRoleUpdated = "user.role_updated"
	ActionUserDeleted     = "user.deleted"

	// Admin slip operations
	ActionSlipCreated  = "slip.created"
	ActionSlipAssigned = "slip.assigned"
	ActionSlipReleased = "slip.released"

	// Content operations
	ActionEventCreated    = "event.created"
	ActionEventDeleted    = "event.deleted"
	ActionPricingUpdated  = "pricing.updated"
	ActionBroadcastSent   = "broadcast.sent"
	ActionDocumentUploaded = "document.uploaded"
	ActionDocumentDeleted  = "document.deleted"

	// Booking operations
	ActionBookingConfirmed = "booking.confirmed"

	// GDPR operations
	ActionGDPRExportRequested    = "gdpr.export_requested"
	ActionGDPRDeletionRequested  = "gdpr.deletion_requested"
	ActionGDPRDeletionCancelled  = "gdpr.deletion_cancelled"
	ActionGDPRDeletionProcessed  = "gdpr.deletion_processed"
	ActionLegalDocumentCreated   = "legal_document.created"
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
