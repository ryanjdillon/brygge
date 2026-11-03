package sesevents

import "testing"

func TestStatusFor(t *testing.T) {
	tests := []struct {
		name string
		rec  Record
		want string
		rank int
	}{
		{"send", Record{EventType: EventSend}, StatusQueued, 1},
		{"delay", Record{EventType: EventDeliveryDelay}, StatusDeferred, 2},
		{"delivery", Record{EventType: EventDelivery}, StatusAccepted, 3},
		{"transient bounce", Record{EventType: EventBounce, BounceType: "Transient", BounceSubType: "MailboxFull"}, StatusRejectedTemporary, 3},
		{"permanent bounce", Record{EventType: EventBounce, BounceType: "Permanent", BounceSubType: "General"}, StatusRejected, 4},
		{"account suppression", Record{EventType: EventBounce, BounceType: "Permanent", BounceSubType: "OnAccountSuppressionList"}, StatusSuppressed, 4},
		{"tenant suppression", Record{EventType: EventBounce, BounceType: "Permanent", BounceSubType: "OnTenantSuppressionList"}, StatusSuppressed, 4},
		{"undetermined bounce", Record{EventType: EventBounce, BounceType: "Undetermined", BounceSubType: "Undetermined"}, StatusRejected, 4},
		{"reject", Record{EventType: EventReject}, StatusRejected, 4},
		{"rendering failure", Record{EventType: EventRenderingFailure}, StatusRejected, 4},
		{"complaint", Record{EventType: EventComplaint}, StatusComplaint, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StatusFor(tt.rec)
			if got != tt.want {
				t.Fatalf("StatusFor = %q, want %q", got, tt.want)
			}
			if r := Rank(got); r != tt.rank {
				t.Fatalf("Rank(%q) = %d, want %d", got, r, tt.rank)
			}
		})
	}
}

func TestShouldAdvance(t *testing.T) {
	tests := []struct {
		cur, next string
		want      bool
	}{
		{"sending", StatusQueued, true},
		{"submitted", StatusAccepted, true},
		{"unknown", StatusDeferred, true},
		{"failed", StatusQueued, true},
		{StatusQueued, StatusAccepted, true},
		// A Send event arriving after the Delivery it preceded.
		{StatusAccepted, StatusQueued, false},
		{StatusAccepted, StatusDeferred, false},
		// Same rank: the later of the two outcomes wins.
		{StatusRejectedTemporary, StatusAccepted, true},
		{StatusAccepted, StatusRejectedTemporary, true},
		{StatusAccepted, StatusRejected, true},
		{StatusSuppressed, StatusAccepted, false},
		{StatusRejected, StatusComplaint, true},
		{StatusComplaint, StatusRejected, false},
	}
	for _, tt := range tests {
		if got := ShouldAdvance(tt.cur, tt.next); got != tt.want {
			t.Errorf("ShouldAdvance(%q, %q) = %v, want %v", tt.cur, tt.next, got, tt.want)
		}
	}
}

func TestEventStatusesAllRanked(t *testing.T) {
	for _, s := range EventStatuses {
		if Rank(s) == 0 {
			t.Errorf("status %q has no rank", s)
		}
	}
}

func TestDomainClass(t *testing.T) {
	tests := map[string]string{
		"gmail.com":        DomainGmail,
		"GMAIL.COM":        DomainGmail,
		"googlemail.com":   DomainGmail,
		"outlook.com":      DomainOutlook,
		"outlook.no":       DomainOutlook,
		"hotmail.com":      DomainOutlook,
		"hotmail.co.uk":    DomainOutlook,
		"live.no":          DomainOutlook,
		"live.com.au":      DomainOutlook,
		"msn.com":          DomainOutlook,
		"yahoo.com":        DomainYahoo,
		"yahoo.no":         DomainYahoo,
		"yahoo.co.jp":      DomainYahoo,
		"ymail.com":        DomainYahoo,
		"icloud.com":       DomainICloud,
		"me.com":           DomainICloud,
		"mac.com":          DomainICloud,
		"example.net":      DomainOther,
		"live.example.org": DomainOther,
		"mail.gmail.com":   DomainOther,
		"gmail":            DomainOther,
		"":                 DomainOther,
	}
	for in, want := range tests {
		if got := DomainClass(in); got != want {
			t.Errorf("DomainClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormaliseKind(t *testing.T) {
	for _, k := range Kinds {
		if got := normaliseKind(k); got != k {
			t.Errorf("normaliseKind(%q) = %q", k, got)
		}
	}
	for _, k := range []string{"", "newsletter", "INVOICE"} {
		if got := normaliseKind(k); got != "other" {
			t.Errorf("normaliseKind(%q) = %q, want other", k, got)
		}
	}
}
