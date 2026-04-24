package email

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// supportedLocales is the set of locale codes brygge has UI translations for.
// Keep aligned with frontend/src/locales/*.json.
var supportedLocales = map[string]bool{
	"nb": true, "en": true, "de": true, "fr": true,
	"it": true, "nl": true, "pl": true,
}

const defaultLocale = "nb"

// DetectLocale picks the best supported locale from an HTTP request's
// Accept-Language header. Returns defaultLocale if no supported match.
func DetectLocale(r *http.Request) string {
	return parseAcceptLanguage(r.Header.Get("Accept-Language"))
}

func parseAcceptLanguage(header string) string {
	if header == "" {
		return defaultLocale
	}
	for _, part := range strings.Split(header, ",") {
		// Each part looks like "nb-NO;q=0.9". Strip quality and region.
		lang := strings.TrimSpace(part)
		if i := strings.Index(lang, ";"); i != -1 {
			lang = lang[:i]
		}
		if i := strings.Index(lang, "-"); i != -1 {
			lang = lang[:i]
		}
		lang = strings.ToLower(strings.TrimSpace(lang))
		if supportedLocales[lang] {
			return lang
		}
	}
	return defaultLocale
}

// MagicLinkSubject returns a localized Subject header for magic-link emails.
// Deliberately constant across sends so spam classifiers (Gmail's in
// particular) recognize repeated transactional mail from the same sender
// and let the reputation compound. Gmail will thread consecutive logins —
// that's fine; only the latest link works anyway.
func MagicLinkSubject(locale, clubName string, _ time.Time) string {
	tpl, ok := magicLinkSubjects[locale]
	if !ok {
		tpl = magicLinkSubjects[defaultLocale]
	}
	if clubName == "" {
		clubName = tpl.fallbackClub
	}
	return fmt.Sprintf("%s %s", tpl.verb, clubName)
}

// MagicLinkBody returns a localized HTML body for magic-link emails.
func MagicLinkBody(locale, clubName, loginURL string) string {
	tpl, ok := magicLinkSubjects[locale]
	if !ok {
		tpl = magicLinkSubjects[defaultLocale]
	}
	if clubName == "" {
		clubName = tpl.fallbackClub
	}
	return fmt.Sprintf(
		`<p>%s</p><p><a href="%s">%s</a></p><p style="color:#666;font-size:0.9em">%s</p>`,
		fmt.Sprintf(tpl.intro, clubName),
		loginURL,
		tpl.cta,
		tpl.expiry,
	)
}

type magicLinkTpl struct {
	verb         string // "Login to", "Logg inn hos", etc.
	intro        string // body opening, has %s for club name
	cta          string // call-to-action link text
	expiry       string // expiry notice
	fallbackClub string // used when CLUB_NAME env is unset
}

// InvoiceSubject returns a localized Subject for invoice-delivery emails.
func InvoiceSubject(locale, clubName string, invoiceNumber int) string {
	tpl, ok := invoiceTemplates[locale]
	if !ok {
		tpl = invoiceTemplates[defaultLocale]
	}
	return fmt.Sprintf(tpl.subject, invoiceNumber, clubName)
}

// InvoiceBody returns a localized HTML body for invoice-delivery emails.
func InvoiceBody(locale, memberName, clubName string, invoiceNumber int, dueDate time.Time, total float64, kid, bankAccount string) string {
	tpl, ok := invoiceTemplates[locale]
	if !ok {
		tpl = invoiceTemplates[defaultLocale]
	}
	return fmt.Sprintf(tpl.body,
		memberName,
		invoiceNumber,
		dueDate.Format("02.01.2006"),
		total,
		kid,
		bankAccount,
		clubName,
	)
}

type invoiceTpl struct {
	subject string // fmt: "%d", "%s" (invoice number, club name)
	body    string // fmt args: memberName, invoiceNumber, dueDate, total, kid, bankAccount, clubName
}

var invoiceTemplates = map[string]invoiceTpl{
	"nb": {
		subject: "Faktura #%d fra %s",
		body:    `<p>Hei %s,</p><p>Vedlagt finner du faktura #%d.</p><p>Forfallsdato: %s<br>Beløp: kr %.2f<br>KID: %s<br>Kontonummer: %s</p><p>Med vennlig hilsen,<br>%s</p>`,
	},
	"en": {
		subject: "Invoice #%d from %s",
		body:    `<p>Hi %s,</p><p>Attached is invoice #%d.</p><p>Due date: %s<br>Amount: NOK %.2f<br>KID: %s<br>Account: %s</p><p>Kind regards,<br>%s</p>`,
	},
	"de": {
		subject: "Rechnung #%d von %s",
		body:    `<p>Hallo %s,</p><p>Im Anhang findest du die Rechnung #%d.</p><p>Fälligkeitsdatum: %s<br>Betrag: NOK %.2f<br>KID: %s<br>Konto: %s</p><p>Mit freundlichen Grüßen,<br>%s</p>`,
	},
	"fr": {
		subject: "Facture n° %d de %s",
		body:    `<p>Bonjour %s,</p><p>Vous trouverez en pièce jointe la facture n° %d.</p><p>Date d'échéance : %s<br>Montant : %.2f NOK<br>KID : %s<br>Compte : %s</p><p>Cordialement,<br>%s</p>`,
	},
	"it": {
		subject: "Fattura #%d da %s",
		body:    `<p>Ciao %s,</p><p>In allegato la fattura #%d.</p><p>Scadenza: %s<br>Importo: NOK %.2f<br>KID: %s<br>Conto: %s</p><p>Cordiali saluti,<br>%s</p>`,
	},
	"nl": {
		subject: "Factuur #%d van %s",
		body:    `<p>Hallo %s,</p><p>In de bijlage vind je factuur #%d.</p><p>Vervaldatum: %s<br>Bedrag: NOK %.2f<br>KID: %s<br>Rekening: %s</p><p>Met vriendelijke groet,<br>%s</p>`,
	},
	"pl": {
		subject: "Faktura #%d od %s",
		body:    `<p>Witaj %s,</p><p>W załączniku znajdziesz fakturę #%d.</p><p>Termin płatności: %s<br>Kwota: %.2f NOK<br>KID: %s<br>Konto: %s</p><p>Z poważaniem,<br>%s</p>`,
	},
}

var magicLinkSubjects = map[string]magicLinkTpl{
	"nb": {
		verb:         "Logg inn hos",
		intro:        "Klikk lenken under for å logge inn hos %s:",
		cta:          "Logg inn",
		expiry:       "Lenken er gyldig i 15 minutter.",
		fallbackClub: "klubben",
	},
	"en": {
		verb:         "Login to",
		intro:        "Click the link below to log in to %s:",
		cta:          "Log in",
		expiry:       "The link is valid for 15 minutes.",
		fallbackClub: "your club",
	},
	"de": {
		verb:         "Anmelden bei",
		intro:        "Klicke auf den Link unten, um dich bei %s anzumelden:",
		cta:          "Anmelden",
		expiry:       "Der Link ist 15 Minuten gültig.",
		fallbackClub: "dem Verein",
	},
	"fr": {
		verb:         "Connexion à",
		intro:        "Cliquez sur le lien ci-dessous pour vous connecter à %s :",
		cta:          "Se connecter",
		expiry:       "Le lien est valable 15 minutes.",
		fallbackClub: "votre club",
	},
	"it": {
		verb:         "Accedi a",
		intro:        "Clicca sul link qui sotto per accedere a %s:",
		cta:          "Accedi",
		expiry:       "Il link è valido per 15 minuti.",
		fallbackClub: "il club",
	},
	"nl": {
		verb:         "Inloggen bij",
		intro:        "Klik op de link hieronder om in te loggen bij %s:",
		cta:          "Inloggen",
		expiry:       "De link is 15 minuten geldig.",
		fallbackClub: "je club",
	},
	"pl": {
		verb:         "Zaloguj się do",
		intro:        "Kliknij poniższy link, aby zalogować się do %s:",
		cta:          "Zaloguj się",
		expiry:       "Link jest ważny przez 15 minut.",
		fallbackClub: "klubu",
	},
}
