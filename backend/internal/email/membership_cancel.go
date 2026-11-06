package email

import (
	"fmt"
	"time"
)

// --- Membership cancellation emails (BRY-633) ---
//
// Cancelling online sends the member a receipt and tells the board; undoing
// it tells the board too, and so does the year-end deactivation when it is
// refused because the member is the club's only active admin. Like the
// settlement emails they are written in Bokmål or Nynorsk by the club's
// default language (JoinLocale), and a member's name or reason reaches the
// board only through html/template's escaping.
//
// The receipt is the member's written notice of the cancellation (the
// statutes ask for one), so it states the end date and that nothing paid is
// refunded.

var membershipCancelReceiptCopy = map[string]membershipCopyT{
	"nb": {
		Subject:      "Utmelding bekreftet",
		Greeting:     "Hei %s,",
		Body:         "Vi har mottatt utmeldingen din fra %s. Medlemskapet varer ut %s og avsluttes da. Kontingent som er betalt, refunderes ikke. Denne e-posten er den skriftlige bekreftelsen på utmeldingen.",
		Next:         "Har du båtplass eller moloandel, tar styret kontakt om det. Ombestemmer du deg, kan du angre utmeldingen i medlemsportalen fram til og med %s.",
		CTA:          "Gå til medlemsportalen",
		FallbackClub: "klubben",
	},
	"nn": {
		Subject:      "Utmelding stadfesta",
		Greeting:     "Hei %s,",
		Body:         "Vi har motteke utmeldinga di frå %s. Medlemskapen varer ut %s og blir avslutta då. Kontingent som er betalt, blir ikkje refundert. Denne e-posten er den skriftlege stadfestinga på utmeldinga.",
		Next:         "Har du båtplass eller moloandel, tek styret kontakt om det. Ombestemmer du deg, kan du angre utmeldinga i medlemsportalen fram til og med %s.",
		CTA:          "Gå til medlemsportalen",
		FallbackClub: "klubben",
	},
}

// membershipCancelWaitingListCopy is the receipt's warning to a member on the
// waiting list.
var membershipCancelWaitingListCopy = map[string]string{
	"nb": "Plassen din på ventelisten faller bort når medlemskapet avsluttes.",
	"nn": "Plassen din på ventelista fell bort når medlemskapen blir avslutta.",
}

var membershipCancelBoardCopy = map[string]membershipCopyT{
	"nb": {
		Subject:      "Utmelding: %s (til og med %s)",
		Body:         "%s har meldt seg ut. Medlemskapet avsluttes %s, og medlemmet deaktiveres automatisk dagen etter.",
		Next:         "Har medlemmet båtplass, moloandel eller et verv, må styret følge opp det.",
		CTA:          "Åpne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "Et medlem",
	},
	"nn": {
		Subject:      "Utmelding: %s (til og med %s)",
		Body:         "%s har meldt seg ut. Medlemskapen blir avslutta %s, og medlemmet blir deaktivert automatisk dagen etter.",
		Next:         "Har medlemmet båtplass, moloandel eller eit verv, må styret følgje opp det.",
		CTA:          "Opne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "Eit medlem",
	},
}

// membershipCancelReasonCopy introduces the member's own reason, when given.
var membershipCancelReasonCopy = map[string]string{
	"nb": "Begrunnelse fra medlemmet: %s",
	"nn": "Grunngjeving frå medlemmet: %s",
}

var membershipCancelUndoneCopy = map[string]membershipCopyT{
	"nb": {
		Subject:      "Utmelding angret: %s",
		Body:         "%s har angret utmeldingen og fortsetter som medlem.",
		Next:         "Dette er bare til orientering.",
		CTA:          "Åpne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "Et medlem",
	},
	"nn": {
		Subject:      "Utmelding angra: %s",
		Body:         "%s har angra utmeldinga og held fram som medlem.",
		Next:         "Dette er berre til orientering.",
		CTA:          "Opne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "Eit medlem",
	},
}

var membershipEndRefusedCopy = map[string]membershipCopyT{
	"nb": {
		Subject:      "Utmelding må behandles: %s",
		Body:         "Medlemskapet til %s skulle avsluttes %s, men medlemmet er klubbens eneste aktive administrator og ble derfor ikke deaktivert automatisk.",
		Next:         "Gjør et annet medlem til administrator; da deaktiveres medlemmet ved neste daglige kjøring.",
		CTA:          "Åpne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "et medlem",
	},
	"nn": {
		Subject:      "Utmelding må handsamast: %s",
		Body:         "Medlemskapen til %s skulle avsluttast %s, men medlemmet er den einaste aktive administratoren i klubben og vart difor ikkje deaktivert automatisk.",
		Next:         "Gjer eit anna medlem til administrator; då blir medlemmet deaktivert ved neste daglege køyring.",
		CTA:          "Opne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "eit medlem",
	},
}

// norDate is a date as Norwegians write it, 31.12.2026.
func norDate(d time.Time) string { return d.Format("02.01.2006") }

// MembershipCancelReceiptSubject is the Subject of the member's receipt for
// an online cancellation.
func MembershipCancelReceiptSubject(locale string) string {
	return membershipCopy(membershipCancelReceiptCopy, locale).Subject
}

// MembershipCancelReceiptBody is its HTML body: endsOn is the last day of
// membership, onWaitingList whether to warn that the waiting-list place is
// lost, and portalURL the page where the cancellation can be undone.
func MembershipCancelReceiptBody(locale, clubName, firstName string, endsOn time.Time, onWaitingList bool, portalURL string) string {
	c := membershipCopy(membershipCancelReceiptCopy, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	greeting := "Hei,"
	if firstName != "" {
		greeting = fmt.Sprintf(c.Greeting, firstName)
	}
	date := norDate(endsOn)
	next := fmt.Sprintf(c.Next, date)
	if onWaitingList {
		wl, ok := membershipCancelWaitingListCopy[locale]
		if !ok {
			wl = membershipCancelWaitingListCopy[defaultLocale]
		}
		next = wl + " " + next
	}
	return renderMembership(membershipData{
		Club: clubName, Greeting: greeting,
		Body: fmt.Sprintf(c.Body, clubName, date), Next: next,
		URL: portalURL, CTA: c.CTA,
	})
}

// MembershipCancelBoardSubject is the Subject of the board's notice of an
// online cancellation, "Utmelding: <name> (til og med 31.12)", naming the
// member's last day.
func MembershipCancelBoardSubject(locale, memberName string, endsOn time.Time) string {
	c := membershipCopy(membershipCancelBoardCopy, locale)
	if memberName == "" {
		memberName = c.FallbackName
	}
	return fmt.Sprintf(c.Subject, memberName, endsOn.Format("02.01"))
}

// MembershipCancelBoardBody is its HTML body; reason is the member's own
// text, "" when none was given, and memberURL opens the member in the admin
// member list.
func MembershipCancelBoardBody(locale, clubName, memberName, reason string, endsOn time.Time, memberURL string) string {
	c := membershipCopy(membershipCancelBoardCopy, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	if memberName == "" {
		memberName = c.FallbackName
	}
	next := c.Next
	if reason != "" {
		intro, ok := membershipCancelReasonCopy[locale]
		if !ok {
			intro = membershipCancelReasonCopy[defaultLocale]
		}
		next += " " + fmt.Sprintf(intro, reason)
	}
	return renderMembership(membershipData{
		Club: clubName, Body: fmt.Sprintf(c.Body, memberName, norDate(endsOn)), Next: next,
		URL: memberURL, CTA: c.CTA,
	})
}

// MembershipCancelUndoneSubject is the Subject of the board's notice that a
// member undid their cancellation.
func MembershipCancelUndoneSubject(locale, memberName string) string {
	return boardSubject(membershipCancelUndoneCopy, locale, memberName)
}

// MembershipCancelUndoneBody is its HTML body.
func MembershipCancelUndoneBody(locale, clubName, memberName, memberURL string) string {
	return boardBody(membershipCancelUndoneCopy, locale, clubName, memberName, memberURL)
}

// MembershipEndRefusedSubject is the Subject of the board's notice that a
// cancelled membership reached its end but the member, the club's only
// active admin, was not deactivated.
func MembershipEndRefusedSubject(locale, memberName string) string {
	return boardSubject(membershipEndRefusedCopy, locale, memberName)
}

// MembershipEndRefusedBody is its HTML body; endsOn is the membership's last
// day.
func MembershipEndRefusedBody(locale, clubName, memberName string, endsOn time.Time, memberURL string) string {
	c := membershipCopy(membershipEndRefusedCopy, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	if memberName == "" {
		memberName = c.FallbackName
	}
	return renderMembership(membershipData{
		Club: clubName, Body: fmt.Sprintf(c.Body, memberName, norDate(endsOn)), Next: c.Next,
		URL: memberURL, CTA: c.CTA,
	})
}
