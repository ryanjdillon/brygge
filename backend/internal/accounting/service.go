package accounting

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
)

// Service provides accounting operations for a club.
type Service struct {
	db    *pgxpool.Pool
	audit *audit.Service
	log   zerolog.Logger
	// stripeSync serialises the Stripe balance ingest per club across the
	// nightly job, the sync endpoint and the payout webhook.
	stripeSync stripeSyncGuard
	// kickDeliveries nudges the delivery worker after the booking pass
	// queued a refund document (SetDeliveryKick); nil leaves the rows for
	// the worker's next tick.
	kickDeliveries func()
}

// SetDeliveryKick wires the delivery worker's kick, so a refund's credit
// note is rendered and sent as soon as its booking commits.
func (s *Service) SetDeliveryKick(kick func()) {
	s.kickDeliveries = kick
}

// NewService creates an accounting service.
func NewService(db *pgxpool.Pool, auditService *audit.Service, log zerolog.Logger) *Service {
	return &Service{
		db:    db,
		audit: auditService,
		log:   log.With().Str("component", "accounting").Logger(),
	}
}

// DB returns the database pool for direct queries from handlers.
func (s *Service) DB() *pgxpool.Pool {
	return s.db
}

// Account represents a row in the accounts table.
type Account struct {
	ID          string         `json:"id"`
	ClubID      string         `json:"club_id"`
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	AccountType AccountType    `json:"account_type"`
	ParentCode  string         `json:"parent_code"`
	IsSystem    bool           `json:"is_system"`
	IsActive    bool           `json:"is_active"`
	MVAEligible MVAEligibility `json:"mva_eligible"`
	Description string         `json:"description"`
	SortOrder   int            `json:"sort_order"`
}

// SeedKontoplan inserts the default chart of accounts for a club.
// Skips accounts that already exist (idempotent).
func (s *Service) SeedKontoplan(ctx context.Context, clubID string) (int, error) {
	defaults := DefaultKontoplan()
	seeded := 0

	for _, def := range defaults {
		tag, err := s.db.Exec(ctx,
			`INSERT INTO accounts (club_id, code, name, account_type, parent_code, is_system, mva_eligible, description, sort_order)
			 VALUES ($1, $2, $3, $4, $5, true, $6, $7, $8)
			 ON CONFLICT (club_id, code) DO NOTHING`,
			clubID, def.Code, def.Name, def.Type, def.ParentCode, def.MVAEligible, def.Description, def.SortOrder,
		)
		if err != nil {
			return seeded, fmt.Errorf("seeding account %s: %w", def.Code, err)
		}
		if tag.RowsAffected() > 0 {
			seeded++
		}
	}

	return seeded, nil
}

// ListAccounts returns all active accounts for a club, ordered by sort_order.
func (s *Service) ListAccounts(ctx context.Context, clubID string) ([]Account, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, club_id, code, name, account_type, COALESCE(parent_code, ''), is_system, is_active, mva_eligible, description, sort_order
		 FROM accounts
		 WHERE club_id = $1 AND is_active = true
		 ORDER BY sort_order, code`,
		clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing accounts: %w", err)
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.ClubID, &a.Code, &a.Name, &a.AccountType, &a.ParentCode, &a.IsSystem, &a.IsActive, &a.MVAEligible, &a.Description, &a.SortOrder); err != nil {
			return nil, fmt.Errorf("scanning account: %w", err)
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// CreateAccount adds a custom account to the club's kontoplan.
func (s *Service) CreateAccount(ctx context.Context, clubID string, a AccountDef) (string, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO accounts (club_id, code, name, account_type, parent_code, is_system, mva_eligible, description, sort_order)
		 VALUES ($1, $2, $3, $4, $5, false, $6, $7, $8)
		 RETURNING id`,
		clubID, a.Code, a.Name, a.Type, a.ParentCode, a.MVAEligible, a.Description, a.SortOrder,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("creating account: %w", err)
	}
	return id, nil
}

// ErrAccountNotFound is returned for an account id that doesn't exist or
// belongs to another club.
var ErrAccountNotFound = errors.New("account not found")

// ErrSystemAccountMVALocked refuses a change to a system account's MVA
// eligibility: the seeded value is what momskompensasjon relies on.
var ErrSystemAccountMVALocked = errors.New("cannot change MVA eligibility of a system account")

// UpdateAccount changes an account's name, description and MVA eligibility,
// and returns the account as it was before. A system account takes a new
// name and description only (posting finds accounts by code, so a club may
// call 3300 whatever its board reports it as); its MVA eligibility stays,
// so mvaEligible must be empty or the stored value. An empty mvaEligible
// keeps the stored value on any account.
func (s *Service) UpdateAccount(ctx context.Context, clubID, accountID string, name, description string, mvaEligible MVAEligibility) (*Account, error) {
	prev, err := s.GetAccount(ctx, clubID, accountID)
	if err != nil {
		return nil, fmt.Errorf("checking account: %w", err)
	}
	if prev == nil {
		return nil, ErrAccountNotFound
	}
	if mvaEligible == "" {
		mvaEligible = prev.MVAEligible
	}
	if prev.IsSystem && mvaEligible != prev.MVAEligible {
		return nil, ErrSystemAccountMVALocked
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE accounts SET name = $3, description = $4, mva_eligible = $5, updated_at = now()
		 WHERE id = $1 AND club_id = $2`,
		accountID, clubID, name, description, mvaEligible,
	); err != nil {
		return nil, fmt.Errorf("updating account: %w", err)
	}
	return prev, nil
}

// DeactivateAccount soft-deletes an account if it has no journal lines.
func (s *Service) DeactivateAccount(ctx context.Context, clubID, accountID string) error {
	// Check for system account
	var isSystem bool
	err := s.db.QueryRow(ctx,
		`SELECT is_system FROM accounts WHERE id = $1 AND club_id = $2`,
		accountID, clubID,
	).Scan(&isSystem)
	if err == pgx.ErrNoRows {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("checking account: %w", err)
	}
	if isSystem {
		return fmt.Errorf("cannot deactivate system account")
	}

	// Check for existing journal lines
	var hasLines bool
	err = s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM journal_lines WHERE account_id = $1)`,
		accountID,
	).Scan(&hasLines)
	if err != nil {
		return fmt.Errorf("checking journal lines: %w", err)
	}
	if hasLines {
		return fmt.Errorf("cannot deactivate account with existing journal entries")
	}

	_, err = s.db.Exec(ctx,
		`UPDATE accounts SET is_active = false, updated_at = now() WHERE id = $1 AND club_id = $2`,
		accountID, clubID,
	)
	return err
}

// GetAccount returns a single account by ID, scoped to the club.
func (s *Service) GetAccount(ctx context.Context, clubID, accountID string) (*Account, error) {
	var a Account
	err := s.db.QueryRow(ctx,
		`SELECT id, club_id, code, name, account_type, COALESCE(parent_code, ''), is_system, is_active, mva_eligible, description, sort_order
		 FROM accounts WHERE id = $1 AND club_id = $2`,
		accountID, clubID,
	).Scan(&a.ID, &a.ClubID, &a.Code, &a.Name, &a.AccountType, &a.ParentCode, &a.IsSystem, &a.IsActive, &a.MVAEligible, &a.Description, &a.SortOrder)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting account: %w", err)
	}
	return &a, nil
}
