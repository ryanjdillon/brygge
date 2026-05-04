package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// bulkInvoiceRequest drives POST /api/v1/admin/invoices/bulk.
//
// Mode "flat" — every selected user gets an invoice for the same
// price_item_id with that item's amount.
//
// Mode "beam_tier" — for every selected user, look at the boat
// occupying their active permanent slip assignment and select the
// price_item from `category` whose metadata.beam_min ≤ boat.beam_m <
// metadata.beam_max. Users with no active slip, no boat on their
// active slip, or no matching tier are skipped (returned in
// `skipped` so the admin sees what to fix).
type bulkInvoiceRequest struct {
	UserIDs        []string `json:"user_ids"`
	FiscalPeriodID string   `json:"fiscal_period_id"`
	Mode           string   `json:"mode"`
	PriceItemID    string   `json:"price_item_id,omitempty"`
	Category       string   `json:"category,omitempty"`
	DueDate        string   `json:"due_date"`
}

type bulkInvoiceCreated struct {
	UserID        string  `json:"user_id"`
	InvoiceID     string  `json:"invoice_id"`
	InvoiceNumber int     `json:"invoice_number"`
	Amount        float64 `json:"amount"`
	PriceItemID   string  `json:"price_item_id"`
}

type bulkInvoiceSkipped struct {
	UserID string `json:"user_id"`
	Reason string `json:"reason"`
}

type bulkInvoiceResponse struct {
	Created []bulkInvoiceCreated `json:"created"`
	Skipped []bulkInvoiceSkipped `json:"skipped"`
}

// HandleBulkCreateInvoices generates draft invoices (sent_at NULL) for
// every selected user. Idempotent on (price_item_id, fiscal_period_id):
// re-running with the same selection skips users who already have an
// invoice for the same line.
func (h *InvoiceHandler) HandleBulkCreateInvoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req bulkInvoiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.UserIDs) == 0 {
		Error(w, http.StatusBadRequest, "user_ids is required")
		return
	}
	if req.FiscalPeriodID == "" {
		Error(w, http.StatusBadRequest, "fiscal_period_id is required")
		return
	}
	if req.Mode != "flat" && req.Mode != "beam_tier" {
		Error(w, http.StatusBadRequest, "mode must be 'flat' or 'beam_tier'")
		return
	}
	if req.Mode == "flat" && req.PriceItemID == "" {
		Error(w, http.StatusBadRequest, "price_item_id is required for flat mode")
		return
	}
	if req.Mode == "beam_tier" && req.Category == "" {
		Error(w, http.StatusBadRequest, "category is required for beam_tier mode")
		return
	}
	dueDate, err := time.Parse("2006-01-02", req.DueDate)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid due_date format, use YYYY-MM-DD")
		return
	}

	// Fiscal period must belong to this club. Use end_date in the
	// invoice description so members see the year clearly.
	var fiscalYear int
	if err := h.db.QueryRow(ctx,
		`SELECT year FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		req.FiscalPeriodID, claims.ClubID,
	).Scan(&fiscalYear); err != nil {
		Error(w, http.StatusBadRequest, "fiscal period not found for this club")
		return
	}

	// Load all candidate price items up-front. For flat mode, exactly
	// one row by id; for beam_tier mode, every active item in the
	// category so we can match per user without an N+1 lookup.
	var flatItem *priceTier
	tiers := make([]priceTier, 0)
	if req.Mode == "flat" {
		var t priceTier
		var meta json.RawMessage
		if err := h.db.QueryRow(ctx,
			`SELECT id, category, amount, description, metadata
			   FROM price_items
			  WHERE id = $1 AND club_id = $2 AND is_active`,
			req.PriceItemID, claims.ClubID,
		).Scan(&t.id, &t.category, &t.amount, &t.desc, &meta); err != nil {
			Error(w, http.StatusBadRequest, "price item not found or inactive")
			return
		}
		flatItem = &t
	} else {
		rows, err := h.db.Query(ctx,
			`SELECT id, category, amount, description, metadata
			   FROM price_items
			  WHERE club_id = $1 AND category = $2 AND is_active`,
			claims.ClubID, req.Category,
		)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to load price tiers")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var t priceTier
			var meta json.RawMessage
			if err := rows.Scan(&t.id, &t.category, &t.amount, &t.desc, &meta); err != nil {
				h.log.Error().Err(err).Msg("failed to scan price tier")
				Error(w, http.StatusInternalServerError, "internal error")
				return
			}
			var m struct {
				BeamMin *float64 `json:"beam_min"`
				BeamMax *float64 `json:"beam_max"`
			}
			_ = json.Unmarshal(meta, &m)
			t.beamMin = m.BeamMin
			t.beamMax = m.BeamMax
			tiers = append(tiers, t)
		}
		if len(tiers) == 0 {
			Error(w, http.StatusBadRequest, fmt.Sprintf("no active price items in category %q", req.Category))
			return
		}
	}

	// Club details for PDF rendering.
	var clubName, orgNumber, clubAddress, bankAccount string
	if err := h.db.QueryRow(ctx,
		`SELECT name, COALESCE(org_number, ''), COALESCE(address, ''), COALESCE(bank_account, '')
		   FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&clubName, &orgNumber, &clubAddress, &bankAccount); err != nil {
		h.log.Error().Err(err).Msg("failed to load club for bulk invoices")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := bulkInvoiceResponse{Created: []bulkInvoiceCreated{}, Skipped: []bulkInvoiceSkipped{}}
	for _, userID := range req.UserIDs {
		userID := userID
		// Resolve which price_item applies to this user, plus boat/slip
		// context to enrich the line description.
		var chosen *priceTier
		var slipInfo *slipBoatInfo
		var skipReason string
		if flatItem != nil {
			chosen = flatItem
		} else {
			info, lookupErr := h.slipBoatForUser(ctx, claims.ClubID, userID)
			if lookupErr != "" {
				skipReason = lookupErr
			} else {
				slipInfo = info
				for i := range tiers {
					t := &tiers[i]
					if t.beamMin == nil || t.beamMax == nil {
						continue
					}
					if *info.beam >= *t.beamMin && *info.beam < *t.beamMax {
						chosen = t
						break
					}
				}
				if chosen == nil {
					skipReason = fmt.Sprintf("beam %.2fm has no matching tier", *info.beam)
				}
			}
		}
		if chosen == nil {
			resp.Skipped = append(resp.Skipped, bulkInvoiceSkipped{UserID: userID, Reason: skipReason})
			continue
		}

		// Idempotency: skip if a non-voided invoice for this
		// (user, category, fiscal_period) already exists. Voided rows
		// fall out so a re-issue after voiding is permitted.
		var existing string
		err := h.db.QueryRow(ctx,
			`SELECT id FROM invoices
			  WHERE club_id = $1 AND user_id = $2
			    AND category = $3 AND fiscal_period_id = $4
			    AND status <> 'voided'`,
			claims.ClubID, userID, chosen.category, req.FiscalPeriodID,
		).Scan(&existing)
		if err == nil {
			resp.Skipped = append(resp.Skipped, bulkInvoiceSkipped{UserID: userID, Reason: "already invoiced in this category for this period"})
			continue
		}
		if err != pgx.ErrNoRows {
			h.log.Error().Err(err).Str("user_id", userID).Msg("idempotency check failed")
			resp.Skipped = append(resp.Skipped, bulkInvoiceSkipped{UserID: userID, Reason: "internal error"})
			continue
		}

		invID, invNum, amount, err := h.createBulkInvoice(ctx,
			claims.ClubID, claims.UserID, userID, chosen, slipInfo,
			req.FiscalPeriodID, fiscalYear, dueDate,
			clubName, orgNumber, clubAddress, bankAccount)
		if err != nil {
			h.log.Error().Err(err).Str("user_id", userID).Msg("bulk invoice create failed")
			resp.Skipped = append(resp.Skipped, bulkInvoiceSkipped{UserID: userID, Reason: "create failed: " + err.Error()})
			continue
		}
		resp.Created = append(resp.Created, bulkInvoiceCreated{
			UserID: userID, InvoiceID: invID, InvoiceNumber: invNum,
			Amount: amount, PriceItemID: chosen.id,
		})
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreated, "invoice", "bulk",
			map[string]any{
				"created_count":    len(resp.Created),
				"skipped_count":    len(resp.Skipped),
				"fiscal_period_id": req.FiscalPeriodID,
				"mode":             req.Mode,
				"price_item_id":    req.PriceItemID,
				"category":         req.Category,
			})
	}

	JSON(w, http.StatusOK, resp)
}

type slipBoatInfo struct {
	beam     *float64
	boatName string
	mfg      string
	model    string
	slipLbl  string
}

// slipBoatForUser returns the boat + slip on the user's active slip
// assignment, plus a distinguishable human-readable reason when the
// lookup can't yield a usable beam. Distinguishing between "no slip",
// "no boat on slip", and "boat has no beam recorded" matters for the
// bulk-faktura skip list.
func (h *InvoiceHandler) slipBoatForUser(ctx context.Context, clubID, userID string) (*slipBoatInfo, string) {
	var (
		hasAssignment   bool
		boatID          *string
		beam            *float64
		boatName        *string
		mfg             *string
		model           *string
		slipSection     *string
		slipNumber      *string
	)
	err := h.db.QueryRow(ctx,
		`SELECT TRUE, sa.boat_id, b.beam_m, b.name, b.manufacturer, b.model,
		        s.section, s.number
		   FROM slip_assignments sa
		   LEFT JOIN boats b ON b.id = sa.boat_id
		   JOIN slips s ON s.id = sa.slip_id
		  WHERE sa.user_id = $1 AND sa.club_id = $2
		    AND sa.released_at IS NULL
		  ORDER BY sa.assigned_at
		  LIMIT 1`,
		userID, clubID,
	).Scan(&hasAssignment, &boatID, &beam, &boatName, &mfg, &model, &slipSection, &slipNumber)
	if err == pgx.ErrNoRows {
		return nil, "no active slip assignment"
	}
	if err != nil {
		return nil, "lookup failed"
	}
	if boatID == nil {
		return nil, "active slip has no boat_id set"
	}
	if beam == nil {
		return nil, "boat on slip has no beam recorded — set boat.beam_m"
	}
	info := &slipBoatInfo{beam: beam}
	if boatName != nil {
		info.boatName = *boatName
	}
	if mfg != nil {
		info.mfg = *mfg
	}
	if model != nil {
		info.model = *model
	}
	if slipSection != nil && slipNumber != nil {
		// "A1" if number doesn't already start with section, else just number.
		if len(*slipNumber) > 0 && len(*slipSection) > 0 && string((*slipNumber)[0]) != *slipSection {
			info.slipLbl = *slipSection + *slipNumber
		} else {
			info.slipLbl = *slipNumber
		}
	}
	return info, ""
}

type priceTier struct {
	id       string
	category string
	amount   float64
	desc     string
	beamMin  *float64
	beamMax  *float64
}

func (h *InvoiceHandler) createBulkInvoice(
	ctx context.Context,
	clubID, actorID, userID string,
	t *priceTier,
	slipInfo *slipBoatInfo,
	fiscalPeriodID string,
	fiscalYear int,
	dueDate time.Time,
	clubName, orgNumber, clubAddress, bankAccount string,
) (string, int, float64, error) {
	var memberName, memberEmail, memberAddress string
	if err := h.db.QueryRow(ctx,
		`SELECT full_name, email, COALESCE(address_line || ', ' || postal_code || ' ' || city, '')
		   FROM users WHERE id = $1 AND club_id = $2`,
		userID, clubID,
	).Scan(&memberName, &memberEmail, &memberAddress); err != nil {
		return "", 0, 0, err
	}

	var invoiceSeq int
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(invoice_number), 0) + 1 FROM invoices WHERE club_id = $1`,
		clubID,
	).Scan(&invoiceSeq); err != nil {
		return "", 0, 0, err
	}

	kid := finance.GenerateKID("000", invoiceSeq, 1)
	desc := fmt.Sprintf("%s %d", t.desc, fiscalYear)
	if slipInfo != nil {
		// Append boat name (or mfg+model) and slip label to the line so
		// the recipient can see exactly what they're paying for.
		boatLabel := slipInfo.boatName
		if boatLabel == "" {
			boatLabel = strings.TrimSpace(slipInfo.mfg + " " + slipInfo.model)
		}
		extras := []string{}
		if slipInfo.slipLbl != "" {
			extras = append(extras, "Plass "+slipInfo.slipLbl)
		}
		if boatLabel != "" {
			extras = append(extras, boatLabel)
		}
		if slipInfo.beam != nil {
			extras = append(extras, fmt.Sprintf("bredde %.2f m", *slipInfo.beam))
		}
		if len(extras) > 0 {
			desc = desc + " — " + strings.Join(extras, ", ")
		}
	}
	pdfLines := []finance.InvoiceLine{{Description: desc, Quantity: 1, UnitPrice: t.amount}}
	inv := finance.Invoice{
		ClubName:      clubName,
		OrgNumber:     orgNumber,
		ClubAddress:   clubAddress,
		MemberName:    memberName,
		MemberAddress: memberAddress,
		InvoiceNumber: invoiceSeq,
		IssueDate:     time.Now(),
		DueDate:       dueDate,
		KID:           kid,
		BankAccount:   bankAccount,
		Lines:         pdfLines,
	}
	pdfData, err := finance.GeneratePDF(inv)
	if err != nil {
		return "", 0, 0, err
	}

	var invoiceID string
	if err := h.db.QueryRow(ctx,
		`INSERT INTO invoices (club_id, user_id, invoice_number, kid_number, due_date,
		                       total_amount, pdf_data, price_item_id, fiscal_period_id,
		                       category, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'open')
		 RETURNING id`,
		clubID, userID, invoiceSeq, kid, dueDate, t.amount, pdfData, t.id, fiscalPeriodID, t.category,
	).Scan(&invoiceID); err != nil {
		return "", 0, 0, err
	}
	if _, err := h.db.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total)
		 VALUES ($1, $2, 1, $3, $3)`,
		invoiceID, desc, t.amount,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to insert invoice line for bulk invoice")
	}
	return invoiceID, invoiceSeq, t.amount, nil
}
