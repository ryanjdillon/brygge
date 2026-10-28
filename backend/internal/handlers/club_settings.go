package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type ClubSettingsHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	log    zerolog.Logger
}

func NewClubSettingsHandler(db *pgxpool.Pool, cfg *config.Config, log zerolog.Logger) *ClubSettingsHandler {
	return &ClubSettingsHandler{
		db:     db,
		config: cfg,
		log:    log.With().Str("handler", "club_settings").Logger(),
	}
}

type clubSetting struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type updateSettingsRequest struct {
	Settings map[string]json.RawMessage `json:"settings"`
}

type generalSettings struct {
	DefaultLanguage string `json:"default_language"`
}

// HandleGetGeneralSettings returns club-wide general settings
// (currently just the default UI/content language).
func (h *ClubSettingsHandler) HandleGetGeneralSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var s generalSettings
	err := h.db.QueryRow(ctx,
		`SELECT default_language FROM clubs WHERE id = $1`, claims.ClubID,
	).Scan(&s.DefaultLanguage)
	if err == pgx.ErrNoRows {
		Error(w, http.StatusNotFound, "club not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to fetch general settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, s)
}

// HandleUpdateGeneralSettings sets the club default language. The value
// is the fallback for members with no explicit preference and drives
// AI/anonymous-facing copy (DIL-329).
func (h *ClubSettingsHandler) HandleUpdateGeneralSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req generalSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	lang := strings.TrimSpace(req.DefaultLanguage)
	if !supportedUILocales[lang] {
		Error(w, http.StatusBadRequest, "unsupported language")
		return
	}

	tag, err := h.db.Exec(ctx,
		`UPDATE clubs SET default_language = $2 WHERE id = $1`,
		claims.ClubID, lang,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update default language")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "club not found")
		return
	}

	JSON(w, http.StatusOK, generalSettings{DefaultLanguage: lang})
}

func (h *ClubSettingsHandler) HandleGetBookingSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT key, value, updated_at FROM club_settings WHERE club_id = $1 ORDER BY key`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list club settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	settings := make(map[string]json.RawMessage)
	for rows.Next() {
		var s clubSetting
		if err := rows.Scan(&s.Key, &s.Value, &s.UpdatedAt); err != nil {
			h.log.Error().Err(err).Msg("failed to scan setting")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		settings[s.Key] = s.Value
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, settings)
}

func (h *ClubSettingsHandler) HandleUpdateBookingSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req updateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Settings) == 0 {
		Error(w, http.StatusBadRequest, "settings map is required")
		return
	}

	allowedKeys := map[string]bool{
		"hoist_slot_duration_minutes": true,
		"hoist_open_hour":             true,
		"hoist_close_hour":            true,
		"hoist_max_consecutive_slots": true,
		"slip_share_rebate_pct":       true,
		"season_summer_start":         true,
		"season_summer_end":           true,
		"season_winter_start":         true,
		"season_winter_end":           true,
	}

	for key := range req.Settings {
		if !allowedKeys[key] {
			Error(w, http.StatusBadRequest, "unknown setting key: "+key)
			return
		}
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	for key, value := range req.Settings {
		_, err := tx.Exec(ctx,
			`INSERT INTO club_settings (club_id, key, value, updated_at)
			 VALUES ($1, $2, $3, now())
			 ON CONFLICT (club_id, key) DO UPDATE SET value = $3, updated_at = now()`,
			claims.ClubID, key, value,
		)
		if err != nil {
			h.log.Error().Err(err).Str("key", key).Msg("failed to upsert setting")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "update_booking_settings", "club_settings", claims.ClubID, nil, req.Settings); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("failed to write audit log")
	}

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

type clubFinancialSettings struct {
	Name                  string   `json:"name"`
	OrgNumber             string   `json:"org_number"`
	Address               string   `json:"address"`
	Phone                 string   `json:"phone"`
	VHFChannel            string   `json:"vhf_channel"`
	Latitude              *float64 `json:"latitude"`
	Longitude             *float64 `json:"longitude"`
	BankAccount           string   `json:"bank_account"`
	KIDCheckMethod        string   `json:"kid_check_method"`
	VippsClearingAccount  string   `json:"vipps_clearing_account"`
	PaymentFeeAccount     string   `json:"payment_fee_account"`
	VippsPerChargeEnabled bool     `json:"vipps_per_charge_enabled"`
	// StripeClearingAccount holds card payments until Stripe pays them out;
	// StripePayoutDescriptor is the regular expression that recognises a
	// Stripe payout in the bank statement text.
	StripeClearingAccount  string `json:"stripe_clearing_account"`
	StripePayoutDescriptor string `json:"stripe_payout_descriptor"`
	// StripeConfigured is whether this instance has its Stripe credentials,
	// so the accounting views offer the Stripe sync. Read only.
	StripeConfigured bool `json:"stripe_configured"`
	// OnlineMaxAmount caps which invoices may offer online payment
	// (BRY-283): null = no limit; otherwise invoices over this kr amount are
	// bank-transfer only.
	OnlineMaxAmount *float64 `json:"online_max_amount"`
	// VippsAutoAssign opts the club into booking unambiguous Vipps charges
	// without a human (BRY-340). VippsAutoAssignTieCategory is the price
	// category to use when an amount fits items in several categories;
	// empty leaves those charges for review. BankAutoAssign is the separate
	// opt-in for KID-less bank deposits (BRY-366).
	VippsAutoAssign            bool   `json:"vipps_auto_assign"`
	VippsAutoAssignTieCategory string `json:"vipps_auto_assign_tie_category"`
	BankAutoAssign             bool   `json:"bank_auto_assign"`
	WebsiteURL                 string `json:"website_url"`
	ChairmanEmail              string `json:"chairman_email"`
	ViceChairmanEmail          string `json:"vice_chairman_email"`
	TreasurerEmail             string `json:"treasurer_email"`
	SecretaryEmail             string `json:"secretary_email"`
	HarborMasterEmail          string `json:"harbor_master_email"`
	HasFakturaLogo             bool   `json:"has_faktura_logo"`
	FakturaLogoMIME            string `json:"faktura_logo_mime"`
	HasSiteLogo                bool   `json:"has_site_logo"`
	SiteLogoMIME               string `json:"site_logo_mime"`
	// Public site content — empty string means "use the frontend's
	// i18n fallback so the page reads sensibly even before any admin
	// has visited settings".
	HarborApproach          string `json:"harbor_approach"`
	HarborDepth             string `json:"harbor_depth"`
	HarborVHF               string `json:"harbor_vhf"`
	HarborCTATitle          string `json:"harbor_cta_title"`
	HarborCTADescription    string `json:"harbor_cta_description"`
	MotorhomePower          string `json:"motorhome_power"`
	MotorhomeFacilities     string `json:"motorhome_facilities"`
	MotorhomeCheckin        string `json:"motorhome_checkin"`
	MotorhomeRules          string `json:"motorhome_rules"`
	MotorhomeCTATitle       string `json:"motorhome_cta_title"`
	MotorhomeCTADescription string `json:"motorhome_cta_description"`
	// Per-club module toggles. Admins flip these from Site settings;
	// the public /api/v1/features endpoint reads them too.
	FeatureBookings   bool `json:"feature_bookings"`
	FeatureProjects   bool `json:"feature_projects"`
	FeatureCalendar   bool `json:"feature_calendar"`
	FeatureCommerce   bool `json:"feature_commerce"`
	FeatureAccounting bool `json:"feature_accounting"`
	HasAnthropicKey   bool `json:"has_anthropic_key"`
}

// HandleGetFinancialSettings returns the club's invoice-relevant
// fields stored on the clubs table (org_number, address, bank_account).
// These render on every faktura PDF.
func (h *ClubSettingsHandler) HandleGetFinancialSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var s clubFinancialSettings
	if err := h.db.QueryRow(ctx,
		`SELECT name, COALESCE(org_number, ''), COALESCE(address, ''),
		        COALESCE(phone, ''), COALESCE(vhf_channel, ''),
		        latitude, longitude,
		        COALESCE(bank_account, ''),
		        COALESCE(kid_check_method, 'mod11'),
		        COALESCE(vipps_clearing_account, '2900'),
		        COALESCE(payment_fee_account, '7700'),
		        COALESCE(vipps_per_charge_enabled, false),
		        stripe_clearing_account, stripe_payout_descriptor,
		        online_max_amount,
		        vipps_auto_assign, COALESCE(vipps_auto_assign_tie_category, ''),
		        bank_auto_assign,
		        COALESCE(website_url, ''),
		        COALESCE(chairman_email, ''), COALESCE(vice_chairman_email, ''),
		        COALESCE(treasurer_email, ''),
		        COALESCE(secretary_email, ''), COALESCE(harbor_master_email, ''),
		        (faktura_logo_data IS NOT NULL), COALESCE(faktura_logo_mime, ''),
		        (site_logo_data IS NOT NULL), COALESCE(site_logo_mime, ''),
		        COALESCE(harbor_approach, ''), COALESCE(harbor_depth, ''),
		        COALESCE(harbor_vhf, ''),
		        COALESCE(harbor_cta_title, ''), COALESCE(harbor_cta_description, ''),
		        COALESCE(motorhome_power, ''), COALESCE(motorhome_facilities, ''),
		        COALESCE(motorhome_checkin, ''), COALESCE(motorhome_rules, ''),
		        COALESCE(motorhome_cta_title, ''), COALESCE(motorhome_cta_description, ''),
		        feature_bookings, feature_projects, feature_calendar,
		        feature_commerce, feature_accounting,
		        (anthropic_api_key IS NOT NULL AND anthropic_api_key != '')
		   FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&s.Name, &s.OrgNumber, &s.Address,
		&s.Phone, &s.VHFChannel,
		&s.Latitude, &s.Longitude,
		&s.BankAccount,
		&s.KIDCheckMethod,
		&s.VippsClearingAccount, &s.PaymentFeeAccount,
		&s.VippsPerChargeEnabled,
		&s.StripeClearingAccount, &s.StripePayoutDescriptor,
		&s.OnlineMaxAmount,
		&s.VippsAutoAssign, &s.VippsAutoAssignTieCategory,
		&s.BankAutoAssign,
		&s.WebsiteURL,
		&s.ChairmanEmail, &s.ViceChairmanEmail,
		&s.TreasurerEmail,
		&s.SecretaryEmail, &s.HarborMasterEmail,
		&s.HasFakturaLogo, &s.FakturaLogoMIME,
		&s.HasSiteLogo, &s.SiteLogoMIME,
		&s.HarborApproach, &s.HarborDepth,
		&s.HarborVHF,
		&s.HarborCTATitle, &s.HarborCTADescription,
		&s.MotorhomePower, &s.MotorhomeFacilities,
		&s.MotorhomeCheckin, &s.MotorhomeRules,
		&s.MotorhomeCTATitle, &s.MotorhomeCTADescription,
		&s.FeatureBookings, &s.FeatureProjects, &s.FeatureCalendar,
		&s.FeatureCommerce, &s.FeatureAccounting,
		&s.HasAnthropicKey); err != nil {
		h.log.Error().Err(err).Msg("load financial settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if h.config != nil {
		s.StripeConfigured, _ = h.config.StripeCredentials()
	}
	JSON(w, http.StatusOK, s)
}

type updateFinancialSettingsRequest struct {
	OrgNumber             *string  `json:"org_number,omitempty"`
	Address               *string  `json:"address,omitempty"`
	Phone                 *string  `json:"phone,omitempty"`
	VHFChannel            *string  `json:"vhf_channel,omitempty"`
	Latitude              *float64 `json:"latitude,omitempty"`
	Longitude             *float64 `json:"longitude,omitempty"`
	BankAccount           *string  `json:"bank_account,omitempty"`
	KIDCheckMethod        *string  `json:"kid_check_method,omitempty"`
	VippsClearingAccount  *string  `json:"vipps_clearing_account,omitempty"`
	PaymentFeeAccount     *string  `json:"payment_fee_account,omitempty"`
	VippsPerChargeEnabled *bool    `json:"vipps_per_charge_enabled,omitempty"`
	// StripeClearingAccount must be an active asset account in the club's
	// chart; StripePayoutDescriptor must compile as a Go regular expression.
	StripeClearingAccount  *string `json:"stripe_clearing_account,omitempty"`
	StripePayoutDescriptor *string `json:"stripe_payout_descriptor,omitempty"`
	// OnlineMaxAmount: omitted = keep; <= 0 = clear the cap (unlimited);
	// > 0 = set the cap (BRY-283).
	OnlineMaxAmount *float64 `json:"online_max_amount,omitempty"`
	// VippsAutoAssignTieCategory: omitted = keep; "" = clear; otherwise a
	// price_items category.
	VippsAutoAssign            *bool   `json:"vipps_auto_assign,omitempty"`
	VippsAutoAssignTieCategory *string `json:"vipps_auto_assign_tie_category,omitempty"`
	BankAutoAssign             *bool   `json:"bank_auto_assign,omitempty"`
	WebsiteURL                 *string `json:"website_url,omitempty"`
	ChairmanEmail              *string `json:"chairman_email,omitempty"`
	ViceChairmanEmail          *string `json:"vice_chairman_email,omitempty"`
	TreasurerEmail             *string `json:"treasurer_email,omitempty"`
	SecretaryEmail             *string `json:"secretary_email,omitempty"`
	HarborMasterEmail          *string `json:"harbor_master_email,omitempty"`
	HarborApproach             *string `json:"harbor_approach,omitempty"`
	HarborDepth                *string `json:"harbor_depth,omitempty"`
	HarborVHF                  *string `json:"harbor_vhf,omitempty"`
	HarborCTATitle             *string `json:"harbor_cta_title,omitempty"`
	HarborCTADescription       *string `json:"harbor_cta_description,omitempty"`
	MotorhomePower             *string `json:"motorhome_power,omitempty"`
	MotorhomeFacilities        *string `json:"motorhome_facilities,omitempty"`
	MotorhomeCheckin           *string `json:"motorhome_checkin,omitempty"`
	MotorhomeRules             *string `json:"motorhome_rules,omitempty"`
	MotorhomeCTATitle          *string `json:"motorhome_cta_title,omitempty"`
	MotorhomeCTADescription    *string `json:"motorhome_cta_description,omitempty"`
	FeatureBookings            *bool   `json:"feature_bookings,omitempty"`
	FeatureProjects            *bool   `json:"feature_projects,omitempty"`
	FeatureCalendar            *bool   `json:"feature_calendar,omitempty"`
	FeatureCommerce            *bool   `json:"feature_commerce,omitempty"`
	FeatureAccounting          *bool   `json:"feature_accounting,omitempty"`
	AnthropicAPIKey            *string `json:"anthropic_api_key,omitempty"`
}

// HandleUpdateFinancialSettings updates org_number, address, and
// bank_account on the clubs row. Each field is optional; only supplied
// keys are written.
func (h *ClubSettingsHandler) HandleUpdateFinancialSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req updateFinancialSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Treat empty anthropic key as nil so we don't overwrite an existing key with "".
	anthropicKey := req.AnthropicAPIKey
	if anthropicKey != nil && *anthropicKey == "" {
		anthropicKey = nil
	}
	// Guard the KID check method to the two recognised OCR schemes so an
	// invalid value can never reach the generator (would break every faktura).
	if req.KIDCheckMethod != nil &&
		*req.KIDCheckMethod != finance.KIDMethodMod10 &&
		*req.KIDCheckMethod != finance.KIDMethodMod11 {
		Error(w, http.StatusBadRequest, "kid_check_method must be mod10 or mod11")
		return
	}

	if req.VippsAutoAssignTieCategory != nil && *req.VippsAutoAssignTieCategory != "" &&
		!validCategories[*req.VippsAutoAssignTieCategory] {
		Error(w, http.StatusBadRequest, "vipps_auto_assign_tie_category must be a price category")
		return
	}
	msg, err := h.validateStripeSettings(ctx, claims.ClubID, &req)
	if err != nil {
		h.log.Error().Err(err).Msg("validate stripe settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if msg != "" {
		Error(w, http.StatusBadRequest, msg)
		return
	}

	// Build COALESCE-style update so unspecified fields stay as-is.
	// Empty-fields check is intentionally relaxed: with this many
	// optional knobs an admin saving a single tweak is still valid.
	if _, err := h.db.Exec(ctx,
		`UPDATE clubs SET
		   org_number                = COALESCE($2,  org_number),
		   address                   = COALESCE($3,  address),
		   phone                     = COALESCE($4,  phone),
		   vhf_channel               = COALESCE($5,  vhf_channel),
		   latitude                  = COALESCE($6,  latitude),
		   longitude                 = COALESCE($7,  longitude),
		   bank_account              = COALESCE($8,  bank_account),
		   website_url               = COALESCE($9,  website_url),
		   chairman_email            = COALESCE($10, chairman_email),
		   vice_chairman_email       = COALESCE($11, vice_chairman_email),
		   treasurer_email           = COALESCE($12, treasurer_email),
		   secretary_email           = COALESCE($13, secretary_email),
		   harbor_master_email       = COALESCE($14, harbor_master_email),
		   harbor_approach           = COALESCE($15, harbor_approach),
		   harbor_depth              = COALESCE($16, harbor_depth),
		   harbor_vhf                = COALESCE($17, harbor_vhf),
		   harbor_cta_title          = COALESCE($18, harbor_cta_title),
		   harbor_cta_description    = COALESCE($19, harbor_cta_description),
		   motorhome_power           = COALESCE($20, motorhome_power),
		   motorhome_facilities      = COALESCE($21, motorhome_facilities),
		   motorhome_checkin         = COALESCE($22, motorhome_checkin),
		   motorhome_rules           = COALESCE($23, motorhome_rules),
		   motorhome_cta_title       = COALESCE($24, motorhome_cta_title),
		   motorhome_cta_description = COALESCE($25, motorhome_cta_description),
		   feature_bookings          = COALESCE($26, feature_bookings),
		   feature_projects          = COALESCE($27, feature_projects),
		   feature_calendar          = COALESCE($28, feature_calendar),
		   feature_commerce          = COALESCE($29, feature_commerce),
		   feature_accounting        = COALESCE($30, feature_accounting),
		   anthropic_api_key         = COALESCE($31, anthropic_api_key),
		   kid_check_method          = COALESCE($32, kid_check_method),
		   vipps_clearing_account    = COALESCE($33, vipps_clearing_account),
		   payment_fee_account       = COALESCE($34, payment_fee_account),
		   vipps_fee_account         = COALESCE($34, payment_fee_account),
		   vipps_per_charge_enabled  = COALESCE($35, vipps_per_charge_enabled),
		   online_max_amount         = CASE
		                                 WHEN $36::numeric IS NULL THEN online_max_amount
		                                 WHEN $36::numeric <= 0    THEN NULL
		                                 ELSE $36::numeric END,
		   vipps_max_amount          = CASE
		                                 WHEN $36::numeric IS NULL THEN online_max_amount
		                                 WHEN $36::numeric <= 0    THEN NULL
		                                 ELSE $36::numeric END,
		   vipps_auto_assign         = COALESCE($37, vipps_auto_assign),
		   vipps_auto_assign_tie_category = CASE
		                                 WHEN $38::text IS NULL THEN vipps_auto_assign_tie_category
		                                 ELSE NULLIF($38::text, '') END,
		   bank_auto_assign          = COALESCE($39, bank_auto_assign),
		   stripe_clearing_account   = COALESCE($40, stripe_clearing_account),
		   stripe_payout_descriptor  = COALESCE($41, stripe_payout_descriptor)
		 WHERE id = $1`,
		claims.ClubID, req.OrgNumber, req.Address, req.Phone, req.VHFChannel,
		req.Latitude, req.Longitude,
		req.BankAccount,
		req.WebsiteURL, req.ChairmanEmail, req.ViceChairmanEmail, req.TreasurerEmail,
		req.SecretaryEmail, req.HarborMasterEmail,
		req.HarborApproach, req.HarborDepth, req.HarborVHF,
		req.HarborCTATitle, req.HarborCTADescription,
		req.MotorhomePower, req.MotorhomeFacilities,
		req.MotorhomeCheckin, req.MotorhomeRules,
		req.MotorhomeCTATitle, req.MotorhomeCTADescription,
		req.FeatureBookings, req.FeatureProjects, req.FeatureCalendar,
		req.FeatureCommerce, req.FeatureAccounting,
		anthropicKey,
		req.KIDCheckMethod,
		req.VippsClearingAccount, req.PaymentFeeAccount,
		req.VippsPerChargeEnabled,
		req.OnlineMaxAmount,
		req.VippsAutoAssign, req.VippsAutoAssignTieCategory,
		req.BankAutoAssign,
		req.StripeClearingAccount, req.StripePayoutDescriptor,
	); err != nil {
		h.log.Error().Err(err).Msg("update financial settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "update_financial_settings", "clubs", claims.ClubID, nil, req); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit financial settings")
	}
	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// maxPayoutDescriptorLen caps the payout descriptor pattern; a bank
// statement line is short, and so is any pattern that matches one.
const maxPayoutDescriptorLen = 200

// validateStripeSettings checks the Stripe accounting settings in req,
// trimming them in place. It returns a client-facing message for invalid
// input, or an error when the check itself failed.
func (h *ClubSettingsHandler) validateStripeSettings(ctx context.Context, clubID string, req *updateFinancialSettingsRequest) (string, error) {
	if p := req.StripePayoutDescriptor; p != nil {
		*p = strings.TrimSpace(*p)
		if *p == "" || len(*p) > maxPayoutDescriptorLen {
			return fmt.Sprintf("stripe_payout_descriptor must be 1-%d characters", maxPayoutDescriptorLen), nil
		}
		if _, err := regexp.Compile(*p); err != nil {
			return "stripe_payout_descriptor is not a valid regular expression", nil
		}
	}
	if code := req.StripeClearingAccount; code != nil {
		*code = strings.TrimSpace(*code)
		var ok bool
		if err := h.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM accounts
			                 WHERE club_id = $1 AND code = $2
			                   AND is_active AND account_type = 'asset'::account_type)`,
			clubID, *code,
		).Scan(&ok); err != nil {
			return "", err
		}
		if !ok {
			return "stripe_clearing_account must be an active asset account in the chart of accounts", nil
		}
	}
	return "", nil
}

const maxLogoBytes = 2 * 1024 * 1024

// uploadLogo is the shared body of the two upload endpoints. kind
// drives both the column pair being written and the accepted MIME
// types — faktura logo is raster-only because the PDF library can't
// rasterize vector formats; site logo is SVG-only so it scales
// crisply in the navbar regardless of viewport size.
func (h *ClubSettingsHandler) uploadLogo(w http.ResponseWriter, r *http.Request, kind string) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+1024)
	if err := r.ParseMultipartForm(maxLogoBytes + 1024); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
		Error(w, http.StatusBadRequest, "invalid multipart body")
		return
	}
	file, _, err := r.FormFile("logo")
	if err != nil {
		Error(w, http.StatusBadRequest, "logo file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
	if err != nil {
		Error(w, http.StatusBadRequest, "could not read upload")
		return
	}
	if len(data) > maxLogoBytes {
		Error(w, http.StatusRequestEntityTooLarge, "logo exceeds 2 MB")
		return
	}
	mime := http.DetectContentType(data)
	var dataCol, mimeCol string
	switch kind {
	case "faktura":
		if mime != "image/png" && mime != "image/jpeg" {
			Error(w, http.StatusUnsupportedMediaType, "faktura logo must be PNG or JPEG")
			return
		}
		dataCol, mimeCol = "faktura_logo_data", "faktura_logo_mime"
	case "site":
		// http.DetectContentType returns "text/xml; charset=utf-8" for
		// SVG so we sniff for the SVG signature explicitly. Reject
		// anything else outright.
		if !looksLikeSVG(data) {
			Error(w, http.StatusUnsupportedMediaType, "site logo must be SVG")
			return
		}
		mime = "image/svg+xml"
		dataCol, mimeCol = "site_logo_data", "site_logo_mime"
	default:
		Error(w, http.StatusBadRequest, "unknown logo kind")
		return
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE clubs SET `+dataCol+` = $2, `+mimeCol+` = $3 WHERE id = $1`,
		claims.ClubID, data, mime,
	); err != nil {
		h.log.Error().Err(err).Msg("save club logo")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, "upload_"+kind+"_logo", "clubs", claims.ClubID, nil, map[string]any{"mime": mime, "size": len(data)}); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit club logo upload")
	}
	JSON(w, http.StatusOK, map[string]any{"status": "updated", "mime": mime, "size": len(data)})
}

// looksLikeSVG sniffs an upload buffer for an SVG root element.
// We ignore an XML declaration and DOCTYPE, accept whitespace, and
// require the first non-trivial element to be `<svg`.
func looksLikeSVG(data []byte) bool {
	s := strings.TrimSpace(string(data))
	if strings.HasPrefix(s, "<?xml") {
		if i := strings.Index(s, "?>"); i != -1 {
			s = strings.TrimSpace(s[i+2:])
		}
	}
	if strings.HasPrefix(strings.ToLower(s), "<!doctype") {
		if i := strings.Index(s, ">"); i != -1 {
			s = strings.TrimSpace(s[i+1:])
		}
	}
	return strings.HasPrefix(strings.ToLower(s), "<svg")
}

func (h *ClubSettingsHandler) HandleUploadFakturaLogo(w http.ResponseWriter, r *http.Request) {
	h.uploadLogo(w, r, "faktura")
}
func (h *ClubSettingsHandler) HandleUploadSiteLogo(w http.ResponseWriter, r *http.Request) {
	h.uploadLogo(w, r, "site")
}

// HandleGetPublicClub returns public club info. Name/slug/domain come from the
// static deploy config so the response is available even before any admin has
// opened the settings page; contact channels, the logo flag and the default
// language come from the DB (edited in the admin UI) so they change without a
// redeploy.
func (h *ClubSettingsHandler) HandleGetPublicClub(w http.ResponseWriter, r *http.Request) {
	var (
		orgNumber               string
		address                 string
		phone                   string
		vhf                     string
		lat                     *float64
		lon                     *float64
		website                 string
		chairman                string
		viceChairman            string
		treasurer               string
		secretary               string
		harborMaster            string
		hasLogo                 bool
		harborApproach          string
		harborDepth             string
		harborVHF               string
		harborCTATitle          string
		harborCTADescription    string
		motorhomePower          string
		motorhomeFacilities     string
		motorhomeCheckin        string
		motorhomeRules          string
		motorhomeCTATitle       string
		motorhomeCTADescription string
		defaultLanguage         string
	)
	_ = h.db.QueryRow(r.Context(),
		`SELECT COALESCE(org_number, ''),
		        COALESCE(address, ''),
		        COALESCE(phone, ''),
		        COALESCE(vhf_channel, ''),
		        latitude, longitude,
		        COALESCE(website_url, ''),
		        COALESCE(chairman_email, ''),
		        COALESCE(vice_chairman_email, ''),
		        COALESCE(treasurer_email, ''),
		        COALESCE(secretary_email, ''),
		        COALESCE(harbor_master_email, ''),
		        (site_logo_data IS NOT NULL AND octet_length(site_logo_data) > 0),
		        COALESCE(harbor_approach, ''),
		        COALESCE(harbor_depth, ''),
		        COALESCE(harbor_vhf, ''),
		        COALESCE(harbor_cta_title, ''),
		        COALESCE(harbor_cta_description, ''),
		        COALESCE(motorhome_power, ''),
		        COALESCE(motorhome_facilities, ''),
		        COALESCE(motorhome_checkin, ''),
		        COALESCE(motorhome_rules, ''),
		        COALESCE(motorhome_cta_title, ''),
		        COALESCE(motorhome_cta_description, ''),
		        default_language
		   FROM clubs WHERE slug = $1`,
		h.config.ClubSlug,
	).Scan(&orgNumber, &address, &phone, &vhf, &lat, &lon,
		&website, &chairman, &viceChairman, &treasurer, &secretary, &harborMaster, &hasLogo,
		&harborApproach, &harborDepth, &harborVHF, &harborCTATitle, &harborCTADescription,
		&motorhomePower, &motorhomeFacilities, &motorhomeCheckin, &motorhomeRules,
		&motorhomeCTATitle, &motorhomeCTADescription, &defaultLanguage)
	// The clubs row may not exist yet on a fresh deploy; the column default
	// is the right answer then too.
	if defaultLanguage == "" {
		defaultLanguage = "nb"
	}
	JSON(w, http.StatusOK, map[string]any{
		"name":                      h.config.ClubName,
		"slug":                      h.config.ClubSlug,
		"domain":                    h.config.Domain,
		"org_number":                orgNumber,
		"address":                   address,
		"phone":                     phone,
		"vhf_channel":               vhf,
		"latitude":                  lat,
		"longitude":                 lon,
		"website_url":               website,
		"chairman_email":            chairman,
		"vice_chairman_email":       viceChairman,
		"treasurer_email":           treasurer,
		"secretary_email":           secretary,
		"harbor_master_email":       harborMaster,
		"has_logo":                  hasLogo, // legacy alias for has_site_logo
		"has_site_logo":             hasLogo,
		"harbor_approach":           harborApproach,
		"harbor_depth":              harborDepth,
		"harbor_vhf":                harborVHF,
		"harbor_cta_title":          harborCTATitle,
		"harbor_cta_description":    harborCTADescription,
		"motorhome_power":           motorhomePower,
		"motorhome_facilities":      motorhomeFacilities,
		"motorhome_checkin":         motorhomeCheckin,
		"motorhome_rules":           motorhomeRules,
		"motorhome_cta_title":       motorhomeCTATitle,
		"motorhome_cta_description": motorhomeCTADescription,
		"default_language":          defaultLanguage,
	})
}

// HandleGetPublicClubLogo streams the stored *site* logo without
// auth — that's the one consumed by the navbar and other public
// pages. Returns 404 when no site logo is set so the frontend falls
// back to clubname-only.
func (h *ClubSettingsHandler) HandleGetPublicClubLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var data []byte
	var mime string
	err := h.db.QueryRow(ctx,
		`SELECT site_logo_data, COALESCE(site_logo_mime, '') FROM clubs WHERE slug = $1`,
		h.config.ClubSlug,
	).Scan(&data, &mime)
	if err == pgx.ErrNoRows || len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("load public site logo")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.Copy(w, bytes.NewReader(data))
}

// streamLogo is the shared GET handler for the admin-side logo
// endpoints. dataCol/mimeCol pick which column pair to read.
func (h *ClubSettingsHandler) streamLogo(w http.ResponseWriter, r *http.Request, dataCol, mimeCol string) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var data []byte
	var mime string
	err := h.db.QueryRow(ctx,
		`SELECT `+dataCol+`, COALESCE(`+mimeCol+`, '') FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&data, &mime)
	if err == pgx.ErrNoRows || len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("load club logo")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = io.Copy(w, bytes.NewReader(data))
}

func (h *ClubSettingsHandler) HandleGetFakturaLogo(w http.ResponseWriter, r *http.Request) {
	h.streamLogo(w, r, "faktura_logo_data", "faktura_logo_mime")
}
func (h *ClubSettingsHandler) HandleGetSiteLogo(w http.ResponseWriter, r *http.Request) {
	h.streamLogo(w, r, "site_logo_data", "site_logo_mime")
}

// HandleDeleteFakturaLogo clears the stored faktura logo.
func (h *ClubSettingsHandler) HandleDeleteFakturaLogo(w http.ResponseWriter, r *http.Request) {
	h.deleteLogo(w, r, "faktura_logo_data", "faktura_logo_mime", "delete_faktura_logo")
}

// HandleDeleteSiteLogo clears the stored site logo.
func (h *ClubSettingsHandler) HandleDeleteSiteLogo(w http.ResponseWriter, r *http.Request) {
	h.deleteLogo(w, r, "site_logo_data", "site_logo_mime", "delete_site_logo")
}

func (h *ClubSettingsHandler) deleteLogo(w http.ResponseWriter, r *http.Request, dataCol, mimeCol, action string) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE clubs SET `+dataCol+` = NULL, `+mimeCol+` = '' WHERE id = $1`,
		claims.ClubID,
	); err != nil {
		h.log.Error().Err(err).Msg("clear club logo")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID, action, "clubs", claims.ClubID, nil, nil); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit club logo delete")
	}
	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// sessionSecuritySettings holds per-club session and TOTP timeout overrides.
// Zero values mean "use the server default". Minutes are used for storage
// to keep the JSON human-readable in club_settings.value.
type sessionSecuritySettings struct {
	// IdleMinutes: how long a session stays valid without activity.
	// Default: 720 (12h). Clamp: [30, 43200].
	IdleMinutes int `json:"idle_minutes"`
	// CapMinutes: hard ceiling from session creation time.
	// Default: 10080 (7d). Clamp: [idle_minutes, 129600].
	CapMinutes int `json:"cap_minutes"`
	// AdminTOTPMinutes: how long the admin TOTP step-up gate stays open.
	// Default: 720 (12h). Clamp: [5, 1440].
	AdminTOTPMinutes int `json:"admin_totp_minutes"`
}

// HandleGetSessionSettings returns the current per-club session timeout settings.
// Unset values are returned as the server defaults.
func (h *ClubSettingsHandler) HandleGetSessionSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT key, (value::text)::int FROM club_settings
		 WHERE club_id = $1 AND key IN ('session_idle_minutes', 'session_absolute_cap_minutes', 'admin_totp_minutes')`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("load session settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	// Defaults matching code constants.
	out := sessionSecuritySettings{
		IdleMinutes:      int(auth.SessionIdleWindowDefault.Minutes()),
		CapMinutes:       int(auth.SessionAbsoluteCapDefault.Minutes()),
		AdminTOTPMinutes: int(auth.AdminTOTPWindowDefault.Minutes()),
	}
	for rows.Next() {
		var key string
		var minutes int
		if err := rows.Scan(&key, &minutes); err != nil {
			continue
		}
		switch key {
		case "session_idle_minutes":
			out.IdleMinutes = minutes
		case "session_absolute_cap_minutes":
			out.CapMinutes = minutes
		case "admin_totp_minutes":
			out.AdminTOTPMinutes = minutes
		}
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("iterate session settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, out)
}

// HandleUpdateSessionSettings persists per-club session timeout overrides.
// Must be called after RequireFreshTOTP — changing security policy is gated
// on recent TOTP proof.
func (h *ClubSettingsHandler) HandleUpdateSessionSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req sessionSecuritySettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Server-side clamp (defence-in-depth — save handler is the last gate).
	req.IdleMinutes = clampMinutes(req.IdleMinutes, 30, 43200)
	req.CapMinutes = clampMinutes(req.CapMinutes, req.IdleMinutes, 129600)
	req.AdminTOTPMinutes = clampMinutes(req.AdminTOTPMinutes, 5, 1440)
	if req.CapMinutes < req.IdleMinutes {
		Error(w, http.StatusBadRequest, "cap_minutes must be ≥ idle_minutes")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("begin session settings tx")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	for key, val := range map[string]int{
		"session_idle_minutes":         req.IdleMinutes,
		"session_absolute_cap_minutes": req.CapMinutes,
		"admin_totp_minutes":           req.AdminTOTPMinutes,
	} {
		if _, err := tx.Exec(ctx,
			`INSERT INTO club_settings (club_id, key, value, updated_at)
			 VALUES ($1, $2, to_jsonb($3::int), now())
			 ON CONFLICT (club_id, key) DO UPDATE SET value = to_jsonb($3::int), updated_at = now()`,
			claims.ClubID, key, val,
		); err != nil {
			h.log.Error().Err(err).Str("key", key).Msg("upsert session setting")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("commit session settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if auditErr := LogAudit(ctx, h.db, claims.ClubID, claims.UserID,
		"update_session_settings", "club_settings", claims.ClubID, nil, req); auditErr != nil {
		h.log.Error().Err(auditErr).Msg("audit session settings")
	}

	JSON(w, http.StatusOK, req)
}

type feedbackSettings struct {
	Enabled   bool   `json:"enabled"`
	HasAPIKey bool   `json:"has_api_key"`
	TeamID    string `json:"linear_team_id"`
	TriageID  string `json:"linear_triage_state_id"`
}

func (h *ClubSettingsHandler) HandleGetFeedbackSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var s feedbackSettings
	var apiKey string
	if err := h.db.QueryRow(ctx,
		`SELECT feature_feedback, feedback_linear_api_key,
		        feedback_linear_team_id, feedback_linear_triage_id
		 FROM clubs WHERE id = $1`,
		claims.ClubID,
	).Scan(&s.Enabled, &apiKey, &s.TeamID, &s.TriageID); err != nil {
		h.log.Error().Err(err).Msg("failed to get feedback settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.HasAPIKey = apiKey != ""
	JSON(w, http.StatusOK, s)
}

type updateFeedbackSettingsReq struct {
	Enabled  bool   `json:"enabled"`
	APIKey   string `json:"linear_api_key"`
	TeamID   string `json:"linear_team_id"`
	TriageID string `json:"linear_triage_state_id"`
}

func (h *ClubSettingsHandler) HandleUpdateFeedbackSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req updateFeedbackSettingsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Fetch the current API key so a blank submission doesn't wipe it.
	var currentAPIKey string
	if err := h.db.QueryRow(ctx,
		`SELECT feedback_linear_api_key FROM clubs WHERE id = $1`, claims.ClubID,
	).Scan(&currentAPIKey); err != nil {
		h.log.Error().Err(err).Msg("failed to fetch current feedback api key")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	newAPIKey := currentAPIKey
	if req.APIKey != "" {
		trimmed := strings.TrimSpace(req.APIKey)
		if !strings.HasPrefix(trimmed, "lin_api_") {
			Error(w, http.StatusBadRequest, "linear_api_key must start with lin_api_")
			return
		}
		newAPIKey = trimmed
	}

	if req.Enabled {
		if newAPIKey == "" || req.TeamID == "" || req.TriageID == "" {
			Error(w, http.StatusBadRequest, "linear_api_key, linear_team_id, and linear_triage_state_id are required to enable feedback")
			return
		}
	}

	if _, err := h.db.Exec(ctx,
		`UPDATE clubs
		 SET feature_feedback          = $2,
		     feedback_linear_api_key   = $3,
		     feedback_linear_team_id   = $4,
		     feedback_linear_triage_id = $5,
		     updated_at = now()
		 WHERE id = $1`,
		claims.ClubID, req.Enabled, newAPIKey, req.TeamID, req.TriageID,
	); err != nil {
		h.log.Error().Err(err).Msg("failed to update feedback settings")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info().Str("actor", claims.UserID).Bool("enabled", req.Enabled).Msg("feedback settings updated")
	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func clampMinutes(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
