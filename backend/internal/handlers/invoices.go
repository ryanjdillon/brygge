package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type InvoiceHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	email  email.Sender
	audit  *audit.Service
	log    zerolog.Logger
}

func NewInvoiceHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	emailClient email.Sender,
	auditService *audit.Service,
	log zerolog.Logger,
) *InvoiceHandler {
	return &InvoiceHandler{
		db:     db,
		config: cfg,
		email:  emailClient,
		audit:  auditService,
		log:    log.With().Str("handler", "invoices").Logger(),
	}
}

type createInvoiceFullRequest struct {
	UserID         string             `json:"user_id"`
	DueDate        string             `json:"due_date"`
	FiscalPeriodID *string            `json:"fiscal_period_id,omitempty"`
	Lines          []invoiceLineInput `json:"lines"`
	SendEmail      bool               `json:"send_email"`

	// Optional organisation recipient. When RecipientKind is "organization"
	// the OrgName is required and the rest are optional overrides; the
	// PDF "Til:" block, address, and email destination use these fields
	// instead of the linked User's personal details. The User row is
	// still recorded as the contact owner so the invoice appears in
	// their member portal.
	RecipientKind          string  `json:"recipient_kind,omitempty"`
	RecipientOrgName       *string `json:"recipient_org_name,omitempty"`
	RecipientOrgNumber     *string `json:"recipient_org_number,omitempty"`
	RecipientOrgAddress    *string `json:"recipient_org_address,omitempty"`
	RecipientContactPerson *string `json:"recipient_contact_person,omitempty"`
	RecipientTheirRef      *string `json:"recipient_their_ref,omitempty"`
	RecipientEmail         *string `json:"recipient_email,omitempty"`
}

type invoiceLineInput struct {
	Description    string  `json:"description"`
	SubDescription string  `json:"sub_description,omitempty"`
	Quantity       int     `json:"quantity"`
	UnitPrice      float64 `json:"unit_price"`
	// AccountID is required for custom lines (lines without a price_item)
	// so journal postings can attribute revenue correctly. Optional for
	// price-item lines because the price_item carries its own account.
	AccountID   *string `json:"account_id,omitempty"`
	PriceItemID *string `json:"price_item_id,omitempty"`
	// TierCategory selects a tiered price set by category — the server
	// then looks up the matching tier slab for the chosen boat. Mutually
	// exclusive with PriceItemID; one must be set for non-custom lines.
	TierCategory *string `json:"tier_category,omitempty"`
	// BoatID is required for tiered lines (drives tier resolution) and
	// for items with requires_boat_selection=true.
	BoatID *string `json:"boat_id,omitempty"`
}

type invoiceResponse struct {
	ID            string    `json:"id"`
	InvoiceNumber int       `json:"invoice_number"`
	KID           string    `json:"kid_number"`
	UserID        string    `json:"user_id"`
	TotalAmount   float64   `json:"total_amount"`
	IssueDate     string    `json:"issue_date"`
	DueDate       string    `json:"due_date"`
	SentAt        *string   `json:"sent_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// HandleCreateInvoice generates a full invoice with KID, PDF, and optional email delivery.
func (h *InvoiceHandler) HandleCreateInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createInvoiceFullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.DueDate == "" || len(req.Lines) == 0 {
		Error(w, http.StatusBadRequest, "due_date and at least one line item are required")
		return
	}

	dueDate, err := time.Parse("2006-01-02", req.DueDate)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid due_date format, use YYYY-MM-DD")
		return
	}

	// Look up member if a UserID was supplied. Member-less single
	// fakturas are valid (e.g. invoicing an external org with no
	// internal contact) — in that case the recipient name and address
	// have to come from the recipient_* override fields.
	var memberName, memberEmail, memberAddress string
	if req.UserID != "" {
		err = h.db.QueryRow(ctx,
			`SELECT full_name, email, COALESCE(address_line || ', ' || postal_code || ' ' || city, '')
			 FROM users WHERE id = $1 AND club_id = $2`,
			req.UserID, claims.ClubID,
		).Scan(&memberName, &memberEmail, &memberAddress)
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			h.log.Error().Err(err).Msg("failed to look up member for invoice")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// Resolve and validate every line. Custom lines (no price_item_id
	// and no tier_category) must carry an account_id. Tiered lines
	// resolve to a specific price_items row matching the chosen boat's
	// beam/length on the tier_dimension. Lines whose price item flags
	// requires_boat_selection=true must include a boat_id; the server
	// auto-fills sub_description with the boat detail to mirror the
	// bulk flow's PDF output.
	resolvedLines := make([]invoiceLineInput, len(req.Lines))
	var total float64
	pdfLines := make([]finance.InvoiceLine, len(req.Lines))
	for i, line := range req.Lines {
		if line.Quantity < 1 {
			Error(w, http.StatusBadRequest, "quantity must be at least 1")
			return
		}
		if line.PriceItemID != nil && line.TierCategory != nil {
			Error(w, http.StatusBadRequest, "line cannot specify both price_item_id and tier_category")
			return
		}
		isCustom := line.PriceItemID == nil && line.TierCategory == nil
		if isCustom && (line.AccountID == nil || *line.AccountID == "") {
			Error(w, http.StatusBadRequest, "custom line items require an account_id")
			return
		}

		resolved := line // copy

		switch {
		case line.PriceItemID != nil:
			var pricingKind string
			var requiresBoat bool
			if err := h.db.QueryRow(ctx,
				`SELECT pricing_kind, requires_boat_selection
				   FROM price_items WHERE id = $1 AND club_id = $2 AND is_active`,
				*line.PriceItemID, claims.ClubID,
			).Scan(&pricingKind, &requiresBoat); err != nil {
				Error(w, http.StatusBadRequest, "price item not found or inactive")
				return
			}
			if pricingKind == "tiered" {
				Error(w, http.StatusBadRequest, "tiered price items must be selected via tier_category, not price_item_id")
				return
			}
			if requiresBoat && (line.BoatID == nil || *line.BoatID == "") {
				Error(w, http.StatusBadRequest, "this price item requires a boat selection")
				return
			}
			if line.BoatID != nil && *line.BoatID != "" {
				info, err := h.boatInfo(ctx, claims.ClubID, req.UserID, *line.BoatID)
				if err != "" {
					Error(w, http.StatusBadRequest, err)
					return
				}
				if resolved.SubDescription == "" {
					resolved.SubDescription = describeSlipBoat(info)
				}
			}

		case line.TierCategory != nil:
			if line.BoatID == nil || *line.BoatID == "" {
				Error(w, http.StatusBadRequest, "tiered line items require a boat_id")
				return
			}
			info, errMsg := h.boatInfo(ctx, claims.ClubID, req.UserID, *line.BoatID)
			if errMsg != "" {
				Error(w, http.StatusBadRequest, errMsg)
				return
			}
			tier, dim, errMsg := h.resolveTier(ctx, claims.ClubID, *line.TierCategory, info)
			if errMsg != "" {
				Error(w, http.StatusBadRequest, errMsg)
				return
			}
			// Server-side resolution wins over whatever amount the client
			// computed — keeps prices canonical.
			resolved.PriceItemID = &tier.id
			resolved.TierCategory = nil
			resolved.UnitPrice = tier.amount
			if resolved.Description == "" {
				resolved.Description = tier.desc
			}
			if resolved.SubDescription == "" {
				resolved.SubDescription = describeSlipBoat(info)
			}
			_ = dim
		}

		resolvedLines[i] = resolved
		pdfLines[i] = finance.InvoiceLine{
			Description:    resolved.Description,
			SubDescription: resolved.SubDescription,
			Quantity:       resolved.Quantity,
			UnitPrice:      resolved.UnitPrice,
		}
		total += float64(resolved.Quantity) * resolved.UnitPrice
	}

	// Get next invoice sequence for KID
	var invoiceSeq int
	err = h.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(invoice_number), 0) + 1 FROM invoices WHERE club_id = $1`,
		claims.ClubID,
	).Scan(&invoiceSeq)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get invoice sequence")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Get club settings for PDF. bank_account resolution prefers the
	// row flagged is_default_for_invoices in club_bank_accounts; the
	// clubs.bank_account column is the fallback for the deprecation
	// window. See DIL-338/341.
	var clubName, orgNumber, clubAddress, bankAccount, website, treasurerEmail, logoMIME string
	var logoData []byte
	err = h.db.QueryRow(ctx,
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
	).Scan(&clubName, &orgNumber, &clubAddress, &bankAccount, &website, &treasurerEmail, &logoData, &logoMIME)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get club details for invoice")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	kid := finance.GenerateKID("000", invoiceSeq, 1)

	// Resolve recipient overrides. recipient_org_name doubles as the
	// canonical recipient name in *both* private and organisation modes
	// — when private, it's the person's name; when organisation, it's
	// the legal entity's name. recipient_org_address is the analogous
	// address override. The org-only extras (org number, Att:, deres
	// ref.) are only consulted when recipient_kind = 'organization'.
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	overrideName := deref(req.RecipientOrgName)
	overrideAddress := deref(req.RecipientOrgAddress)

	// Pick the buyer name + address used by the PDF for the private
	// path. Override > member name. Without either we can't issue an
	// invoice — the PDF needs *some* "Til:" line.
	pdfMemberName := overrideName
	if pdfMemberName == "" {
		pdfMemberName = memberName
	}
	pdfMemberAddress := overrideAddress
	if pdfMemberAddress == "" {
		pdfMemberAddress = memberAddress
	}
	if pdfMemberName == "" && req.RecipientKind != "organization" {
		Error(w, http.StatusBadRequest, "recipient name is required (select a member or fill the recipient field)")
		return
	}

	var orgRecipient *finance.OrgRecipient
	if req.RecipientKind == "organization" {
		if overrideName == "" {
			Error(w, http.StatusBadRequest, "recipient_org_name is required when recipient_kind=organization")
			return
		}
		contact := deref(req.RecipientContactPerson)
		if contact == "" {
			contact = memberName
		}
		orgRecipient = &finance.OrgRecipient{
			Name:          overrideName,
			OrgNumber:     deref(req.RecipientOrgNumber),
			Address:       overrideAddress,
			ContactPerson: contact,
			TheirRef:      deref(req.RecipientTheirRef),
		}
	}

	// Generate PDF
	inv := finance.Invoice{
		ClubName:      clubName,
		OrgNumber:     orgNumber,
		ClubAddress:    clubAddress,
		Website:        website,
		TreasurerEmail: treasurerEmail,
		LogoData:       logoData,
		LogoMIME:       logoMIME,
		MemberName:     pdfMemberName,
		MemberAddress: pdfMemberAddress,
		OrgRecipient:  orgRecipient,
		InvoiceNumber: invoiceSeq,
		IssueDate:     time.Now(),
		DueDate:       dueDate,
		KID:           kid,
		BankAccount:   bankAccount,
		Lines:         pdfLines,
	}

	pdfData, err := finance.GeneratePDF(inv)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate invoice PDF")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Store invoice. Recipient overrides (org_name, org_number, etc.)
	// are persisted alongside the linked user_id so the contact still
	// sees the invoice in their portal but reissues / PDF regenerations
	// will faithfully reproduce the org-addressed version.
	recipientKind := req.RecipientKind
	if recipientKind == "" {
		recipientKind = "private"
	}
	// user_id is now nullable — pass NULL when not supplied so the FK
	// stays satisfied for member-less invoices.
	var userIDArg any
	if req.UserID != "" {
		userIDArg = req.UserID
	}
	var invoiceID string
	var createdAt time.Time
	err = h.db.QueryRow(ctx,
		`INSERT INTO invoices (club_id, user_id, invoice_number, kid_number, due_date,
		                       total_amount, pdf_data, fiscal_period_id,
		                       recipient_kind, recipient_org_name, recipient_org_number,
		                       recipient_org_address, recipient_contact_person,
		                       recipient_their_ref, recipient_email)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		 RETURNING id, created_at`,
		claims.ClubID, userIDArg, invoiceSeq, kid, dueDate, total, pdfData, req.FiscalPeriodID,
		recipientKind,
		req.RecipientOrgName, req.RecipientOrgNumber,
		req.RecipientOrgAddress, req.RecipientContactPerson,
		req.RecipientTheirRef, req.RecipientEmail,
	).Scan(&invoiceID, &createdAt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to store invoice")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Store line items, including account_id and price_item_id so the
	// journal posting can attribute revenue and the dedup index can
	// see which price item drove the line.
	for _, line := range resolvedLines {
		lineTotal := float64(line.Quantity) * line.UnitPrice
		// Denormalize price_items.category onto the line so dedup and
		// the admin chip query work per-line on multi-category invoices.
		var lineCategory *string
		if line.PriceItemID != nil {
			var c string
			if err := h.db.QueryRow(ctx,
				`SELECT category FROM price_items WHERE id = $1`, *line.PriceItemID,
			).Scan(&c); err == nil && c != "" {
				lineCategory = &c
			}
		}
		_, err = h.db.Exec(ctx,
			`INSERT INTO invoice_lines (invoice_id, description, sub_description, quantity,
			                            unit_price, line_total, account_id, price_item_id, category)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			invoiceID, line.Description, line.SubDescription, line.Quantity,
			line.UnitPrice, lineTotal, line.AccountID, line.PriceItemID, lineCategory,
		)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to store invoice line")
		}
	}

	// Send email if requested. Org fakturas commonly have a shared
	// invoicing inbox (e.g. faktura@org.no); when the admin supplied
	// a recipient_email override we use that, otherwise fall back to
	// the contact person's personal email.
	var sentAt *string
	deliverTo := memberEmail
	deliverName := memberName
	if req.RecipientEmail != nil && strings.TrimSpace(*req.RecipientEmail) != "" {
		deliverTo = strings.TrimSpace(*req.RecipientEmail)
	}
	if orgRecipient != nil {
		deliverName = orgRecipient.Name
	}
	if req.SendEmail && h.email != nil && deliverTo != "" {
		filename := fmt.Sprintf("faktura-%d.pdf", invoiceSeq)
		// Invoice locale follows the requesting admin's UI language for now.
		// DIL-185 (users.preferred_locale) will swap this for per-member.
		locale := email.DetectLocale(r)
		subject := email.InvoiceSubject(locale, clubName, invoiceSeq)
		htmlBody := email.InvoiceBody(locale, deliverName, clubName, invoiceSeq, dueDate, total, kid, bankAccount)
		if err := h.email.SendWithAttachment(ctx, deliverTo, subject, htmlBody, filename, pdfData); err != nil {
			h.log.Error().Err(err).Str("email", deliverTo).Msg("failed to send invoice email")
		} else {
			now := time.Now().Format(time.RFC3339)
			sentAt = &now
			h.db.Exec(ctx, `UPDATE invoices SET sent_at = NOW() WHERE id = $1`, invoiceID)
			if h.audit != nil {
				h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
					audit.ActionInvoiceEmailed, "invoice", invoiceID,
					map[string]any{"email": deliverTo, "invoice_number": invoiceSeq})
			}
		}
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreated, "invoice", invoiceID,
			map[string]any{"invoice_number": invoiceSeq, "kid": kid, "total": total, "user_id": req.UserID})
	}

	JSON(w, http.StatusCreated, invoiceResponse{
		ID:            invoiceID,
		InvoiceNumber: invoiceSeq,
		KID:           kid,
		UserID:        req.UserID,
		TotalAmount:   total,
		IssueDate:     time.Now().Format("2006-01-02"),
		DueDate:       req.DueDate,
		SentAt:        sentAt,
		CreatedAt:     createdAt,
	})
}

// boatInfo loads the boat + matching slip context (if any) for a
// boat_id chosen on a single-faktura line. Verifies the boat belongs
// to the user and club. Falls back to slipBoatInfo so describeSlipBoat
// can render a unified sub-description.
func (h *InvoiceHandler) boatInfo(ctx context.Context, clubID, userID, boatID string) (*slipBoatInfo, string) {
	var (
		ownerID     *string
		beam        *float64
		length      *float64
		boatName    *string
		mfg         *string
		model       *string
		slipSection *string
		slipNumber  *string
	)
	err := h.db.QueryRow(ctx,
		`SELECT b.user_id, b.beam_m, b.length_m, b.name, b.manufacturer, b.model,
		        s.section, s.number
		   FROM boats b
		   LEFT JOIN slip_assignments sa ON sa.boat_id = b.id AND sa.released_at IS NULL
		   LEFT JOIN slips s ON s.id = sa.slip_id
		  WHERE b.id = $1 AND b.club_id = $2`,
		boatID, clubID,
	).Scan(&ownerID, &beam, &length, &boatName, &mfg, &model, &slipSection, &slipNumber)
	if err == pgx.ErrNoRows {
		return nil, "boat not found"
	}
	if err != nil {
		h.log.Error().Err(err).Msg("boat lookup failed")
		return nil, "boat lookup failed"
	}
	if ownerID == nil || *ownerID != userID {
		return nil, "boat does not belong to selected member"
	}
	info := &slipBoatInfo{beam: beam, length: length}
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
	return info, ""
}

// resolveTier picks the tier slab in `category` whose min/max range
// covers the boat's beam or length per the category's tier_dimension.
// Returns the chosen priceTier and the dimension actually used, or an
// error message if no tier matched / the dimension data is missing.
func (h *InvoiceHandler) resolveTier(ctx context.Context, clubID, category string, info *slipBoatInfo) (priceTier, string, string) {
	rows, err := h.db.Query(ctx,
		`SELECT id, category, amount, description, metadata, tier_dimension
		   FROM price_items
		  WHERE club_id = $1 AND category = $2 AND is_active
		    AND pricing_kind = 'tiered'`,
		clubID, category,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("tier lookup failed")
		return priceTier{}, "", "tier lookup failed"
	}
	defer rows.Close()
	var dimension string
	var tiers []priceTier
	for rows.Next() {
		var t priceTier
		var meta json.RawMessage
		var dim *string
		if err := rows.Scan(&t.id, &t.category, &t.amount, &t.desc, &meta, &dim); err != nil {
			return priceTier{}, "", "tier scan failed"
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
		tiers = append(tiers, t)
	}
	if len(tiers) == 0 {
		return priceTier{}, "", "no tiered items in category " + category
	}
	var measure *float64
	if dimension == "length" {
		measure = info.length
	} else {
		measure = info.beam
	}
	if measure == nil {
		return priceTier{}, dimension, "boat has no " + dimension + " recorded"
	}
	for i := range tiers {
		t := &tiers[i]
		if t.tierMin == nil || t.tierMax == nil {
			continue
		}
		if *measure >= *t.tierMin && *measure < *t.tierMax {
			return *t, dimension, ""
		}
	}
	return priceTier{}, dimension, dimension + " has no matching tier"
}

// HandleGetInvoicePDF returns the stored PDF for an invoice.
func (h *InvoiceHandler) HandleGetInvoicePDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice ID is required")
		return
	}

	var pdfData []byte
	var invoiceNumber int
	var memberLast, memberFirst string
	var fiscalYear *int
	err := h.db.QueryRow(ctx,
		`SELECT i.pdf_data, i.invoice_number,
		        COALESCE(u.last_name, ''), COALESCE(u.first_name, ''),
		        fp.year
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		   LEFT JOIN fiscal_periods fp ON fp.id = i.fiscal_period_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, claims.ClubID,
	).Scan(&pdfData, &invoiceNumber, &memberLast, &memberFirst, &fiscalYear)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "invoice not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch invoice PDF")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if pdfData == nil {
		Error(w, http.StatusNotFound, "PDF not available for this invoice")
		return
	}

	// Default to inline so clicking "preview" opens the PDF in the
	// browser viewer. Append ?download=1 to force a save dialog.
	disposition := "inline"
	if r.URL.Query().Get("download") != "" {
		disposition = "attachment"
	}
	filename := buildInvoiceFilename(invoiceNumber, memberLast, memberFirst, fiscalYear)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, filename))
	w.Write(pdfData)
}

// buildInvoiceFilename produces a descriptive, ASCII-safe filename
// such as "faktura-0001-dillon-ryan-2026.pdf" so a folder of saved
// invoices sorts and searches sensibly.
func buildInvoiceFilename(num int, last, first string, year *int) string {
	parts := []string{fmt.Sprintf("faktura-%04d", num)}
	if s := slugify(last); s != "" {
		parts = append(parts, s)
	}
	if s := slugify(first); s != "" {
		parts = append(parts, s)
	}
	if year != nil && *year > 0 {
		parts = append(parts, fmt.Sprintf("%d", *year))
	}
	return strings.Join(parts, "-") + ".pdf"
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == 'å':
			b.WriteString("aa")
		case r == 'æ':
			b.WriteString("ae")
		case r == 'ø':
			b.WriteString("oe")
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

type draftInvoiceRow struct {
	ID            string    `json:"id"`
	InvoiceNumber int       `json:"invoice_number"`
	UserID        *string   `json:"user_id"`
	MemberName    string    `json:"member_name"`
	MemberEmail   string    `json:"member_email"`
	TotalAmount   float64   `json:"total_amount"`
	IssueDate     string    `json:"issue_date"`
	DueDate       string    `json:"due_date"`
	PriceItemName string    `json:"price_item_name"`
	FiscalYear    *int      `json:"fiscal_year"`
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"created_at"`
}

// HandleListInvoices returns invoices for the club filtered by a
// lifecycle status passed via ?status=. Supported values:
//
//   - "draft" (default): unsent open invoices (sent_at IS NULL AND status='open')
//   - "sent":             sent open invoices  (sent_at IS NOT NULL AND status='open')
//   - "voided":           any voided invoice  (status='voided')
//
// Used by the tabbed Faktura page (DIL-257). The legacy
// "/invoices/drafts" route maps to status=draft for back-compat.
func (h *InvoiceHandler) HandleListInvoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	status := r.URL.Query().Get("status")
	var where string
	switch status {
	case "", "draft":
		where = "i.sent_at IS NULL AND i.status = 'open'"
		status = "draft"
	case "sent":
		where = "i.sent_at IS NOT NULL AND i.status = 'open'"
	case "voided":
		where = "i.status = 'voided'"
	default:
		Error(w, http.StatusBadRequest, "status must be one of: draft, sent, voided")
		return
	}

	// LEFT JOIN users so member-less (org-only) invoices still appear
	// in the list. The recipient name is taken from recipient_org_name
	// when no user is linked, and the email column similarly falls
	// back to recipient_email so the list-row "to" field is always
	// populated.
	rows, err := h.db.Query(ctx,
		`SELECT i.id, i.invoice_number, i.user_id,
		        COALESCE(NULLIF(i.recipient_org_name, ''),
		                 u.full_name,
		                 u.first_name || ' ' || u.last_name,
		                 ''),
		        COALESCE(NULLIF(i.recipient_email, ''), u.email, ''),
		        i.total_amount, i.issue_date, i.due_date,
		        COALESCE(pi.name, ''), fp.year,
		        COALESCE((SELECT description FROM invoice_lines WHERE invoice_id = i.id LIMIT 1), ''),
		        i.created_at, i.sent_at, i.status,
		        (i.payment_id IS NOT NULL) AS paid,
		        (SELECT MAX(created_at) FROM audit_log
		           WHERE club_id = i.club_id
		             AND resource  = 'invoice'
		             AND resource_id = i.id::text
		             AND action = 'invoice.reminded') AS last_reminder_at
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		   LEFT JOIN price_items pi ON pi.id = i.price_item_id
		   LEFT JOIN fiscal_periods fp ON fp.id = i.fiscal_period_id
		  WHERE i.club_id = $1 AND `+where+`
		  ORDER BY i.created_at DESC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("list invoices")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type listRow struct {
		draftInvoiceRow
		SentAt         *time.Time `json:"sent_at"`
		Status         string     `json:"status"`
		Paid           bool       `json:"paid"`
		LastReminderAt *time.Time `json:"last_reminder_at"`
	}
	out := make([]listRow, 0)
	for rows.Next() {
		var d listRow
		var issue, due time.Time
		if err := rows.Scan(&d.ID, &d.InvoiceNumber, &d.UserID,
			&d.MemberName, &d.MemberEmail,
			&d.TotalAmount, &issue, &due,
			&d.PriceItemName, &d.FiscalYear, &d.Description, &d.CreatedAt,
			&d.SentAt, &d.Status, &d.Paid, &d.LastReminderAt); err != nil {
			h.log.Error().Err(err).Msg("scan invoice row")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		d.IssueDate = issue.Format("2006-01-02")
		d.DueDate = due.Format("2006-01-02")
		out = append(out, d)
	}

	JSON(w, http.StatusOK, map[string]any{"items": out, "status": status})
}

// HandleListDraftInvoices is preserved as a thin wrapper around
// HandleListInvoices defaulting to status=draft, so the legacy
// /invoices/drafts route keeps working until callers migrate.
func (h *InvoiceHandler) HandleListDraftInvoices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Set("status", "draft")
	r.URL.RawQuery = q.Encode()
	h.HandleListInvoices(w, r)
}

// HandleSendInvoice emails the stored PDF to the member and stamps
// sent_at. Idempotent: 409 if already sent.
func (h *InvoiceHandler) HandleSendInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice ID is required")
		return
	}

	var (
		invoiceNumber int
		userID        *string
		memberName    string
		memberLast    string
		memberFirst   string
		memberEmail   string
		recipientEmail string
		fiscalYear    *int
		total         float64
		dueDate       time.Time
		kid           string
		pdfData       []byte
		alreadySent   *time.Time
	)
	if err := h.db.QueryRow(ctx,
		`SELECT i.invoice_number, i.user_id,
		        COALESCE(NULLIF(i.recipient_org_name, ''),
		                 u.full_name,
		                 u.first_name || ' ' || u.last_name,
		                 ''),
		        COALESCE(u.last_name, ''), COALESCE(u.first_name, ''),
		        COALESCE(u.email, ''), COALESCE(i.recipient_email, ''),
		        fp.year, i.total_amount, i.due_date, i.kid_number,
		        i.pdf_data, i.sent_at
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		   LEFT JOIN fiscal_periods fp ON fp.id = i.fiscal_period_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, claims.ClubID,
	).Scan(&invoiceNumber, &userID, &memberName, &memberLast, &memberFirst, &memberEmail, &recipientEmail,
		&fiscalYear, &total, &dueDate, &kid, &pdfData, &alreadySent); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "invoice not found")
			return
		}
		h.log.Error().Err(err).Msg("load invoice for send")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if alreadySent != nil {
		Error(w, http.StatusConflict, "invoice already sent")
		return
	}
	if h.email == nil {
		Error(w, http.StatusServiceUnavailable, "email delivery not configured")
		return
	}
	// Prefer the per-invoice recipient email override (e.g. an org's
	// shared faktura@ inbox) when it was set at creation time; fall
	// back to the linked member's address otherwise.
	deliverTo := recipientEmail
	if deliverTo == "" {
		deliverTo = memberEmail
	}
	if deliverTo == "" {
		Error(w, http.StatusBadRequest, "no recipient email on this invoice (set recipient_email or link to a user with an email)")
		return
	}
	if pdfData == nil {
		Error(w, http.StatusInternalServerError, "invoice has no stored PDF")
		return
	}

	var clubName, bankAccount string
	_ = h.db.QueryRow(ctx,
		`SELECT name, COALESCE(bank_account, '') FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&clubName, &bankAccount)

	locale := email.DetectLocale(r)
	subject := email.InvoiceSubject(locale, clubName, invoiceNumber)
	htmlBody := email.InvoiceBody(locale, memberName, clubName, invoiceNumber, dueDate, total, kid, bankAccount)
	filename := buildInvoiceFilename(invoiceNumber, memberLast, memberFirst, fiscalYear)

	// Stamp sent_at BEFORE handing the message to the mail server, then
	// roll back to NULL if the SMTP submission fails. The reverse order
	// (send then stamp) opens a narrow race where the email succeeds but
	// the post-send UPDATE fails (DB blip, ctx cancellation), leaving an
	// invoice on disk as "draft" that the recipient already received.
	// Re-clicking send would then double-deliver. Stamping first flips
	// that failure mode to the safer one: a DB-stamped invoice whose
	// email never actually went out, which the operator can spot via
	// "no Authentication-Results" on the receiver side and recover by
	// nulling sent_at manually.
	if _, err := h.db.Exec(ctx,
		`UPDATE invoices SET sent_at = now() WHERE id = $1`, invoiceID,
	); err != nil {
		h.log.Error().Err(err).Msg("stamp sent_at pre-send")
		Error(w, http.StatusInternalServerError, "failed to mark invoice as sending")
		return
	}
	if err := h.email.SendWithAttachment(ctx, deliverTo, subject, htmlBody, filename, pdfData); err != nil {
		h.log.Error().Err(err).Str("email", deliverTo).Msg("send invoice email — rolling back sent_at")
		// Best-effort rollback. If this also fails we log loudly so the
		// operator can spot the stuck row; the alternative (leaving it
		// stamped) is acceptable because the recipient definitely did
		// not get the message.
		if _, rbErr := h.db.Exec(context.Background(),
			`UPDATE invoices SET sent_at = NULL WHERE id = $1`, invoiceID,
		); rbErr != nil {
			h.log.Error().Err(rbErr).Str("invoice_id", invoiceID).Msg("CRITICAL: failed to roll back sent_at after email error — manual reset required")
		}
		Error(w, http.StatusBadGateway, "failed to send email")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceEmailed, "invoice", invoiceID,
			map[string]any{"email": deliverTo, "invoice_number": invoiceNumber})
	}
	_ = userID

	JSON(w, http.StatusOK, map[string]any{"id": invoiceID, "sent": true})
}

// HandleResendInvoice resends the stored PDF to the recipient AFTER
// a previous successful send. Unlike HandleSendInvoice this is not
// gated on sent_at being NULL — the whole point is to recover from
// delivery failures (recipient claims they never got it, spam-
// filter ate it, etc.). The original sent_at is preserved so the
// audit trail of the FIRST send isn't overwritten; the resend is
// recorded only via the audit log with resend=true.
func (h *InvoiceHandler) HandleResendInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice ID is required")
		return
	}

	var (
		invoiceNumber  int
		memberName     string
		memberLast     string
		memberFirst    string
		memberEmail    string
		recipientEmail string
		fiscalYear     *int
		total          float64
		dueDate        time.Time
		kid            string
		pdfData        []byte
		sentAt         *time.Time
	)
	if err := h.db.QueryRow(ctx,
		`SELECT i.invoice_number,
		        COALESCE(NULLIF(i.recipient_org_name, ''),
		                 u.full_name,
		                 u.first_name || ' ' || u.last_name,
		                 ''),
		        COALESCE(u.last_name, ''), COALESCE(u.first_name, ''),
		        COALESCE(u.email, ''), COALESCE(i.recipient_email, ''),
		        fp.year, i.total_amount, i.due_date, i.kid_number,
		        i.pdf_data, i.sent_at
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		   LEFT JOIN fiscal_periods fp ON fp.id = i.fiscal_period_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, claims.ClubID,
	).Scan(&invoiceNumber, &memberName, &memberLast, &memberFirst, &memberEmail, &recipientEmail,
		&fiscalYear, &total, &dueDate, &kid, &pdfData, &sentAt); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "invoice not found")
			return
		}
		h.log.Error().Err(err).Msg("load invoice for resend")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if sentAt == nil {
		// Resend only makes sense after a successful first send;
		// otherwise the operator wants the regular Send action so
		// sent_at gets stamped.
		Error(w, http.StatusBadRequest, "invoice has not been sent yet — use Send, not Resend")
		return
	}
	if h.email == nil {
		Error(w, http.StatusServiceUnavailable, "email delivery not configured")
		return
	}
	deliverTo := recipientEmail
	if deliverTo == "" {
		deliverTo = memberEmail
	}
	if deliverTo == "" {
		Error(w, http.StatusBadRequest, "no recipient email on this invoice")
		return
	}
	if pdfData == nil {
		Error(w, http.StatusInternalServerError, "invoice has no stored PDF")
		return
	}

	var clubName, bankAccount string
	_ = h.db.QueryRow(ctx,
		`SELECT name, COALESCE(bank_account, '') FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&clubName, &bankAccount)

	locale := email.DetectLocale(r)
	subject := email.InvoiceSubject(locale, clubName, invoiceNumber)
	htmlBody := email.InvoiceBody(locale, memberName, clubName, invoiceNumber, dueDate, total, kid, bankAccount)
	filename := buildInvoiceFilename(invoiceNumber, memberLast, memberFirst, fiscalYear)

	if err := h.email.SendWithAttachment(ctx, deliverTo, subject, htmlBody, filename, pdfData); err != nil {
		h.log.Error().Err(err).Str("email", deliverTo).Msg("resend invoice email failed")
		Error(w, http.StatusBadGateway, "failed to send email")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceEmailed, "invoice", invoiceID,
			map[string]any{
				"email":          deliverTo,
				"invoice_number": invoiceNumber,
				"resend":         true,
				"original_sent_at": sentAt.Format(time.RFC3339),
			})
	}

	JSON(w, http.StatusOK, map[string]any{"id": invoiceID, "resent": true})
}

// HandleDeleteInvoice permanently removes an invoice. Allowed for
// drafts (sent_at IS NULL) and for voided invoices — sent+open
// invoices must be voided first so an audit trail of the original
// send remains until the admin explicitly retires it.
func (h *InvoiceHandler) HandleDeleteInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice ID is required")
		return
	}

	tag, err := h.db.Exec(ctx,
		`DELETE FROM invoices
		  WHERE id = $1 AND club_id = $2
		    AND (sent_at IS NULL OR status = 'voided')`,
		invoiceID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("delete invoice")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusConflict, "invoice cannot be deleted — void it first")
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreated, "invoice", invoiceID,
			map[string]any{"deleted": true})
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleVoidInvoice flips an invoice's status to 'voided'. Voided
// invoices fall out of the bulk-faktura dedup index so the admin can
// re-issue under the same (member, category, period) without manual
// SQL. Idempotent — voiding an already-voided invoice returns 204.
func (h *InvoiceHandler) HandleVoidInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice ID is required")
		return
	}
	tag, err := h.db.Exec(ctx,
		`UPDATE invoices SET status = 'voided'
		  WHERE id = $1 AND club_id = $2`,
		invoiceID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("void invoice")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "invoice not found")
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceCreated, "invoice", invoiceID,
			map[string]any{"voided": true})
	}
	w.WriteHeader(http.StatusNoContent)
}
