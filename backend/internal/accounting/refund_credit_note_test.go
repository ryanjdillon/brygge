package accounting

import (
	"errors"
	"slices"
	"testing"
)

func strp(s string) *string { return &s }

// An exemption folds into the item line it offsets (same price item and
// boat); a fully exempt pair drops out, and other lines are kept.
func TestNetExemptions(t *testing.T) {
	item, slip, boat := strp("pi-member"), strp("pi-slip"), strp("boat-1")
	lines := []documentLine{
		{description: "Medlemskap", amountOre: 30000, priceItemID: item},
		{description: "Fritak", amountOre: -15000, priceItemID: slip, boatID: boat, exemptionRole: strp("chair")},
		{description: "Båtplass", amountOre: 15000, priceItemID: slip, boatID: boat},
		{description: "Rabatt", amountOre: -5000},
	}
	got := netExemptions(lines)
	var descs []string
	var sum int64
	for _, l := range got {
		descs = append(descs, l.description)
		sum += l.amountOre
	}
	if !slices.Equal(descs, []string{"Medlemskap", "Rabatt"}) || sum != 25000 {
		t.Errorf("netted = %v (sum %d), want the membership and the discount", descs, sum)
	}
}

// A partial exemption leaves the item's net, and an exemption with no item
// to fold into is kept as it is.
func TestNetExemptionsPartial(t *testing.T) {
	slip := strp("pi-slip")
	got := netExemptions([]documentLine{
		{description: "Båtplass", amountOre: 20000, priceItemID: slip},
		{description: "Fritak", amountOre: -5000, priceItemID: slip, exemptionRole: strp("chair")},
		{description: "Fritak uten vare", amountOre: -1000, priceItemID: strp("pi-other"), exemptionRole: strp("chair")},
	})
	if len(got) != 2 || got[0].amountOre != 15000 || got[1].amountOre != -1000 {
		t.Errorf("netted = %+v, want 15000 and the lone exemption", got)
	}
}

// Scaling truncates each share and puts the remainder on the largest line,
// so the parts always sum to the amount.
func TestScaleLines(t *testing.T) {
	lines := []documentLine{{amountOre: 30000}, {amountOre: 15000}, {amountOre: 100}}
	got, err := scaleLines(lines, 9001)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, v := range got {
		sum += v
	}
	if sum != 9001 || got[0] != 5989 || got[1] != 2993 || got[2] != 19 {
		t.Errorf("scaled = %v (sum %d), want [5989 2993 19]", got, sum)
	}
	if _, err := scaleLines([]documentLine{{amountOre: 0}}, 100); !errors.Is(err, errRefundNotScalable) {
		t.Errorf("zero lines: err = %v, want errRefundNotScalable", err)
	}
}

// A partial refund is divided by share, the remainder on the last target;
// the refund that completes the charge gives each what it has left, so
// partials add up to each faktura's total exactly.
func TestAllocateRefund(t *testing.T) {
	targets := func(credited ...int64) []*refundTarget {
		out := []*refundTarget{{shareOre: 30000}, {shareOre: 15000}}
		for i, c := range credited {
			out[i].creditedOre = c
		}
		return out
	}
	tests := []struct {
		name    string
		targets []*refundTarget
		refund  int64
		full    bool
		want    []int64
		ok      bool
	}{
		{"partial", targets(), 10001, false, []int64{6667, 3334}, true},
		{"completes after a partial", targets(6667, 3334), 34999, true, []int64{23333, 11666}, true},
		{"full at once", targets(), 45000, true, []int64{30000, 15000}, true},
		{"completes with a mismatch", targets(6667, 3334), 30000, true, nil, false},
		{"partial past a target's rest", targets(30000, 0), 10000, false, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := allocateRefund(tt.targets, tt.refund, tt.full)
			if ok != tt.ok || !slices.Equal(got, tt.want) {
				t.Errorf("allocateRefund = %v, %v; want %v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// The targets must add up to the charge, and none may be voided or
// credited by hand since.
func TestRefundTargetsSplittable(t *testing.T) {
	ok := []*refundTarget{{shareOre: 30000, status: "open"}, {shareOre: 15000, status: "open"}}
	if !refundTargetsSplittable(ok, 45000) {
		t.Error("matching targets refused")
	}
	if refundTargetsSplittable(ok, 46000) {
		t.Error("targets short of the charge accepted")
	}
	voided := []*refundTarget{{shareOre: 45000, status: "voided"}}
	if refundTargetsSplittable(voided, 45000) {
		t.Error("voided faktura accepted")
	}
	credited := []*refundTarget{{shareOre: 45000, status: "open", manuallyCredited: true}}
	if refundTargetsSplittable(credited, 45000) || refundTargetsSplittable(nil, 0) {
		t.Error("credited or empty targets accepted")
	}
}
