package email

import (
	"bytes"
	"fmt"
	"html/template"
)

// --- Membership settlement emails (BRY-630) ---
//
// Settling a join payment sends the new member a welcome and tells the board;
// a refunded join payment, or a paid join whose grant was withheld, tells the
// board too. All three are written in
// Bokmål or Nynorsk by the club's default language (JoinLocale), like the join
// emails. The member's name reaches the board emails only through
// html/template's escaping.

type membershipCopyT struct {
	Subject      string // fmt string with %s for the club name, or the member's name
	Greeting     string // fmt string with %s for the first name (welcome only)
	Body         string // fmt string with %s for the club name, or the member's name
	Next         string
	CTA          string
	Secondary    string
	FallbackClub string
	FallbackName string
}

var membershipWelcomeCopy = map[string]membershipCopyT{
	"nb": {
		Subject:      "Velkommen som medlem i %s",
		Greeting:     "Hei %s,",
		Body:         "Betalingen er mottatt, og du er nå medlem i %s. Velkommen!",
		Next:         "I medlemsportalen finner du profilen din, fakturaer og nyheter fra klubben. Ønsker du båtplass, kan du sette deg på ventelisten der.",
		CTA:          "Gå til medlemsportalen",
		Secondary:    "Sett deg på ventelisten",
		FallbackClub: "klubben",
	},
	"nn": {
		Subject:      "Velkomen som medlem i %s",
		Greeting:     "Hei %s,",
		Body:         "Betalinga er motteken, og du er no medlem i %s. Velkomen!",
		Next:         "I medlemsportalen finn du profilen din, fakturaer og nytt frå klubben. Ønskjer du båtplass, kan du setje deg på ventelista der.",
		CTA:          "Gå til medlemsportalen",
		Secondary:    "Set deg på ventelista",
		FallbackClub: "klubben",
	},
}

var membershipBoardNoticeCopy = map[string]membershipCopyT{
	"nb": {
		Subject:      "Nytt medlem: %s",
		Body:         "%s har betalt innmeldingen og er nå medlem.",
		Next:         "Dette er bare til orientering; innmeldingen trenger ikke godkjenning.",
		CTA:          "Åpne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "Et nytt medlem",
	},
	"nn": {
		Subject:      "Nytt medlem: %s",
		Body:         "%s har betalt innmeldinga og er no medlem.",
		Next:         "Dette er berre til orientering; innmeldinga treng ikkje godkjenning.",
		CTA:          "Opne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "Eit nytt medlem",
	},
}

var membershipJoinRefundedCopy = map[string]membershipCopyT{
	"nb": {
		Subject:      "Innmeldingsbetaling refundert: %s",
		Body:         "Innmeldingsbetalingen fra %s er refundert eller kreditert.",
		Next:         "Medlemskapet er ikke trukket tilbake automatisk. Vurder om medlemmet skal deaktiveres.",
		CTA:          "Åpne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "et medlem",
	},
	"nn": {
		Subject:      "Innmeldingsbetaling refundert: %s",
		Body:         "Innmeldingsbetalinga frå %s er refundert eller kreditert.",
		Next:         "Medlemskapen er ikkje trekt tilbake automatisk. Vurder om medlemmet skal deaktiverast.",
		CTA:          "Opne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "eit medlem",
	},
}

var membershipGrantWithheldCopy = map[string]membershipCopyT{
	"nb": {
		Subject:      "Innmelding må kontrolleres: %s",
		Body:         "%s har betalt innmeldingen, men medlemskapet ble ikke gitt automatisk fordi betalingen ikke stemte med innmeldingen.",
		Next:         "Kontroller betalingen, og gi medlemskap manuelt hvis den er i orden. Teknisk årsak: %s.",
		CTA:          "Åpne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "Et medlem",
	},
	"nn": {
		Subject:      "Innmelding må kontrollerast: %s",
		Body:         "%s har betalt innmeldinga, men medlemskapen vart ikkje gjeven automatisk fordi betalinga ikkje stemde med innmeldinga.",
		Next:         "Kontroller betalinga, og gje medlemskap manuelt om ho er i orden. Teknisk årsak: %s.",
		CTA:          "Opne medlemmet",
		FallbackClub: "klubben",
		FallbackName: "Eit medlem",
	},
}

func membershipCopy(copies map[string]membershipCopyT, locale string) membershipCopyT {
	c, ok := copies[locale]
	if !ok {
		c = copies[defaultLocale]
	}
	return c
}

// membershipHTMLTpl is one card: an optional greeting, the message, the next
// steps, a button and an optional second link.
var membershipHTMLTpl = template.Must(template.Must(sharedEmail.Clone()).Parse(`{{define "content"}}
{{if .Greeting}}              <p style="margin:0 0 16px 0;">{{.Greeting}}</p>
{{end}}              <p style="margin:0 0 16px 0;">{{.Body}}</p>
              <p style="margin:0 0 24px 0;">{{.Next}}</p>
{{template "button" (dict "URL" .URL "Label" .CTA "BG" "#0f172a")}}
{{if .SecondaryURL}}              <p style="margin:16px 0 0 0;text-align:center;font-size:14px;"><a href="{{.SecondaryURL}}" style="color:#0f172a;text-decoration:underline;">{{.Secondary}}</a></p>
{{end}}{{end}}`))

type membershipData struct {
	Club         string
	Greeting     string
	Body         string
	Next         string
	URL          string
	CTA          string
	SecondaryURL string
	Secondary    string
}

func renderMembership(d membershipData) string {
	var buf bytes.Buffer
	if err := membershipHTMLTpl.ExecuteTemplate(&buf, "base", d); err != nil {
		// Template bug: the message and a bare link still reach the reader.
		return fmt.Sprintf(`<p>%s</p><p><a href=%q>%s</a></p>`,
			template.HTMLEscapeString(d.Body), template.HTMLEscapeString(d.URL), template.HTMLEscapeString(d.CTA))
	}
	return buf.String()
}

// MembershipWelcomeSubject is the Subject of the welcome email a new member
// gets when their join payment settles.
func MembershipWelcomeSubject(locale, clubName string) string {
	c := membershipCopy(membershipWelcomeCopy, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	return fmt.Sprintf(c.Subject, clubName)
}

// MembershipWelcomeBody is its HTML body: portalURL is the member portal and
// waitingListURL the portal's waiting-list page.
func MembershipWelcomeBody(locale, clubName, firstName, portalURL, waitingListURL string) string {
	c := membershipCopy(membershipWelcomeCopy, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	greeting := "Hei,"
	if firstName != "" {
		greeting = fmt.Sprintf(c.Greeting, firstName)
	}
	return renderMembership(membershipData{
		Club: clubName, Greeting: greeting,
		Body: fmt.Sprintf(c.Body, clubName), Next: c.Next,
		URL: portalURL, CTA: c.CTA,
		SecondaryURL: waitingListURL, Secondary: c.Secondary,
	})
}

func boardSubject(copies map[string]membershipCopyT, locale, memberName string) string {
	c := membershipCopy(copies, locale)
	if memberName == "" {
		memberName = c.FallbackName
	}
	return fmt.Sprintf(c.Subject, memberName)
}

func boardBody(copies map[string]membershipCopyT, locale, clubName, memberName, memberURL string) string {
	c := membershipCopy(copies, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	if memberName == "" {
		memberName = c.FallbackName
	}
	return renderMembership(membershipData{
		Club: clubName, Body: fmt.Sprintf(c.Body, memberName), Next: c.Next,
		URL: memberURL, CTA: c.CTA,
	})
}

// MembershipBoardNoticeSubject is the Subject of the board's notice that a
// join payment settled and the payer is now a member.
func MembershipBoardNoticeSubject(locale, memberName string) string {
	return boardSubject(membershipBoardNoticeCopy, locale, memberName)
}

// MembershipBoardNoticeBody is its HTML body; memberURL opens the member in
// the admin member list.
func MembershipBoardNoticeBody(locale, clubName, memberName, memberURL string) string {
	return boardBody(membershipBoardNoticeCopy, locale, clubName, memberName, memberURL)
}

// MembershipJoinRefundedSubject is the Subject of the board's notice that a
// join payment was refunded or credited.
func MembershipJoinRefundedSubject(locale, memberName string) string {
	return boardSubject(membershipJoinRefundedCopy, locale, memberName)
}

// MembershipJoinRefundedBody is its HTML body; memberURL opens the member in
// the admin member list, where the board decides whether to deactivate.
func MembershipJoinRefundedBody(locale, clubName, memberName, memberURL string) string {
	return boardBody(membershipJoinRefundedCopy, locale, clubName, memberName, memberURL)
}

// MembershipGrantWithheldSubject is the Subject of the board's notice that a
// join was paid but the grant was withheld.
func MembershipGrantWithheldSubject(locale, memberName string) string {
	return boardSubject(membershipGrantWithheldCopy, locale, memberName)
}

// MembershipGrantWithheldBody is its HTML body; reason is the integrity
// check's reason, memberURL opens the member in the admin member list.
func MembershipGrantWithheldBody(locale, clubName, memberName, reason, memberURL string) string {
	c := membershipCopy(membershipGrantWithheldCopy, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	if memberName == "" {
		memberName = c.FallbackName
	}
	return renderMembership(membershipData{
		Club: clubName, Body: fmt.Sprintf(c.Body, memberName), Next: fmt.Sprintf(c.Next, reason),
		URL: memberURL, CTA: c.CTA,
	})
}
