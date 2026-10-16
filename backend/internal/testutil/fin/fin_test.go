package fin_test

import (
	"context"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func TestBuildersProduceLinkedRows(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	club := fin.NewClub(t, pool)
	inv := club.NewInvoice(t, fin.InvoiceOpts{Lines: []fin.Line{
		{Category: "membership", Cents: 45000},
		{Category: "slip_fee", Cents: 260050},
	}})
	if inv.TotalCents != 305050 {
		t.Fatalf("total = %d, want 305050", inv.TotalCents)
	}
	var total float64
	var lines int
	if err := pool.QueryRow(ctx,
		`SELECT i.total_amount, (SELECT count(*) FROM invoice_lines WHERE invoice_id = i.id)
		   FROM invoices i WHERE i.id = $1`, inv.ID,
	).Scan(&total, &lines); err != nil || total != 3050.50 || lines != 2 {
		t.Fatalf("invoice row = %v kr / %d lines (err %v), want 3050.50 / 2", total, lines, err)
	}

	order := club.NewOrderForInvoice(t, inv)
	var linked string
	if err := pool.QueryRow(ctx,
		`SELECT os.invoice_id FROM order_sources os JOIN orders o ON o.id = os.order_id
		  WHERE o.vipps_reference = $1`, order.VippsReference,
	).Scan(&linked); err != nil || linked != inv.ID {
		t.Fatalf("order source = %q (err %v), want invoice %s", linked, err, inv.ID)
	}

	row := club.NewBankRow(t, club.NewBankImport(t), fin.BankRowOpts{Cents: 45000, KID: inv.KID})
	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 305050, OrderID: order.VippsReference})
	for name, q := range map[string]string{
		"bank row":  `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND amount = 450`,
		"vipps row": `SELECT count(*) FROM vipps_import_rows WHERE id = $1 AND amount = 3050.50`,
	} {
		id := row
		if name == "vipps row" {
			id = charge
		}
		var n int
		if err := pool.QueryRow(ctx, q, id).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s not stored as expected (n=%d, err %v)", name, n, err)
		}
	}
}
