package email

import (
	"strings"
	"testing"
	"time"
)

func TestDisputeSubject(t *testing.T) {
	due := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	if got, want := DisputeSubject(&due), "Omstridt kortbetaling – svar innen "+due.In(time.Local).Format("02.01.2006"); got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
	if got := DisputeSubject(nil); got != "Omstridt kortbetaling" {
		t.Errorf("subject without a deadline = %q", got)
	}
}

func TestDisputeBody(t *testing.T) {
	due := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	amount := int64(123456)
	body := DisputeBody(DisputeNotice{
		ClubName: "Synthetic <Båtlag>", AmountOre: &amount, Reference: "faktura 701, 702", Reason: "product_not_received",
		EvidenceDueBy: &due, DashboardURL: "https://dashboard.stripe.com/test/disputes/dp_1?a=1&b=2",
	})
	for _, want := range []string{
		"Synthetic &lt;Båtlag&gt;", "kr 1 234,56", "faktura 701, 702", "varen ikke mottatt",
		due.In(time.Local).Format("02.01.2006"),
		`href="https://dashboard.stripe.com/test/disputes/dp_1?a=1&amp;b=2"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<Båtlag>") {
		t.Errorf("club name not escaped:\n%s", body)
	}

	bare := DisputeBody(DisputeNotice{Reason: "<script>"})
	for _, absent := range []string{"Beløp", "Gjelder", "Frist", "href"} {
		if strings.Contains(bare, absent) {
			t.Errorf("a notice without %s still mentions it:\n%s", absent, bare)
		}
	}
	if !strings.Contains(bare, "klubben") || !strings.Contains(bare, "&lt;script&gt;") {
		t.Errorf("bare notice = %s", bare)
	}
}
