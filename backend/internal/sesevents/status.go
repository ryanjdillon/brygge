package sesevents

import "strings"

// email_sends.status values set from delivery events. The pre-event
// statuses (sending, submitted, failed, unknown) are written by the sender.
const (
	StatusQueued            = "queued"
	StatusDeferred          = "deferred"
	StatusAccepted          = "accepted"
	StatusRejectedTemporary = "rejected_temporary"
	StatusRejected          = "rejected"
	StatusSuppressed        = "suppressed"
	StatusComplaint         = "complaint"
)

// EventStatuses lists every status an event can map to; it is the closed
// value set of the result attribute on brygge.email.delivery.
var EventStatuses = []string{
	StatusQueued, StatusDeferred, StatusAccepted, StatusRejectedTemporary,
	StatusRejected, StatusSuppressed, StatusComplaint,
}

// statusRank orders statuses so events arriving out of order never move a
// send backwards: SNS→SQS gives no ordering guarantee, so a Send event can
// land after the Delivery it preceded. Statuses not listed (the sender's
// pre-event states) rank 0.
var statusRank = map[string]int{
	StatusQueued:            1,
	StatusDeferred:          2,
	StatusAccepted:          3,
	StatusRejectedTemporary: 3,
	StatusRejected:          4,
	StatusSuppressed:        4,
	StatusComplaint:         5,
}

// Rank returns the precedence of status; unknown and pre-event statuses
// rank 0.
func Rank(status string) int { return statusRank[status] }

// ShouldAdvance reports whether a send currently in status cur moves to
// next. Equal ranks advance so the latest of two same-rank outcomes (a
// transient bounce followed by the eventual delivery) wins.
func ShouldAdvance(cur, next string) bool {
	return Rank(next) >= Rank(cur)
}

// StatusFor maps a record to the email_sends status it implies.
// Bounce types and subtypes:
// https://docs.aws.amazon.com/ses/latest/dg/event-publishing-retrieving-sns-contents.html
func StatusFor(r Record) string {
	switch r.EventType {
	case EventSend:
		return StatusQueued
	case EventDeliveryDelay:
		return StatusDeferred
	case EventDelivery:
		return StatusAccepted
	case EventBounce:
		switch r.BounceType {
		case "Transient":
			return StatusRejectedTemporary
		case "Permanent":
			switch r.BounceSubType {
			case "OnAccountSuppressionList", "OnTenantSuppressionList":
				return StatusSuppressed
			}
			return StatusRejected
		}
		// Undetermined: SES could not classify it, but the message was
		// not delivered, so it surfaces as a hard failure for review.
		return StatusRejected
	case EventReject, EventRenderingFailure:
		return StatusRejected
	case EventComplaint:
		switch r.ComplaintSubType {
		case "OnAccountSuppressionList", "OnTenantSuppressionList":
			return StatusSuppressed
		}
		return StatusComplaint
	}
	return ""
}

// Kind values the metric accepts. They mirror the email_sends.kind
// vocabulary; anything else counts as "other" so a stray tag cannot grow
// the metric's cardinality.
var Kinds = []string{
	"magic_link", "invoice", "invoice_reminder", "credit_note",
	"dispute", "contact", "join_confirm", "join_already_member",
	"join_contact_board", "membership_welcome", "membership_board_notice",
	"membership_join_refunded", "membership_grant_withheld", "other",
}

func normaliseKind(k string) string {
	for _, known := range Kinds {
		if k == known {
			return k
		}
	}
	return "other"
}

// Recipient domain classes for brygge.email.delivery. Raw domains would
// identify members and make the series count unbounded.
const (
	DomainGmail   = "gmail"
	DomainOutlook = "outlook"
	DomainYahoo   = "yahoo"
	DomainICloud  = "icloud"
	DomainOther   = "other"
)

// DomainClasses is the closed recipient_domain_class value set.
var DomainClasses = []string{DomainGmail, DomainOutlook, DomainYahoo, DomainICloud, DomainOther}

var exactDomains = map[string]string{
	"gmail.com":      DomainGmail,
	"googlemail.com": DomainGmail,
	"msn.com":        DomainOutlook,
	"ymail.com":      DomainYahoo,
	"rocketmail.com": DomainYahoo,
	"icloud.com":     DomainICloud,
	"me.com":         DomainICloud,
	"mac.com":        DomainICloud,
}

// Providers that register their brand under many country TLDs
// (hotmail.no, outlook.co.uk, yahoo.com.au).
var brandDomains = map[string]string{
	"outlook": DomainOutlook,
	"hotmail": DomainOutlook,
	"live":    DomainOutlook,
	"yahoo":   DomainYahoo,
}

// DomainClass buckets a recipient domain into a mailbox provider.
func DomainClass(domain string) string {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if c, ok := exactDomains[d]; ok {
		return c
	}
	brand, suffix, ok := strings.Cut(d, ".")
	if !ok {
		return DomainOther
	}
	c, ok := brandDomains[brand]
	if !ok || !looksLikePublicSuffix(suffix) {
		return DomainOther
	}
	return c
}

// looksLikePublicSuffix accepts a country or generic TLD ("no", "com") or
// a two-level registry suffix ("co.uk", "com.au"), so live.no matches but
// live.example.org does not.
func looksLikePublicSuffix(s string) bool {
	labels := strings.Split(s, ".")
	switch len(labels) {
	case 1:
		return labels[0] != ""
	case 2:
		switch labels[0] {
		case "co", "com", "ne", "net", "org":
			return labels[1] != "" && len(labels[1]) <= 3
		}
	}
	return false
}
