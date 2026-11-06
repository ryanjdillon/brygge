package email

import (
	"bytes"
	"fmt"
	"html/template"
)

// --- Membership join emails (BRY-629) ---
//
// Both go to an address a visitor typed into the public join form, so neither
// carries anything else the visitor typed: the greeting has no name, and the
// copy is fixed per locale. That keeps the form from being a way to mail a
// chosen text to a stranger. They are written in Bokmål or Nynorsk, by the
// club's default language (JoinLocale), like the rest of the member mail.

// JoinLocale picks the language of the join emails from the club's default
// language: Nynorsk for a Nynorsk club, Bokmål otherwise.
func JoinLocale(clubDefaultLanguage string) string {
	if clubDefaultLanguage == "nn" {
		return "nn"
	}
	return defaultLocale
}

type joinCopyT struct {
	Subject       string // fmt string with %s for the club name
	Greeting      string
	Explanation   string // fmt string with %s for the club name
	CTA           string
	LinkIntro     string
	Expiry        string
	Ignore        string
	FallbackClub  string
	FooterContact string
}

var joinConfirmCopy = map[string]joinCopyT{
	"nb": {
		Subject:       "Bekreft innmeldingen i %s",
		Greeting:      "Hei,",
		Explanation:   "Takk for at du vil bli medlem i %s. Trykk på knappen for å bekrefte e-postadressen din og gå videre til betaling av kontingenten.",
		CTA:           "Bekreft innmeldingen",
		LinkIntro:     "Eller kopier denne lenken til nettleseren din:",
		Expiry:        "Lenken er gyldig i 24 timer og kan bare brukes én gang.",
		Ignore:        "Hvis du ikke har meldt deg inn, kan du se bort fra denne e-posten. Uten bekreftelse blir innmeldingen slettet etter 24 timer.",
		FallbackClub:  "klubben",
		FooterContact: "Har du spørsmål? Svar på denne e-posten – vi leser alle henvendelser.",
	},
	"nn": {
		Subject:       "Stadfest innmeldinga i %s",
		Greeting:      "Hei,",
		Explanation:   "Takk for at du vil bli medlem i %s. Trykk på knappen for å stadfeste e-postadressa di og gå vidare til betaling av kontingenten.",
		CTA:           "Stadfest innmeldinga",
		LinkIntro:     "Eller kopier denne lenkja til nettlesaren din:",
		Expiry:        "Lenkja er gyldig i 24 timar og kan berre brukast éin gong.",
		Ignore:        "Har du ikkje meldt deg inn, kan du sjå bort frå denne e-posten. Utan stadfesting blir innmeldinga sletta etter 24 timar.",
		FallbackClub:  "klubben",
		FooterContact: "Har du spørsmål? Svar på denne e-posten – vi les alle førespurnader.",
	},
}

var joinAlreadyMemberCopy = map[string]joinCopyT{
	"nb": {
		Subject:       "Du er allerede registrert i %s",
		Greeting:      "Hei,",
		Explanation:   "Noen prøvde å melde inn denne e-postadressen i %s, men den er allerede registrert hos oss, så du trenger ikke melde deg inn på nytt. Bruk knappen for å logge inn.",
		CTA:           "Logg inn",
		LinkIntro:     "Eller kopier denne lenken til nettleseren din:",
		Expiry:        "Lenken er gyldig i 15 minutter og kan bare brukes én gang.",
		Ignore:        "Hvis det ikke var deg, kan du se bort fra denne e-posten.",
		FallbackClub:  "klubben",
		FooterContact: "Har du spørsmål? Svar på denne e-posten – vi leser alle henvendelser.",
	},
	"nn": {
		Subject:       "Du er allereie registrert i %s",
		Greeting:      "Hei,",
		Explanation:   "Nokon prøvde å melde inn denne e-postadressa i %s, men ho er allereie registrert hjå oss, så du treng ikkje melde deg inn på nytt. Bruk knappen for å logge inn.",
		CTA:           "Logg inn",
		LinkIntro:     "Eller kopier denne lenkja til nettlesaren din:",
		Expiry:        "Lenkja er gyldig i 15 minutt og kan berre brukast éin gong.",
		Ignore:        "Var det ikkje deg, kan du sjå bort frå denne e-posten.",
		FallbackClub:  "klubben",
		FooterContact: "Har du spørsmål? Svar på denne e-posten – vi les alle førespurnader.",
	},
}

// joinContactCopy is the email sent instead of a confirmation when the board
// has marked the address "can't rejoin". It says only that membership goes
// through the board, never why.
var joinContactCopy = map[string]joinCopyT{
	"nb": {
		Subject:       "Innmelding i %s",
		Greeting:      "Hei,",
		Explanation:   "Takk for at du vil bli medlem i %s. Innmeldingen for denne e-postadressen kan ikke fullføres på nettsiden, så ta kontakt med styret om medlemskap.",
		CTA:           "Kontakt styret",
		LinkIntro:     "Styret nås på:",
		Ignore:        "Hvis du ikke har meldt deg inn, kan du se bort fra denne e-posten.",
		FallbackClub:  "klubben",
		FooterContact: "Har du spørsmål? Svar på denne e-posten – vi leser alle henvendelser.",
	},
	"nn": {
		Subject:       "Innmelding i %s",
		Greeting:      "Hei,",
		Explanation:   "Takk for at du vil bli medlem i %s. Innmeldinga for denne e-postadressa kan ikkje fullførast på nettsida, så ta kontakt med styret om medlemskap.",
		CTA:           "Kontakt styret",
		LinkIntro:     "Styret når du på:",
		Ignore:        "Har du ikkje meldt deg inn, kan du sjå bort frå denne e-posten.",
		FallbackClub:  "klubben",
		FooterContact: "Har du spørsmål? Svar på denne e-posten – vi les alle førespurnader.",
	},
}

// joinContactHTMLTpl is the join card with a mailto button to the board when
// the club has a contact address, and only the reply hint in the footer when
// it has none.
var joinContactHTMLTpl = template.Must(template.Must(sharedEmail.Clone()).Parse(`{{define "content"}}
              <p style="margin:0 0 16px 0;">{{.Copy.Greeting}}</p>
              <p style="margin:0 0 24px 0;">{{.Copy.Explanation}}</p>
{{if .Contact}}{{template "button" (dict "URL" .MailTo "Label" .Copy.CTA "BG" "#0f172a")}}
              <p style="margin:0 0 24px 0;font-size:13px;color:#64748b;">{{.Copy.LinkIntro}} {{.Contact}}</p>
{{end}}              <p style="margin:0;font-size:13px;color:#64748b;">{{.Copy.Ignore}}</p>{{end}}
{{define "footer"}}<p style="margin:0 0 6px 0;">{{.Copy.FooterContact}}</p>
              <p style="margin:0;"><strong style="color:#334155;">{{.Club}}</strong>{{if .Domain}} · <a href="https://{{.Domain}}" style="color:#64748b;text-decoration:underline;">{{.Domain}}</a>{{end}}</p>{{end}}`))

// JoinContactBoardSubject is the Subject of the email sent when the board has
// marked the address "can't rejoin".
func JoinContactBoardSubject(locale, clubName string) string {
	return joinSubject(joinContactCopy, locale, clubName)
}

// JoinContactBoardBody is its HTML body. contact is the club's board address
// (contact-form recipient), or "" when none is set: the email then relies on
// the reply hint in the footer, as the other join emails do.
func JoinContactBoardBody(locale, clubName, domain, contact string) string {
	c := joinCopy(joinContactCopy, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	rendered := c
	rendered.Explanation = fmt.Sprintf(c.Explanation, clubName)
	data := struct {
		Club    string
		Domain  string
		Contact string
		MailTo  template.URL
		Copy    joinCopyT
	}{Club: clubName, Domain: domain, Contact: contact, Copy: rendered}
	if contact != "" {
		data.MailTo = template.URL("mailto:" + contact) // #nosec G203 -- a bare mailbox validated by parseBareEmail from club settings
	}
	var buf bytes.Buffer
	if err := joinContactHTMLTpl.ExecuteTemplate(&buf, "base", data); err != nil {
		return fmt.Sprintf(`<p>%s</p>`, template.HTMLEscapeString(rendered.Explanation))
	}
	return buf.String()
}

// joinHTMLTpl is the magic-link card: a greeting, one explanation, one button
// and the link spelled out, with the contact footer.
var joinHTMLTpl = template.Must(template.Must(sharedEmail.Clone()).Parse(`{{define "content"}}
              <p style="margin:0 0 16px 0;">{{.Copy.Greeting}}</p>
              <p style="margin:0 0 24px 0;">{{.Copy.Explanation}}</p>
{{template "button" (dict "URL" .URL "Label" .Copy.CTA "BG" "#0f172a")}}
              <p style="margin:0 0 8px 0;font-size:13px;color:#64748b;">{{.Copy.LinkIntro}}</p>
              <p style="margin:0 0 24px 0;font-family:ui-monospace,Menlo,Consolas,monospace;font-size:12px;color:#475569;word-break:break-all;background:#f8fafc;padding:10px 12px;border-radius:4px;">{{.URL}}</p>
              <p style="margin:0 0 8px 0;font-size:13px;color:#64748b;">{{.Copy.Expiry}}</p>
              <p style="margin:0;font-size:13px;color:#64748b;">{{.Copy.Ignore}}</p>{{end}}
{{define "footer"}}<p style="margin:0 0 6px 0;">{{.Copy.FooterContact}}</p>
              <p style="margin:0;"><strong style="color:#334155;">{{.Club}}</strong>{{if .Domain}} · <a href="https://{{.Domain}}" style="color:#64748b;text-decoration:underline;">{{.Domain}}</a>{{end}}</p>{{end}}`))

func joinCopy(copies map[string]joinCopyT, locale string) joinCopyT {
	c, ok := copies[locale]
	if !ok {
		c = copies[defaultLocale]
	}
	return c
}

func joinSubject(copies map[string]joinCopyT, locale, clubName string) string {
	c := joinCopy(copies, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	return fmt.Sprintf(c.Subject, clubName)
}

func joinBody(copies map[string]joinCopyT, locale, clubName, domain, url string) string {
	c := joinCopy(copies, locale)
	if clubName == "" {
		clubName = c.FallbackClub
	}
	rendered := c
	rendered.Explanation = fmt.Sprintf(c.Explanation, clubName)
	data := struct {
		Club   string
		Domain string
		URL    string
		Copy   joinCopyT
	}{Club: clubName, Domain: domain, URL: url, Copy: rendered}
	var buf bytes.Buffer
	if err := joinHTMLTpl.ExecuteTemplate(&buf, "base", data); err != nil {
		// Template bug: a bare link still lets the recipient go on.
		return fmt.Sprintf(`<p><a href=%q>%s</a></p>`, template.HTMLEscapeString(url), template.HTMLEscapeString(c.CTA))
	}
	return buf.String()
}

// JoinConfirmSubject is the Subject of the email that confirms a membership
// application.
func JoinConfirmSubject(locale, clubName string) string {
	return joinSubject(joinConfirmCopy, locale, clubName)
}

// JoinConfirmBody is the HTML body of the confirmation email; confirmURL is
// the SPA page that confirms the application (BRY-632), never an API route,
// so a mail scanner fetching the link changes nothing.
func JoinConfirmBody(locale, clubName, domain, confirmURL string) string {
	return joinBody(joinConfirmCopy, locale, clubName, domain, confirmURL)
}

// JoinAlreadyMemberSubject is the Subject of the email sent instead of a
// confirmation when the address already belongs to an active user.
func JoinAlreadyMemberSubject(locale, clubName string) string {
	return joinSubject(joinAlreadyMemberCopy, locale, clubName)
}

// JoinAlreadyMemberBody is its HTML body; loginURL is an ordinary magic link.
func JoinAlreadyMemberBody(locale, clubName, domain, loginURL string) string {
	return joinBody(joinAlreadyMemberCopy, locale, clubName, domain, loginURL)
}
