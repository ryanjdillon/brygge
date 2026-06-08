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

// bulkInvoiceRequest drives POST /api/v1/admin/financials/invoices/bulk.
//
// Each selected user gets one invoice with one line per resolved
// selection:
//   - price_item_ids: flat items applied verbatim to every user
//   - beam_categories: each user's slip-boat beam is matched against
//     active price_items in that category to pick the right tier; users
//     with no matching tier are skipped *for that line*, not the whole
//     invoice.
//
// Per-line idempotency: if a non-voided invoice for the same user +
// category + fiscal_period already exists, that line is dropped. If
// every requested line is dropped this way, the user is fully skipped.
type bulkInvoiceRequest struct {
	UserIDs        []string `json:"user_ids"`
	FiscalPeriodID string   `json:"fiscal_period_id"`
	PriceItemIDs   []string `json:"price_item_ids,omitempty"`
	BeamCategories []string `json:"beam_categories,omitempty"`
	DueDate        string   `json:"due_date"`
	// AllowDuplicateLines bypasses the per-line idempotency check. Off
	// by default. The check protects against accidentally re-billing
	// the same yearly item to a member who already has it on a sent
	// (or draft) invoice this fiscal period. Set to true only when the
	// operator has explicitly confirmed a re-bill in the UI.
	AllowDuplicateLines bool `json:"allow_duplicate_lines,omitempty"`
}

type bulkInvoiceCreated struct {
	UserID        string   `json:"user_id"`
	InvoiceID     string   `json:"invoice_id"`
	InvoiceNumber int      `json:"invoice_number"`
	Amount        float64  `json:"amount"`
	LineCount     int      `json:"line_count"`
	DroppedLines  []string `json:"dropped_lines,omitempty"`
}

type bulkInvoiceSkipped struct {
	UserID string `json:"user_id"`
	Reason string `json:"reason"`
}

type bulkInvoiceResponse struct {
	Created []bulkInvoiceCreated `json:"created"`
	Skipped []bulkInvoiceSkipped `json:"skipped"`
}

// bulkResolvedLine binds a chosen price tier to optional slip context.
// For flat items slipInfo stays nil; for beam_tier lines slipInfo
// carries the boat/slip detail used in the rendered sub-description.
type bulkResolvedLine struct {
	tier     priceTier
	slipInfo *slipBoatInfo
}

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
	if len(req.PriceItemIDs) == 0 && len(req.BeamCategories) == 0 {
		Error(w, http.StatusBadRequest, "at least one price_item_id or beam_category is required")
		return
	}
	dueDate, err := time.Parse("2006-01-02", req.DueDate)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid due_date format, use YYYY-MM-DD")
		return
	}

	var fiscalYear int
	if err := h.db.QueryRow(ctx,
		`SELECT year FROM fiscal_periods WHERE id = $1 AND club_id = $2`,
		req.FiscalPeriodID, claims.ClubID,
	).Scan(&fiscalYear); err != nil {
		Error(w, http.StatusBadRequest, "fiscal period not found for this club")
		return
	}

	flatItems := make([]priceTier, 0, len(req.PriceItemIDs))
	for _, id := range req.PriceItemIDs {
		var t priceTier
		var meta json.RawMessage
		var pricingKind string
		var showInBatch bool
		if err := h.db.QueryRow(ctx,
			`SELECT id, category, amount, description, metadata, pricing_kind, show_in_batch
			   FROM price_items
			  WHERE id = $1 AND club_id = $2 AND is_active`,
			id, claims.ClubID,
		).Scan(&t.id, &t.category, &t.amount, &t.desc, &meta, &pricingKind, &showInBatch); err != nil {
			Error(w, http.StatusBadRequest, fmt.Sprintf("price item %s not found or inactive", id))
			return
		}
		if !showInBatch {
			Error(w, http.StatusBadRequest, fmt.Sprintf("price item %q is not enabled for batch fakturas", t.desc))
			return
		}
		if pricingKind == "tiered" {
			Error(w, http.StatusBadRequest, fmt.Sprintf("price item %q is tiered — pick the category instead", t.desc))
			return
		}
		flatItems = append(flatItems, t)
	}

	tierDimensionByCategory := make(map[string]string, len(req.BeamCategories))
	tiersByCategory := make(map[string][]priceTier, len(req.BeamCategories))
	for _, cat := range req.BeamCategories {
		rows, err := h.db.Query(ctx,
			`SELECT id, category, amount, description, metadata, tier_dimension
			   FROM price_items
			  WHERE club_id = $1 AND category = $2 AND is_active
			    AND pricing_kind = 'tiered' AND show_in_batch = TRUE`,
			claims.ClubID, cat,
		)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to load price tiers")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		var list []priceTier
		var dimension string
		for rows.Next() {
			var t priceTier
			var meta json.RawMessage
			var dim *string
			if err := rows.Scan(&t.id, &t.category, &t.amount, &t.desc, &meta, &dim); err != nil {
				rows.Close()
				h.log.Error().Err(err).Msg("failed to scan price tier")
				Error(w, http.StatusInternalServerError, "internal error")
				return
			}
			var m struct {
				BeamMin   *float64 `json:"beam_min"`
				BeamMax   *float64 `json:"beam_max"`
				LengthMin *float64 `json:"length_min"`
				LengthMax *float64 `json:"length_max"`
			}
			_ = json.Unmarshal(meta, &m)
			if dim != nil && *dim == "length" {
				t.tierMin = m.LengthMin
				t.tierMax = m.LengthMax
				dimension = "length"
			} else {
				t.tierMin = m.BeamMin
				t.tierMax = m.BeamMax
				dimension = "beam"
			}
			list = append(list, t)
		}
		rows.Close()
		if len(list) == 0 {
			Error(w, http.StatusBadRequest, fmt.Sprintf("no active tiered items in category %q for batch", cat))
			return
		}
		tierDimensionByCategory[cat] = dimension
		tiersByCategory[cat] = list
	}

	var clubName, orgNumber, clubAddress, bankAccount, website, treasurerEmail, logoMIME string
	var logoData []byte
	if err := h.db.QueryRow(ctx,
		`SELECT name, COALESCE(org_number, ''), COALESCE(address, ''),
		        COALESCE(
		          (SELECT account_number FROM club_bank_accounts
		            WHERE club_id = clubs.id AND is_default_for_invoices AND archived_at IS NULL
		            LIMIT 1),
		          bank_account, ''),
		        COALESCE(website_url, ''), COALESCE(treasurer_email, ''),
		        faktura_logo_data, COALESCE(faktura_logo_mime, '')
		   FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&clubName, &orgNumber, &clubAddress, &bankAccount, &website, &treasurerEmail, &logoData, &logoMIME); err != nil {
		h.log.Error().Err(err).Msg("failed to load club for bulk invoices")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := bulkInvoiceResponse{Created: []bulkInvoiceCreated{}, Skipped: []bulkInvoiceSkipped{}}

	for _, userID := range req.UserIDs {
		userID := userID
		var lines []bulkResolvedLine
		var dropReasons []string

		for i := range flatItems {
			lines = append(lines, bulkResolvedLine{tier: flatItems[i]})
		}

		var slipBoats []*slipBoatInfo
		var slipErr string
		if len(req.BeamCategories) > 0 {
			slipBoats, slipErr = h.slipBoatsForUser(ctx, claims.ClubID, userID)
		}
		for _, cat := range req.BeamCategories {
			if len(slipBoats) == 0 {
				dropReasons = append(dropReasons, fmt.Sprintf("%s: %s", cat, slipErr))
				continue
			}
			dim := tierDimensionByCategory[cat]
			// Emit one line per (boat × matching tier) pair. Multi-boat
			// slip-holders previously got only the first boat's line.
			for _, sb := range slipBoats {
				var measure *float64
				var measureLabel string
				if dim == "length" {
					measure = sb.length
					measureLabel = "length"
				} else {
					measure = sb.beam
					measureLabel = "beam"
				}
				if measure == nil {
					dropReasons = append(dropReasons, fmt.Sprintf("%s (%s): boat has no %s recorded", cat, sb.slipLbl, measureLabel))
					continue
				}
				tiers := tiersByCategory[cat]
				var chosen *priceTier
				for i := range tiers {
					t := &tiers[i]
					if t.tierMin == nil || t.tierMax == nil {
						continue
					}
					if *measure >= *t.tierMin && *measure < *t.tierMax {
						chosen = t
						break
					}
				}
				if chosen == nil {
					dropReasons = append(dropReasons, fmt.Sprintf("%s (%s): %s %.2fm has no matching tier", cat, sb.slipLbl, measureLabel, *measure))
					continue
				}
				lines = append(lines, bulkResolvedLine{tier: *chosen, slipInfo: sb})
			}
		}

		// Per-line idempotency: drop any line whose category is already
		// on a non-voided invoice line for this user/period. Keyed by
		// invoice_lines.category (denormalized from price_items at
		// insert time, see migration 000043), so a re-bill is blocked
		// even when the price_item_id differs across years/tiers and
		// even when the prior invoice was multi-line — invoices.category
		// is NULL for multi-line rows, so the partial unique index on
		// (club, user, category, period) doesn't fire on its own.
		//
		// Skipped entirely when allow_duplicate_lines=true; the operator
		// has explicitly opted into a re-bill at that point.
		if !req.AllowDuplicateLines {
			kept := make([]bulkResolvedLine, 0, len(lines))
			for _, l := range lines {
				if l.tier.category == "" {
					kept = append(kept, l)
					continue
				}
				// Match dedup to the dimension that distinguishes the line:
				// category-only for non-boat lines (dues, harbor_membership),
				// category + boat_id for boat-bearing lines (slip_fee), so
				// two boats on the same invoice don't collide.
				var existing string
				var err error
				if l.slipInfo != nil && l.slipInfo.boatID != "" {
					err = h.db.QueryRow(ctx,
						`SELECT il.id
						   FROM invoice_lines il
						   JOIN invoices i ON i.id = il.invoice_id
						  WHERE i.club_id = $1 AND i.user_id = $2
						    AND il.category = $3
						    AND il.boat_id = $4
						    AND i.fiscal_period_id = $5
						    AND i.status <> 'voided'
						  LIMIT 1`,
						claims.ClubID, userID, l.tier.category, l.slipInfo.boatID, req.FiscalPeriodID,
					).Scan(&existing)
				} else {
					err = h.db.QueryRow(ctx,
						`SELECT il.id
						   FROM invoice_lines il
						   JOIN invoices i ON i.id = il.invoice_id
						  WHERE i.club_id = $1 AND i.user_id = $2
						    AND il.category = $3
						    AND il.boat_id IS NULL
						    AND i.fiscal_period_id = $4
						    AND i.status <> 'voided'
						  LIMIT 1`,
						claims.ClubID, userID, l.tier.category, req.FiscalPeriodID,
					).Scan(&existing)
				}
				if err == nil {
					label := l.tier.desc
					if l.slipInfo != nil && l.slipInfo.slipLbl != "" {
						label = fmt.Sprintf("%s (%s)", l.tier.desc, l.slipInfo.slipLbl)
					}
					dropReasons = append(dropReasons, fmt.Sprintf("%s already invoiced (override available)", label))
					continue
				}
				if err != pgx.ErrNoRows {
					h.log.Error().Err(err).Str("user_id", userID).Str("category", l.tier.category).Msg("idempotency check failed")
					dropReasons = append(dropReasons, fmt.Sprintf("%s: idempotency check error", l.tier.desc))
					continue
				}
				kept = append(kept, l)
			}
			lines = kept
		}

		if len(lines) == 0 {
			reason := "no lines applicable"
			if len(dropReasons) > 0 {
				reason = strings.Join(dropReasons, "; ")
			}
			resp.Skipped = append(resp.Skipped, bulkInvoiceSkipped{UserID: userID, Reason: reason})
			continue
		}

		invID, invNum, total, err := h.createBulkInvoice(ctx,
			claims.ClubID, claims.UserID, userID, lines,
			req.FiscalPeriodID, fiscalYear, dueDate,
			clubName, orgNumber, clubAddress, bankAccount, website, treasurerEmail, logoData, logoMIME)
		if err != nil {
			h.log.Error().Err(err).Str("user_id", userID).Msg("bulk invoice create failed")
			resp.Skipped = append(resp.Skipped, bulkInvoiceSkipped{UserID: userID, Reason: "create failed: " + err.Error()})
			continue
		}
		resp.Created = append(resp.Created, bulkInvoiceCreated{
			UserID: userID, InvoiceID: invID, InvoiceNumber: invNum,
			Amount: total, LineCount: len(lines),
			DroppedLines: dropReasons,
		})
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreated, "invoice", "bulk",
			map[string]any{
				"created_count":    len(resp.Created),
				"skipped_count":    len(resp.Skipped),
				"fiscal_period_id": req.FiscalPeriodID,
				"price_item_ids":   req.PriceItemIDs,
				"beam_categories":  req.BeamCategories,
			})
	}

	JSON(w, http.StatusOK, resp)
}

type slipBoatInfo struct {
	boatID   string
	beam     *float64
	length   *float64
	boatName string
	mfg      string
	model    string
	slipLbl  string
}

// slipBoatsForUser returns every active slip assignment a user owns
// (one entry per (slip, boat) pair). Multi-boat slip-holders previously
// lost lines because the prior helper used LIMIT 1.
func (h *InvoiceHandler) slipBoatsForUser(ctx context.Context, clubID, userID string) ([]*slipBoatInfo, string) {
	rows, err := h.db.Query(ctx,
		`SELECT sa.boat_id, b.beam_m, b.length_m, b.name, b.manufacturer, b.model,
		        s.section, s.number
		   FROM slip_assignments sa
		   LEFT JOIN boats b ON b.id = sa.boat_id
		   JOIN slips s ON s.id = sa.slip_id
		  WHERE sa.user_id = $1 AND sa.club_id = $2
		    AND sa.released_at IS NULL
		  ORDER BY sa.assigned_at`,
		userID, clubID,
	)
	if err != nil {
		return nil, "lookup failed"
	}
	defer rows.Close()
	var out []*slipBoatInfo
	for rows.Next() {
		var (
			boatID      *string
			beam        *float64
			length      *float64
			boatName    *string
			mfg         *string
			model       *string
			slipSection *string
			slipNumber  *string
		)
		if err := rows.Scan(&boatID, &beam, &length, &boatName, &mfg, &model, &slipSection, &slipNumber); err != nil {
			return nil, "scan failed"
		}
		if boatID == nil {
			continue
		}
		info := &slipBoatInfo{boatID: *boatID, beam: beam, length: length}
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
			if len(*slipNumber) > 0 && len(*slipSection) > 0 && string((*slipNumber)[0]) != *slipSection {
				info.slipLbl = *slipSection + *slipNumber
			} else {
				info.slipLbl = *slipNumber
			}
		}
		out = append(out, info)
	}
	if len(out) == 0 {
		return nil, "no active slip assignment"
	}
	return out, ""
}

// priceTier is a single tier slab loaded for the bulk flow. tierMin
// and tierMax are taken from metadata.beam_* or metadata.length_*
// depending on the tier_dimension on the parent item — this struct
// stores the abstract range so the matching loop doesn't need to
// re-decide which dimension to use.
type priceTier struct {
	id       string
	category string
	amount   float64
	desc     string
	tierMin  *float64
	tierMax  *float64
}

func (h *InvoiceHandler) createBulkInvoice(
	ctx context.Context,
	clubID, actorID, userID string,
	lines []bulkResolvedLine,
	fiscalPeriodID string,
	fiscalYear int,
	dueDate time.Time,
	clubName, orgNumber, clubAddress, bankAccount, website, treasurerEmail string,
	logoData []byte, logoMIME string,
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

	pdfLines := make([]finance.InvoiceLine, 0, len(lines))
	var total float64
	for _, l := range lines {
		desc := fmt.Sprintf("%s %d", l.tier.desc, fiscalYear)
		subDesc := ""
		if l.slipInfo != nil {
			subDesc = describeSlipBoat(l.slipInfo)
		}
		pdfLines = append(pdfLines, finance.InvoiceLine{
			Description: desc, SubDescription: subDesc, Quantity: 1, UnitPrice: l.tier.amount,
		})
		total += l.tier.amount
	}

	inv := finance.Invoice{
		ClubName:       clubName,
		OrgNumber:      orgNumber,
		ClubAddress:    clubAddress,
		Website:        website,
		TreasurerEmail: treasurerEmail,
		LogoData:       logoData,
		LogoMIME:       logoMIME,
		MemberName:     memberName,
		MemberAddress:  memberAddress,
		InvoiceNumber:  invoiceSeq,
		IssueDate:      time.Now(),
		DueDate:        dueDate,
		KID:            kid,
		BankAccount:    bankAccount,
		Lines:          pdfLines,
	}
	pdfData, err := finance.GeneratePDF(inv)
	if err != nil {
		return "", 0, 0, err
	}

	// invoices.category stays NULL when an invoice covers multiple
	// categories so the (user, category, period) unique partial index
	// only constrains single-category invoices. The per-line idempotency
	// check above is what prevents double-billing in the multi-line
	// case.
	var invoiceCategory *string
	var invoicePriceItem *string
	if len(lines) == 1 {
		c := lines[0].tier.category
		invoiceCategory = &c
		id := lines[0].tier.id
		invoicePriceItem = &id
	}

	var invoiceID string
	if err := h.db.QueryRow(ctx,
		`INSERT INTO invoices (club_id, user_id, invoice_number, kid_number, due_date,
		                       total_amount, pdf_data, price_item_id, fiscal_period_id,
		                       category, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'open')
		 RETURNING id`,
		clubID, userID, invoiceSeq, kid, dueDate, total, pdfData, invoicePriceItem, fiscalPeriodID, invoiceCategory,
	).Scan(&invoiceID); err != nil {
		return "", 0, 0, err
	}
	for i, l := range lines {
		var lineCategory *string
		if l.tier.category != "" {
			c := l.tier.category
			lineCategory = &c
		}
		var lineBoatID *string
		if l.slipInfo != nil && l.slipInfo.boatID != "" {
			b := l.slipInfo.boatID
			lineBoatID = &b
		}
		if _, err := h.db.Exec(ctx,
			`INSERT INTO invoice_lines (invoice_id, description, sub_description, quantity,
			                            unit_price, line_total, price_item_id, category, boat_id)
			 VALUES ($1, $2, $3, 1, $4, $4, $5, $6, $7)`,
			invoiceID, pdfLines[i].Description, pdfLines[i].SubDescription, l.tier.amount, l.tier.id, lineCategory, lineBoatID,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to insert invoice line for bulk invoice")
		}
	}
	_ = memberEmail
	_ = actorID
	return invoiceID, invoiceSeq, total, nil
}

// describeSlipBoat builds the sub-line "Plass A12, Contrast 33, bredde
// 3.40 m" describing the boat/slip context behind a tiered line. Used
// by both the bulk flow and the single-faktura tier resolution so PDF
// output is identical regardless of entry point.
func describeSlipBoat(info *slipBoatInfo) string {
	boatLabel := info.boatName
	if boatLabel == "" {
		boatLabel = strings.TrimSpace(info.mfg + " " + info.model)
	}
	extras := []string{}
	if info.slipLbl != "" {
		extras = append(extras, "Plass "+info.slipLbl)
	}
	if boatLabel != "" {
		extras = append(extras, boatLabel)
	}
	if info.beam != nil {
		extras = append(extras, fmt.Sprintf("bredde %.2f m", *info.beam))
	}
	if info.length != nil {
		extras = append(extras, fmt.Sprintf("lengde %.2f m", *info.length))
	}
	return strings.Join(extras, ", ")
}
