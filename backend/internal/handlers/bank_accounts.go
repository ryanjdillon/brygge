package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

type BankAccountsHandler struct {
	db  *pgxpool.Pool
	log zerolog.Logger
}

func NewBankAccountsHandler(db *pgxpool.Pool, log zerolog.Logger) *BankAccountsHandler {
	return &BankAccountsHandler{
		db:  db,
		log: log.With().Str("handler", "bank_accounts").Logger(),
	}
}

type bankAccount struct {
	ID                    string    `json:"id"`
	AccountNumber         string    `json:"account_number"`
	Role                  string    `json:"role"`
	GLCode                string    `json:"gl_code"`
	Label                 string    `json:"label"`
	IsDefaultForInvoices  bool      `json:"is_default_for_invoices"`
	CreatedAt             time.Time `json:"created_at"`
}

var validRoles = map[string]struct{}{
	"drift":    {},
	"hoyrente": {},
	"other":    {},
}

// HandleList returns every live (non-archived) bank account for the
// caller's club, drift accounts first, then by created_at.
func (h *BankAccountsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rows, err := h.db.Query(ctx,
		`SELECT id, account_number, role, gl_code, COALESCE(label, ''),
		        is_default_for_invoices, created_at
		   FROM club_bank_accounts
		  WHERE club_id = $1 AND archived_at IS NULL
		  ORDER BY (role = 'drift') DESC, created_at`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("list bank accounts")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	out := make([]bankAccount, 0)
	for rows.Next() {
		var a bankAccount
		if err := rows.Scan(&a.ID, &a.AccountNumber, &a.Role, &a.GLCode, &a.Label,
			&a.IsDefaultForInvoices, &a.CreatedAt); err != nil {
			h.log.Error().Err(err).Msg("scan bank account")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, a)
	}
	JSON(w, http.StatusOK, out)
}

type bankAccountWrite struct {
	AccountNumber        string `json:"account_number"`
	Role                 string `json:"role"`
	GLCode               string `json:"gl_code"`
	Label                string `json:"label"`
	IsDefaultForInvoices bool   `json:"is_default_for_invoices"`
}

// normalizeAccountNumber strips spaces and dots, leaving 11 digits in
// the standard Norwegian kontonummer format.
func normalizeAccountNumber(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// formatAccountNumber renders an 11-digit Norwegian kontonummer as
// xxxx.xx.xxxxx, leaving anything else untouched (so foreign-format
// numbers are stored as the operator entered them).
func formatAccountNumber(s string) string {
	if len(s) != 11 {
		return s
	}
	return s[0:4] + "." + s[4:6] + "." + s[6:11]
}

func validateBankAccountWrite(req bankAccountWrite) (bankAccountWrite, error) {
	if _, ok := validRoles[req.Role]; !ok {
		return req, errors.New("role must be one of drift, hoyrente, other")
	}
	digits := normalizeAccountNumber(req.AccountNumber)
	if digits == "" {
		return req, errors.New("account_number is required")
	}
	req.AccountNumber = formatAccountNumber(digits)
	if req.GLCode == "" {
		req.GLCode = "1920"
	}
	req.Label = strings.TrimSpace(req.Label)
	return req, nil
}

// HandleCreate adds a new bank account. If is_default_for_invoices is
// true, any existing default is unset in the same transaction so the
// partial unique index holds.
func (h *BankAccountsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req bankAccountWrite
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req, err := validateBankAccountWrite(req)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	tx, err := h.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		h.log.Error().Err(err).Msg("begin tx")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)
	if req.IsDefaultForInvoices {
		if _, err := tx.Exec(ctx,
			`UPDATE club_bank_accounts SET is_default_for_invoices = FALSE
			  WHERE club_id = $1 AND is_default_for_invoices AND archived_at IS NULL`,
			claims.ClubID,
		); err != nil {
			h.log.Error().Err(err).Msg("clear previous default")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO club_bank_accounts
		   (club_id, account_number, role, gl_code, label, is_default_for_invoices)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)
		 RETURNING id`,
		claims.ClubID, req.AccountNumber, req.Role, req.GLCode, req.Label,
		req.IsDefaultForInvoices,
	).Scan(&id); err != nil {
		h.log.Error().Err(err).Msg("insert bank account")
		Error(w, http.StatusBadRequest, "account number must be unique within the club")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("commit")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "create_bank_account", "club_bank_accounts", id, nil, req); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit create bank account")
	}
	JSON(w, http.StatusCreated, map[string]string{"id": id})
}

// HandleUpdate edits a single account. is_default_for_invoices=true
// atomically promotes this row and demotes any other default.
func (h *BankAccountsHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id := chi.URLParam(r, "accountID")
	if id == "" {
		Error(w, http.StatusBadRequest, "accountID required")
		return
	}
	var req bankAccountWrite
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req, err := validateBankAccountWrite(req)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	tx, err := h.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		h.log.Error().Err(err).Msg("begin tx")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)
	if req.IsDefaultForInvoices {
		if _, err := tx.Exec(ctx,
			`UPDATE club_bank_accounts SET is_default_for_invoices = FALSE
			  WHERE club_id = $1 AND is_default_for_invoices AND archived_at IS NULL AND id <> $2`,
			claims.ClubID, id,
		); err != nil {
			h.log.Error().Err(err).Msg("clear previous default")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	tag, err := tx.Exec(ctx,
		`UPDATE club_bank_accounts
		    SET account_number          = $3,
		        role                    = $4,
		        gl_code                 = $5,
		        label                   = NULLIF($6, ''),
		        is_default_for_invoices = $7
		  WHERE id = $1 AND club_id = $2 AND archived_at IS NULL`,
		id, claims.ClubID, req.AccountNumber, req.Role, req.GLCode, req.Label,
		req.IsDefaultForInvoices,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("update bank account")
		Error(w, http.StatusBadRequest, "account number must be unique within the club")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "bank account not found")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("commit")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "update_bank_account", "club_bank_accounts", id, nil, req); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit update bank account")
	}
	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// HandleArchive soft-deletes an account. Refuses to archive the
// faktura default — operator must promote another row first.
func (h *BankAccountsHandler) HandleArchive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id := chi.URLParam(r, "accountID")
	if id == "" {
		Error(w, http.StatusBadRequest, "accountID required")
		return
	}
	var isDefault bool
	if err := h.db.QueryRow(ctx,
		`SELECT is_default_for_invoices FROM club_bank_accounts
		  WHERE id = $1 AND club_id = $2 AND archived_at IS NULL`,
		id, claims.ClubID,
	).Scan(&isDefault); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			Error(w, http.StatusNotFound, "bank account not found")
			return
		}
		h.log.Error().Err(err).Msg("load bank account")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if isDefault {
		Error(w, http.StatusBadRequest, "cannot archive the faktura-default account; promote another first")
		return
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE club_bank_accounts SET archived_at = now()
		  WHERE id = $1 AND club_id = $2`,
		id, claims.ClubID,
	); err != nil {
		h.log.Error().Err(err).Msg("archive bank account")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "archive_bank_account", "club_bank_accounts", id, nil, nil); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit archive bank account")
	}
	JSON(w, http.StatusOK, map[string]string{"status": "archived"})
}
