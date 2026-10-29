package accounting

import (
	"regexp"
	"testing"
)

func TestProviderPayoutKind(t *testing.T) {
	stripe := providerPayouts{stripe: regexp.MustCompile(defaultStripePayoutDescriptor)}
	none := providerPayouts{}
	cases := []struct {
		name        string
		p           providerPayouts
		desc, party string
		cents       int64
		want        string
	}{
		{"vipps settlement", stripe, "Utb. 3000100 Vippsnr 548005", "", 160000, ProviderPayoutVipps},
		{"vipps without stripe use", none, "utb.3000100 vippsnr 548005", "", 160000, ProviderPayoutVipps},
		{"stripe in description", stripe, "STRIPE PAYMENTS", "", 5000, ProviderPayoutStripe},
		{"stripe in counterpart", stripe, "Overføring", "Stripe Technology Europe", 5000, ProviderPayoutStripe},
		{"outgoing stripe bill", stripe, "Stripe faktura", "", -5000, ""},
		{"club not on stripe", none, "STRIPE PAYMENTS", "", 5000, ""},
		{"ordinary deposit", stripe, "Kontingent", "Ola Testmann", 5000, ""},
	}
	for _, c := range cases {
		if got := c.p.kind(c.desc, c.party, c.cents); got != c.want {
			t.Errorf("%s: kind = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCompileStripeDescriptor(t *testing.T) {
	for _, d := range []string{"", "   ", "(stripe"} {
		if re := compileStripeDescriptor(d); re.String() != defaultStripePayoutDescriptor {
			t.Errorf("%q compiled to %q, want the default", d, re.String())
		}
	}
	if re := compileStripeDescriptor(`^PAYOUT\d+`); !re.MatchString("PAYOUT42") || re.MatchString("payout42") {
		t.Errorf("custom descriptor not used as written: %q", re.String())
	}
}

func TestStripeMembersWaitingReason(t *testing.T) {
	var m stripeMembers
	m.add("charge", StripeTxnUnbooked, 100)
	m.add("charge", StripeTxnUnbooked, 100)
	m.add("charge", StripeTxnNeedsReview, 100)
	m.add("fee", StripeTxnUnbooked, -5)
	m.add("refund", StripeTxnNeedsReview, -50)
	m.add("refund", StripeTxnUnbooked, -20)
	m.add("refund_failure", StripeTxnUnbooked, 20)
	m.add("dispute", StripeTxnNeedsReview, -30)
	m.add("other_adjustment", StripeTxnNeedsReview, 0)
	m.add("charge", StripeTxnBooked, 100)
	m.add("charge", StripeTxnDismissed, 100)
	want := "2 kortbetalinger venter på bokføring, 1 kortbetaling til gjennomgang, 1 Stripe-gebyr venter på bokføring, " +
		"2 refusjoner venter på bokføring, 1 refusjon til gjennomgang, 1 tvist venter (bokføres ikke automatisk ennå), " +
		"1 annen Stripe-transaksjon til gjennomgang"
	if got := m.waitingReason(); got != want {
		t.Errorf("reason = %q\nwant %q", got, want)
	}
	if m.count != 11 || m.netOre != 415 || !m.pendingBlocker {
		t.Errorf("members = %+v", m)
	}
}
