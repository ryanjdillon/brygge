package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// A member who cancelled is billed dues for the year they cancelled in, and
// skipped with a coded reason for a period that starts after their
// membership ends; other members of the same run are billed as usual.
func TestBulkInvoicesSkipMembershipEnded(t *testing.T) {
	f := newExemptionFixture(t)
	ctx := context.Background()
	leaving := f.club.NewMember(t, "member")
	staying := f.club.NewMember(t, "member")
	mustExec(t, f.club.Pool, ctx,
		`UPDATE users SET membership_cancelled_at = now(), membership_ends_on = '2026-12-31' WHERE id = $1`, leaving.ID)

	current := f.bulk(t, bulkInvoiceRequest{UserIDs: []string{leaving.ID, staying.ID}, PriceItemIDs: []string{f.dues}})
	if len(current.Created) != 2 || len(current.Skipped) != 0 {
		t.Fatalf("2026 run: created %d, skipped %+v; want both billed", len(current.Created), current.Skipped)
	}

	var next string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2027, '2027-01-01', '2027-12-31', 'open') RETURNING id`, f.club.ID).Scan(&next); err != nil {
		t.Fatalf("next period: %v", err)
	}
	for _, preview := range []bool{true, false} {
		req := bulkInvoiceRequest{
			UserIDs: []string{leaving.ID, staying.ID}, PriceItemIDs: []string{f.dues},
			FiscalPeriodID: next, DueDate: "2027-03-31", Preview: preview,
		}
		rec := f.call(t, f.h.HandleBulkCreateInvoices, http.MethodPost, "/invoices/bulk", req)
		if rec.Code != http.StatusOK {
			t.Fatalf("2027 run (preview %v): %d %s", preview, rec.Code, rec.Body)
		}
		var out bulkInvoiceResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Skipped) != 1 || out.Skipped[0].UserID != leaving.ID ||
			out.Skipped[0].Code != skipMembershipEnded || out.Skipped[0].EndsOn != "2026-12-31" {
			t.Errorf("2027 run (preview %v) skipped %+v, want the cancelled member with code %s", preview, out.Skipped, skipMembershipEnded)
		}
		if !preview && (len(out.Created) != 1 || out.Created[0].UserID != staying.ID) {
			t.Errorf("2027 run (preview %v) created %+v, want the staying member only", preview, out.Created)
		}
	}
}
