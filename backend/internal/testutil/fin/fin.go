// Package fin builds synthetic finance fixtures for integration tests: a club
// with an open fiscal period, members, invoices, checkout orders, bank rows
// and Vipps rows. Amounts are given in integer øre (cents) so tests don't
// carry float rounding into their setup.
//
// It deliberately does not import internal/accounting, so accounting's own
// package-internal tests can use it; pass the kontoplan seeder in with
// WithKontoplan. All data is synthetic — never real people, member names or
// account numbers.
package fin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// Kr converts øre to the NUMERIC kroner value the schema stores.
func Kr(cents int64) float64 { return float64(cents) / 100 }

// Club is a seeded club with an open fiscal period and a treasurer who acts
// as the creating/importing user for everything else the builders insert.
type Club struct {
	Pool      *pgxpool.Pool
	ID        string
	PeriodID  string
	Year      int
	Treasurer Member

	priceItems  map[string]string
	nextInvoice int
}

// Member is a seeded user in the club.
type Member struct {
	ID    string
	Email string
}

type clubConfig struct {
	year      int
	kontoplan func(ctx context.Context, clubID string) (int, error)
}

// ClubOption customises NewClub.
type ClubOption func(*clubConfig)

// WithYear sets the fiscal year of the open period (default 2026).
func WithYear(year int) ClubOption { return func(c *clubConfig) { c.year = year } }

// WithKontoplan seeds the club's chart of accounts, typically with
// accounting.Service.SeedKontoplan. Without it the club has no accounts.
func WithKontoplan(seed func(ctx context.Context, clubID string) (int, error)) ClubOption {
	return func(c *clubConfig) { c.kontoplan = seed }
}

// NewClub seeds a club, a treasurer, an open fiscal period for the year and
// a default drift bank account on 1920.
func NewClub(t *testing.T, pool *pgxpool.Pool, opts ...ClubOption) *Club {
	t.Helper()
	cfg := clubConfig{year: 2026}
	for _, o := range opts {
		o(&cfg)
	}
	ctx := context.Background()

	c := &Club{Pool: pool, Year: cfg.year, priceItems: map[string]string{}, nextInvoice: 1000}
	c.ID = testutil.SeedClub(t, pool)
	c.Treasurer = c.NewMember(t, "treasurer")

	if cfg.kontoplan != nil {
		if _, err := cfg.kontoplan(ctx, c.ID); err != nil {
			t.Fatalf("fin: seed kontoplan: %v", err)
		}
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'open') RETURNING id`,
		c.ID, cfg.year,
	).Scan(&c.PeriodID); err != nil {
		t.Fatalf("fin: fiscal period: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO club_bank_accounts (club_id, account_number, role, gl_code, is_default_for_invoices)
		 VALUES ($1, $2, 'drift', '1920', true)`,
		c.ID, "9999"+testutil.RandomHex(4),
	); err != nil {
		t.Fatalf("fin: bank account: %v", err)
	}
	return c
}

// NewMember seeds a user in the club with the given roles.
func (c *Club) NewMember(t *testing.T, roles ...string) Member {
	t.Helper()
	id, email := testutil.SeedUser(t, c.Pool, c.ID, roles)
	return Member{ID: id, Email: email}
}

// PriceItem returns the club's price item for a category, creating a flat
// one on first use.
func (c *Club) PriceItem(t *testing.T, category string) string {
	t.Helper()
	if id, ok := c.priceItems[category]; ok {
		return id
	}
	var id string
	if err := c.Pool.QueryRow(context.Background(),
		`INSERT INTO price_items (club_id, category, name, amount, unit, is_active)
		 VALUES ($1, $2, $3, 100, 'once', true) RETURNING id`,
		c.ID, category, "Synthetic "+category,
	).Scan(&id); err != nil {
		t.Fatalf("fin: price item %s: %v", category, err)
	}
	c.priceItems[category] = id
	return id
}

// Line is one invoice line. Set AccountCode for a custom line booked to its
// own account; otherwise the line links to the club's price item for
// Category (default slip_fee).
type Line struct {
	Category    string
	AccountCode string
	Cents       int64
}

// InvoiceOpts describes an invoice. Zero values mean: next free number,
// a generated KID, status 'open', sent, owned by a fresh member, issued
// 1 March of the club year, and one slip_fee line for the full amount.
type InvoiceOpts struct {
	Number    int
	KID       string
	Status    string
	Unsent    bool
	Member    *Member
	IssueDate time.Time
	// Cents is the total when Lines is empty; with Lines it is ignored and
	// the total is their sum.
	Cents     int64
	Lines     []Line
	VippsPaid bool
}

// Invoice is a seeded invoice.
type Invoice struct {
	ID         string
	Number     int
	KID        string
	TotalCents int64
	MemberID   string
}

// NewInvoice seeds an invoice and its lines.
func (c *Club) NewInvoice(t *testing.T, o InvoiceOpts) Invoice {
	t.Helper()
	ctx := context.Background()

	if o.Number == 0 {
		c.nextInvoice++
		o.Number = c.nextInvoice
	}
	if o.KID == "" {
		o.KID = fmt.Sprintf("%010d%02d", o.Number, len(c.ID)%97)
	}
	if o.Status == "" {
		o.Status = "open"
	}
	if o.Member == nil {
		m := c.NewMember(t, "member")
		o.Member = &m
	}
	if o.IssueDate.IsZero() {
		o.IssueDate = time.Date(c.Year, 3, 1, 0, 0, 0, 0, time.UTC)
	}
	lines := o.Lines
	if len(lines) == 0 {
		lines = []Line{{Category: "slip_fee", Cents: o.Cents}}
	}
	var total int64
	for _, l := range lines {
		total += l.Cents
	}

	var sentAt, vippsPaidAt *time.Time
	now := time.Now()
	if !o.Unsent {
		sentAt = &now
	}
	if o.VippsPaid {
		vippsPaidAt = &now
	}

	inv := Invoice{Number: o.Number, KID: o.KID, TotalCents: total, MemberID: o.Member.ID}
	if err := c.Pool.QueryRow(ctx,
		`INSERT INTO invoices (club_id, user_id, invoice_number, kid_number, issue_date, due_date,
		                       total_amount, status, sent_at, vipps_paid_at, fiscal_period_id)
		 VALUES ($1, $2, $3, $4, $5, $5::date + 14, $6, $7, $8, $9, $10) RETURNING id`,
		c.ID, o.Member.ID, o.Number, o.KID, o.IssueDate, Kr(total), o.Status, sentAt, vippsPaidAt, c.PeriodID,
	).Scan(&inv.ID); err != nil {
		t.Fatalf("fin: invoice %d: %v", o.Number, err)
	}

	for _, l := range lines {
		var err error
		if l.AccountCode != "" {
			_, err = c.Pool.Exec(ctx,
				`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, account_id)
				 VALUES ($1, 'Custom line', 1, $2, $2, (SELECT id FROM accounts WHERE club_id = $3 AND code = $4))`,
				inv.ID, Kr(l.Cents), c.ID, l.AccountCode,
			)
		} else {
			cat := l.Category
			if cat == "" {
				cat = "slip_fee"
			}
			_, err = c.Pool.Exec(ctx,
				`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, price_item_id)
				 VALUES ($1, $2, 1, $3, $3, $4)`,
				inv.ID, "Synthetic "+cat, Kr(l.Cents), c.PriceItem(t, cat),
			)
		}
		if err != nil {
			t.Fatalf("fin: invoice %d line: %v", o.Number, err)
		}
	}
	return inv
}

// Order is a checkout order settling one invoice.
type Order struct {
	ID             string
	Token          string
	VippsReference string
}

// NewOrderForInvoice seeds the durable checkout order (and its order_sources
// link) that a faktura pay link resolves to.
func (c *Club) NewOrderForInvoice(t *testing.T, inv Invoice) Order {
	t.Helper()
	ctx := context.Background()
	o := Order{Token: "tok-" + testutil.RandomHex(12), VippsReference: "ref-" + testutil.RandomHex(8)}
	if err := c.Pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, user_id, total_amount, token, vipps_reference)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		c.ID, inv.MemberID, Kr(inv.TotalCents), o.Token, o.VippsReference,
	).Scan(&o.ID); err != nil {
		t.Fatalf("fin: order: %v", err)
	}
	if _, err := c.Pool.Exec(ctx,
		`INSERT INTO order_sources (order_id, invoice_id) VALUES ($1, $2)`, o.ID, inv.ID,
	); err != nil {
		t.Fatalf("fin: order source: %v", err)
	}
	return o
}

// NewBankImport seeds a bank import owned by the treasurer and returns its ID.
func (c *Club) NewBankImport(t *testing.T) string {
	t.Helper()
	var id string
	if err := c.Pool.QueryRow(context.Background(),
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'synthetic.csv', 'sparebank-norge-v1', '1920', $2, 0) RETURNING id`,
		c.ID, c.Treasurer.ID,
	).Scan(&id); err != nil {
		t.Fatalf("fin: bank import: %v", err)
	}
	return id
}

// BankRowOpts describes an unbooked bank statement row.
type BankRowOpts struct {
	Date        time.Time
	Cents       int64
	KID         string
	Reference   string
	Description string
}

// NewBankRow seeds an unbooked row on a bank import and returns its ID. Use
// accounting.ImportBankRows instead when the test needs the import cascade.
func (c *Club) NewBankRow(t *testing.T, importID string, o BankRowOpts) string {
	t.Helper()
	if o.Date.IsZero() {
		o.Date = time.Date(c.Year, 6, 1, 0, 0, 0, 0, time.UTC)
	}
	if o.Reference == "" {
		o.Reference = "SYN" + testutil.RandomHex(5)
	}
	if o.Description == "" {
		o.Description = "Synthetic transfer"
	}
	var id string
	if err := c.Pool.QueryRow(context.Background(),
		`INSERT INTO bank_import_rows (bank_import_id, club_id, row_date, description, amount, reference, kid_number, row_hash)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		importID, c.ID, o.Date, o.Description, Kr(o.Cents), o.Reference, o.KID, testutil.RandomHex(32),
	).Scan(&id); err != nil {
		t.Fatalf("fin: bank row: %v", err)
	}
	return id
}

// VippsRowOpts describes a Vipps settlement report row.
type VippsRowOpts struct {
	RowType    string // belastning (default), fee, payout
	Cents      int64
	Date       time.Time
	OrderID    string // the order's vipps_reference for ePayment charges
	PSPRef     string
	Settlement string
	MSN        string
}

// NewVippsRow seeds a Vipps import (once per call) with one row and returns
// the row ID.
func (c *Club) NewVippsRow(t *testing.T, o VippsRowOpts) string {
	t.Helper()
	ctx := context.Background()
	if o.RowType == "" {
		o.RowType = "belastning"
	}
	if o.Date.IsZero() {
		o.Date = time.Date(c.Year, 6, 1, 0, 0, 0, 0, time.UTC)
	}
	if o.MSN == "" {
		o.MSN = "000001"
	}
	if o.PSPRef == "" {
		o.PSPRef = "psp-" + testutil.RandomHex(6)
	}
	var importID string
	if err := c.Pool.QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by)
		 VALUES ($1, 'synthetic-vipps.csv', $2, $3) RETURNING id`,
		c.ID, o.MSN, c.Treasurer.ID,
	).Scan(&importID); err != nil {
		t.Fatalf("fin: vipps import: %v", err)
	}
	var id string
	if err := c.Pool.QueryRow(ctx,
		`INSERT INTO vipps_import_rows (vipps_import_id, club_id, row_hash, row_type, booking_date,
		                                amount, psp_ref, order_id, settlement_number, msn)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		importID, c.ID, testutil.RandomHex(32), o.RowType, o.Date, Kr(o.Cents),
		o.PSPRef, o.OrderID, o.Settlement, o.MSN,
	).Scan(&id); err != nil {
		t.Fatalf("fin: vipps row: %v", err)
	}
	return id
}
