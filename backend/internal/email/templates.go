package email

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// supportedLocales is the set of locale codes brygge has UI translations for.
// Keep aligned with frontend/src/locales/*.json.
var supportedLocales = map[string]bool{
	"nb": true, "nn": true, "en": true, "de": true, "fr": true,
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

// --- Magic-link email ---

// MagicLinkSubject returns a localized Subject header for magic-link emails.
// Deliberately constant across sends so spam classifiers (Gmail's in
// particular) recognize repeated transactional mail from the same sender
// and let the reputation compound. Gmail will thread consecutive logins —
// that's fine; only the latest link works anyway.
func MagicLinkSubject(locale, clubName string, _ time.Time) string {
	tpl, ok := magicLinkCopy[locale]
	if !ok {
		tpl = magicLinkCopy[defaultLocale]
	}
	if clubName == "" {
		clubName = tpl.FallbackClub
	}
	return fmt.Sprintf(tpl.Subject, clubName)
}

// MagicLinkBody returns a localized HTML body for magic-link emails.
// domain is used in the footer link; clubName is interpolated throughout
// the copy. When clubName is empty, a locale-appropriate fallback is used.
func MagicLinkBody(locale, clubName, domain, loginURL string) string {
	tpl, ok := magicLinkCopy[locale]
	if !ok {
		tpl = magicLinkCopy[defaultLocale]
	}
	if clubName == "" {
		clubName = tpl.FallbackClub
	}
	// Pre-render Explanation since html/template doesn't recursively
	// evaluate text substituted into a template — embedding {{.Club}}
	// in the locale string would emit it literally. Use %s instead.
	rendered := tpl
	rendered.Explanation = fmt.Sprintf(tpl.Explanation, clubName)
	data := struct {
		Club     string
		Domain   string
		LoginURL string
		Copy     magicLinkCopyT
	}{
		Club:     clubName,
		Domain:   domain,
		LoginURL: loginURL,
		Copy:     rendered,
	}
	var buf bytes.Buffer
	if err := magicLinkHTMLTpl.ExecuteTemplate(&buf, "base", data); err != nil {
		// template bug — fall back to a plain anchor so the user can still log in
		return fmt.Sprintf(`<p><a href=%q>%s</a></p>`, loginURL, tpl.CTA)
	}
	return buf.String()
}

type magicLinkCopyT struct {
	Subject       string // fmt string with %s for club name
	Greeting      string
	Explanation   string // "Someone requested a login link for {Club}"
	CTA           string // button text
	LinkIntro     string // "Or copy this link..."
	Expiry        string
	Ignore        string // "If you didn't request this..."
	FallbackClub  string
	FooterContact string // shown beneath the divider
}

var magicLinkCopy = map[string]magicLinkCopyT{
	"nb": {
		Subject:       "Logg inn hos %s",
		Greeting:      "Hei,",
		Explanation:   "Noen ba om en innloggingslenke til medlemsportalen til %s.",
		CTA:           "Logg inn",
		LinkIntro:     "Eller kopier denne lenken til nettleseren din:",
		Expiry:        "Lenken er gyldig i 15 minutter og kan kun brukes én gang.",
		Ignore:        "Hvis du ikke ba om denne e-posten, kan du trygt slette den.",
		FallbackClub:  "klubben",
		FooterContact: "Har du spørsmål? Svar på denne e-posten – vi leser alle henvendelser.",
	},
	"en": {
		Subject:       "Sign in to %s",
		Greeting:      "Hello,",
		Explanation:   "Someone requested a sign-in link for the %s member portal.",
		CTA:           "Sign in",
		LinkIntro:     "Or copy this link into your browser:",
		Expiry:        "This link is valid for 15 minutes and can be used only once.",
		Ignore:        "If you didn't request this email, you can safely ignore it.",
		FallbackClub:  "your club",
		FooterContact: "Questions? Just reply to this email — we read every one.",
	},
	"de": {
		Subject:       "Anmeldung bei %s",
		Greeting:      "Hallo,",
		Explanation:   "Es wurde ein Anmeldelink für das Mitgliederportal von %s angefordert.",
		CTA:           "Anmelden",
		LinkIntro:     "Oder kopiere diesen Link in deinen Browser:",
		Expiry:        "Der Link ist 15 Minuten gültig und kann nur einmal verwendet werden.",
		Ignore:        "Wenn du diese E-Mail nicht angefordert hast, kannst du sie einfach ignorieren.",
		FallbackClub:  "dem Verein",
		FooterContact: "Fragen? Antworte einfach auf diese E-Mail – wir lesen jede.",
	},
	"fr": {
		Subject:       "Connexion à %s",
		Greeting:      "Bonjour,",
		Explanation:   "Quelqu'un a demandé un lien de connexion au portail membre de %s.",
		CTA:           "Se connecter",
		LinkIntro:     "Ou copiez ce lien dans votre navigateur :",
		Expiry:        "Ce lien est valable 15 minutes et ne peut être utilisé qu'une seule fois.",
		Ignore:        "Si vous n'avez pas demandé ce message, vous pouvez l'ignorer.",
		FallbackClub:  "votre club",
		FooterContact: "Une question ? Répondez à cet e-mail, nous lisons chaque message.",
	},
	"it": {
		Subject:       "Accedi a %s",
		Greeting:      "Ciao,",
		Explanation:   "Qualcuno ha richiesto un link di accesso al portale soci di %s.",
		CTA:           "Accedi",
		LinkIntro:     "Oppure copia questo link nel tuo browser:",
		Expiry:        "Il link è valido per 15 minuti e può essere usato una sola volta.",
		Ignore:        "Se non hai richiesto questa email, puoi ignorarla tranquillamente.",
		FallbackClub:  "il club",
		FooterContact: "Hai domande? Rispondi a questa email, leggiamo ogni messaggio.",
	},
	"nl": {
		Subject:       "Inloggen bij %s",
		Greeting:      "Hallo,",
		Explanation:   "Iemand heeft een inloglink aangevraagd voor het ledenportaal van %s.",
		CTA:           "Inloggen",
		LinkIntro:     "Of kopieer deze link naar je browser:",
		Expiry:        "De link is 15 minuten geldig en kan één keer worden gebruikt.",
		Ignore:        "Als je deze e-mail niet hebt aangevraagd, kun je hem negeren.",
		FallbackClub:  "je club",
		FooterContact: "Vragen? Antwoord op deze e-mail, we lezen alles.",
	},
	"pl": {
		Subject:       "Zaloguj się do %s",
		Greeting:      "Cześć,",
		Explanation:   "Ktoś poprosił o link do logowania w portalu członkowskim %s.",
		CTA:           "Zaloguj się",
		LinkIntro:     "Albo skopiuj ten link do przeglądarki:",
		Expiry:        "Link jest ważny przez 15 minut i można go użyć tylko raz.",
		Ignore:        "Jeśli nie prosiłeś o tę wiadomość, możesz ją zignorować.",
		FallbackClub:  "klubu",
		FooterContact: "Pytania? Odpowiedz na tę wiadomość, czytamy każdą.",
	},
}

// --- Shared email chrome + partials (single source of truth, BRY-258) ---
//
// Every transactional email is the same card: grey page, white rounded card,
// a {{.Club}} header, a per-email {{template "content" .}}, and a
// {{template "footer" .}} (default below, overridable per email). The CTA
// button and the invoice detail box are single partials so their structure
// and style cannot diverge between emails.

// emailDict builds a map for passing named params to a partial, e.g.
// {{template "button" (dict "URL" .X "Label" "..." "BG" "#hex")}}.
func emailDict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd argument count")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: non-string key at %d", i)
		}
		m[k] = pairs[i+1]
	}
	return m, nil
}

var emailFuncs = template.FuncMap{
	"dict": emailDict,
	"css":  func(s string) template.CSS { return template.CSS(s) }, // #nosec G203 -- only literal hex colours from the templates reach it, never caller data
}

// emailFont is the one font stack of every email. Cells that carry values
// repeat it because some clients reset the font inside tables.
const emailFont = `font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;`

// The card is designed for light only. The color-scheme declarations keep
// clients that honour color-scheme (Apple Mail, Outlook.com) from darkening
// it; the Gmail apps apply their own inversion regardless.
const emailBaseLayout = `{{define "base"}}<!DOCTYPE html>
<html style="color-scheme:light;">
<head>
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
</head>
<body style="margin:0;padding:0;background:#f5f5f5;` + emailFont + `">
  <table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="background:#f5f5f5;padding:32px 16px;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="max-width:560px;background:#ffffff;border:1px solid #e5e7eb;border-radius:6px;">
          <tr>
            <td style="padding:32px 32px 16px 32px;border-bottom:1px solid #e5e7eb;">
              <h1 style="margin:0;font-size:20px;color:#0f172a;font-weight:600;">{{.Club}}</h1>
            </td>
          </tr>
          <tr>
            <td style="padding:24px 32px;color:#1f2937;font-size:15px;line-height:1.55;">
{{template "content" .}}
            </td>
          </tr>
          <tr>
            <td style="padding:20px 32px;border-top:1px solid #e5e7eb;background:#f8fafc;color:#64748b;font-size:12px;line-height:1.5;border-radius:0 0 6px 6px;">
{{template "footer" .}}
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>{{end}}`

// emailButton is the one CTA button: shared Gmail-safe structure (centered <p>
// + inline-block <a>; html/template URL-escapes the href), brand colour via .BG.
const emailButton = `{{define "button"}}<p style="margin:0 0 8px 0;text-align:center;">
                <a href="{{.URL}}" style="display:inline-block;background:{{css .BG}};color:#ffffff;text-decoration:none;padding:12px 28px;border-radius:4px;font-weight:600;font-size:15px;">{{.Label}}</a>
              </p>{{end}}`

// emailPayButton is the online pay CTA shared by the invoice and reminder
// emails, with the accepted methods beneath; absent without a pay link.
const emailPayButton = `{{define "payButton"}}{{if .PayURL}}
              <p style="margin:0 0 4px 0;font-size:13px;color:#64748b;">Du kan også betale på nett om du ønsker det:</p>
{{template "button" (dict "URL" .PayURL "Label" "Betal nå" "BG" "#0f172a")}}
{{if .PayMethods}}              <p style="margin:0 0 8px 0;text-align:center;font-size:13px;color:#64748b;">{{.PayMethods}}</p>
{{end}}{{end}}{{end}}`

// emailInvoiceDetails is the label|value payment box shared by the invoice and
// reminder emails. Numbers stay in the body font with tabular figures: a
// monospace stack made them look like a different, smaller typeface.
const emailInvoiceDetails = `{{define "invoiceDetails"}}<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="margin:0 0 24px 0;background:#f8fafc;border-radius:6px;">
                <tr>
                  <td style="padding:12px 16px 4px 16px;font-size:14px;line-height:1.4;color:#64748b;width:40%;` + emailFont + `">Fakturanummer</td>
                  <td style="padding:12px 16px 4px 16px;font-size:14px;line-height:1.4;color:#0f172a;` + emailFont + `font-variant-numeric:tabular-nums;">#{{.InvoiceNumber}}</td>
                </tr>
                <tr>
                  <td style="padding:4px 16px;font-size:14px;line-height:1.4;color:#64748b;` + emailFont + `">Forfallsdato</td>
                  <td style="padding:4px 16px;font-size:14px;line-height:1.4;color:#0f172a;font-weight:600;` + emailFont + `font-variant-numeric:tabular-nums;">{{.DueDate}}</td>
                </tr>
                <tr>
                  <td style="padding:4px 16px;font-size:14px;line-height:1.4;color:#64748b;` + emailFont + `">Beløp å betale</td>
                  <td style="padding:4px 16px;font-size:14px;line-height:1.4;color:#0f172a;font-weight:600;white-space:nowrap;` + emailFont + `font-variant-numeric:tabular-nums;">{{.Amount}}</td>
                </tr>
                <tr>
                  <td style="padding:4px 16px;font-size:14px;line-height:1.4;color:#64748b;` + emailFont + `">KID</td>
                  <td style="padding:4px 16px;font-size:14px;line-height:1.4;color:#0f172a;` + emailFont + `font-variant-numeric:tabular-nums;">{{.KID}}</td>
                </tr>
                <tr>
                  <td style="padding:4px 16px 12px 16px;font-size:14px;line-height:1.4;color:#64748b;` + emailFont + `">Kontonummer</td>
                  <td style="padding:4px 16px 12px 16px;font-size:14px;line-height:1.4;color:#0f172a;` + emailFont + `font-variant-numeric:tabular-nums;">{{.BankAccount}}</td>
                </tr>
              </table>{{end}}`

// emailInvoiceLines is the "Gjelder" list of what the faktura charges for,
// shared by the invoice and reminder emails; absent without lines. Line text
// is member- and treasurer-supplied, so it only ever reaches the email through
// html/template's escaping.
const emailInvoiceLines = `{{define "invoiceLines"}}{{if .Lines}}<p style="margin:0 0 6px 0;font-size:13px;font-weight:600;color:#334155;">Gjelder</p>
              <table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="margin:0 0 20px 0;">
{{range .Lines}}                <tr>
                  <td style="padding:5px 0;border-bottom:1px solid #e5e7eb;font-size:14px;line-height:1.4;color:#0f172a;vertical-align:top;word-break:break-word;overflow-wrap:anywhere;` + emailFont + `">{{.Description}}{{if .SubDescription}}<br><span style="font-size:12px;color:#64748b;">{{.SubDescription}}</span>{{end}}</td>
                  <td align="right" style="padding:5px 0 5px 12px;border-bottom:1px solid #e5e7eb;font-size:14px;line-height:1.4;color:#0f172a;vertical-align:top;text-align:right;white-space:nowrap;` + emailFont + `font-variant-numeric:tabular-nums;">{{.Amount}}</td>
                </tr>
{{end}}{{if .MoreLines}}                <tr>
                  <td colspan="2" style="padding:6px 0 0 0;font-size:13px;line-height:1.4;color:#64748b;` + emailFont + `">+ {{.MoreLines}} til (se PDF-vedlegget)</td>
                </tr>
{{end}}              </table>
{{end}}{{end}}`

// emailDefaultFooter is the simple club-name footer (invoice + reminder). The
// magic-link email overrides "footer" with its own.
const emailDefaultFooter = `{{define "footer"}}<p style="margin:0;"><strong style="color:#334155;">{{.Club}}</strong></p>{{end}}`

// sharedEmail holds the base layout + partials. Each email clones it and adds
// its own "content" (and, for the magic-link, a "footer" override).
var sharedEmail = template.Must(template.New("shared").Funcs(emailFuncs).
	Parse(emailBaseLayout + emailButton + emailPayButton + emailInvoiceDetails + emailInvoiceLines + emailDefaultFooter))

var magicLinkHTMLTpl = template.Must(template.Must(sharedEmail.Clone()).Parse(`{{define "content"}}
              <p style="margin:0 0 16px 0;">{{.Copy.Greeting}}</p>
              <p style="margin:0 0 24px 0;">{{.Copy.Explanation}}</p>
{{template "button" (dict "URL" .LoginURL "Label" .Copy.CTA "BG" "#0f172a")}}
              <p style="margin:0 0 8px 0;font-size:13px;color:#64748b;">{{.Copy.LinkIntro}}</p>
              <p style="margin:0 0 24px 0;font-family:ui-monospace,Menlo,Consolas,monospace;font-size:12px;color:#475569;word-break:break-all;background:#f8fafc;padding:10px 12px;border-radius:4px;">{{.LoginURL}}</p>
              <p style="margin:0 0 8px 0;font-size:13px;color:#64748b;">{{.Copy.Expiry}}</p>
              <p style="margin:0;font-size:13px;color:#64748b;">{{.Copy.Ignore}}</p>{{end}}
{{define "footer"}}<p style="margin:0 0 6px 0;">{{.Copy.FooterContact}}</p>
              <p style="margin:0;"><strong style="color:#334155;">{{.Club}}</strong> · <a href="https://{{.Domain}}" style="color:#64748b;text-decoration:underline;">{{.Domain}}</a></p>{{end}}`))

// --- Invoice email ---
//
// Visually mirrors the magic-link template (same card chrome, same
// header/body/footer rhythm) so members get a consistent look from the
// club. Norwegian only for now — the locale arg is accepted and
// ignored so existing callers don't need to change. Localization will
// be added back when translations are ready.

// InvoiceLine is one line of a faktura as its email lists it under "Gjelder".
type InvoiceLine struct {
	Description    string
	SubDescription string
	// Amount is the line total, signed: an exemption line is negative.
	Amount float64
}

// maxEmailLines caps the "Gjelder" list; the PDF carries the rest, so a
// faktura with many lines does not push the payment details off the screen.
const maxEmailLines = 10

type emailLineView struct {
	Description    string
	SubDescription string
	Amount         string
}

// emailLines formats lines for the "Gjelder" list: the first maxEmailLines,
// and how many more the PDF holds.
func emailLines(lines []InvoiceLine) (shown []emailLineView, more int) {
	if len(lines) > maxEmailLines {
		more = len(lines) - maxEmailLines
		lines = lines[:maxEmailLines]
	}
	shown = make([]emailLineView, len(lines))
	for i, l := range lines {
		shown[i] = emailLineView{Description: l.Description, SubDescription: l.SubDescription, Amount: formatNOK(l.Amount)}
	}
	return shown, more
}

// InvoiceSubject returns the Subject header for invoice-delivery emails.
// Pattern: "Du har mottatt en ny faktura fra <Klubb> [Faktura 123]".
// The bracketed token at the end gives Gmail/Apple Mail something to
// thread on if multiple fakturas land in the same conversation.
func InvoiceSubject(_, clubName string, invoiceNumber int) string {
	if clubName == "" {
		clubName = "klubben"
	}
	return fmt.Sprintf("Du har mottatt en ny faktura fra %s [Faktura %d]", clubName, invoiceNumber)
}

// InvoiceBody returns the HTML body for invoice-delivery emails.
// memberName, dueDate, total, kid and bankAccount are interpolated
// into the Norwegian copy below. The PDF is attached separately by
// the caller.
//
// payURL is the faktura's online pay page ("" for a KID-only email) and
// payMethods the line naming the ways to pay there (PayMethodsLabel). lines
// are the faktura's lines, listed under "Gjelder"; nil omits
// the list.
func InvoiceBody(_ /*locale*/, memberName, clubName string, invoiceNumber int, dueDate time.Time, total float64, kid, bankAccount, payURL, payMethods string, lines []InvoiceLine) string {
	if clubName == "" {
		clubName = "klubben"
	}
	if memberName == "" {
		memberName = "medlem"
	}
	data := struct {
		Club          string
		Member        string
		InvoiceNumber int
		DueDate       string
		Amount        string
		KID           string
		BankAccount   string
		PayURL        string
		PayMethods    string
		Lines         []emailLineView
		MoreLines     int
	}{
		Club:          clubName,
		Member:        memberName,
		InvoiceNumber: invoiceNumber,
		DueDate:       dueDate.Format("02.01.2006"),
		Amount:        formatNOK(total),
		KID:           kid,
		BankAccount:   bankAccount,
		PayURL:        payURL,
		PayMethods:    payMethods,
	}
	data.Lines, data.MoreLines = emailLines(lines)
	var buf bytes.Buffer
	// A faktura that nets to zero (every line exempted) asks for nothing:
	// no KID, account number or pay button that could prompt a payment.
	if math.Round(total*100) == 0 {
		if err := invoiceNoPaymentHTMLTpl.ExecuteTemplate(&buf, "base", data); err == nil {
			return buf.String()
		}
		return invoiceNoPaymentFallback(memberName, clubName, invoiceNumber)
	}
	if err := invoiceHTMLTpl.ExecuteTemplate(&buf, "base", data); err != nil {
		// Template bug — fall back to a flat readable summary so the
		// member still has the payment details.
		return invoicePaymentFallback(false, memberName, clubName, invoiceNumber, data.DueDate, data.Amount, kid, bankAccount, payURL)
	}
	return buf.String()
}

// invoiceNoPaymentFallback is the flat body sent for a faktura that nets to
// zero when its template fails to render. Built without html/template, it
// escapes every value itself, and it leaves the lines to the PDF.
func invoiceNoPaymentFallback(memberName, clubName string, invoiceNumber int) string {
	return fmt.Sprintf(
		`<p>Hei %s,</p><p>Du har mottatt faktura #%d fra %s. Den er fullt fritatt, så du skal ikke betale noe.</p>`,
		html.EscapeString(memberName), invoiceNumber, html.EscapeString(clubName),
	)
}

// invoicePaymentFallback is the flat summary of the payment details sent when
// the invoice or reminder template fails to render. Like
// invoiceNoPaymentFallback it escapes every value and omits the lines.
func invoicePaymentFallback(reminder bool, memberName, clubName string, invoiceNumber int, dueDate, amount, kid, bankAccount, payURL string) string {
	intro := "Du har mottatt en ny faktura"
	if reminder {
		intro = "Påminnelse om ubetalt faktura"
	}
	return fmt.Sprintf(
		`<p>Hei %s,</p><p>%s (#%d) fra %s.</p><p>Forfall: %s · Beløp: %s · KID: %s · Konto: %s</p>%s`,
		html.EscapeString(memberName), intro, invoiceNumber, html.EscapeString(clubName), html.EscapeString(dueDate), html.EscapeString(amount),
		html.EscapeString(kid), html.EscapeString(bankAccount), fallbackPayLine(payURL),
	)
}

// InvoiceReminderSubject returns the Subject header for purring
// (reminder) emails referencing the original faktura.
func InvoiceReminderSubject(_, clubName string, invoiceNumber int) string {
	if clubName == "" {
		clubName = "klubben"
	}
	return fmt.Sprintf("Påminnelse om ubetalt faktura fra %s [Faktura %d]", clubName, invoiceNumber)
}

// InvoiceReminderBody renders a Norwegian purring email that explains
// the invoice is past due (or pending), restates the payment details,
// and notes the PDF copy is attached. The PDF is attached separately
// by the caller — same as InvoiceBody, as are payURL, payMethods and lines.
func InvoiceReminderBody(_ /*locale*/, memberName, clubName string, invoiceNumber int, dueDate time.Time, total float64, kid, bankAccount, payURL, payMethods string, lines []InvoiceLine) string {
	if clubName == "" {
		clubName = "klubben"
	}
	if memberName == "" {
		memberName = "medlem"
	}
	overdue := time.Now().After(dueDate)
	data := struct {
		Club          string
		Member        string
		InvoiceNumber int
		DueDate       string
		Amount        string
		KID           string
		BankAccount   string
		Overdue       bool
		PayURL        string
		PayMethods    string
		Lines         []emailLineView
		MoreLines     int
	}{
		Club:          clubName,
		Member:        memberName,
		InvoiceNumber: invoiceNumber,
		DueDate:       dueDate.Format("02.01.2006"),
		Amount:        formatNOK(total),
		KID:           kid,
		BankAccount:   bankAccount,
		Overdue:       overdue,
		PayURL:        payURL,
		PayMethods:    payMethods,
	}
	data.Lines, data.MoreLines = emailLines(lines)
	var buf bytes.Buffer
	if err := reminderHTMLTpl.ExecuteTemplate(&buf, "base", data); err != nil {
		return invoicePaymentFallback(true, memberName, clubName, invoiceNumber, data.DueDate, data.Amount, kid, bankAccount, payURL)
	}
	return buf.String()
}

// PayMethodsLabel names the ways to pay online for the line under an email's
// pay button: "Vipps", "Kort", "Vipps eller kort", or "" when neither is
// offered. It stands as a line of its own, hence the capital on "Kort".
// Norwegian, like the rest of the invoice emails.
func PayMethodsLabel(vipps, card bool) string {
	switch {
	case vipps && card:
		return "Vipps eller kort"
	case vipps:
		return "Vipps"
	case card:
		return "Kort"
	}
	return ""
}

// CreditNoteSubject is the Subject header for the email that carries a credit
// note.
func CreditNoteSubject(clubName string, number int) string {
	if clubName == "" {
		clubName = "klubben"
	}
	return fmt.Sprintf("Kreditnota fra %s [Kreditnota %d]", clubName, number)
}

// CreditNoteBody is the HTML body for the email that carries a credit note
// the treasurer issued. It asks for no payment. The PDF is attached by the
// caller.
func CreditNoteBody(memberName, clubName string, number int, total float64) string {
	if clubName == "" {
		clubName = "klubben"
	}
	if memberName == "" {
		memberName = "medlem"
	}
	return fmt.Sprintf(`<p>Hei %s,</p><p>Vedlagt er kreditnota #%d fra %s på %s. `+
		`Du skal ikke betale noe.</p>`,
		html.EscapeString(memberName), number, html.EscapeString(clubName), formatNOK(math.Abs(total)))
}

// RefundDocumentSubject is the Subject header for a document a card refund
// issued: a credit note (negative total), or the new faktura after a failed
// refund.
func RefundDocumentSubject(clubName string, number int, total float64) string {
	if total < 0 {
		return CreditNoteSubject(clubName, number)
	}
	if clubName == "" {
		clubName = "klubben"
	}
	return fmt.Sprintf("Ny faktura fra %s [Faktura %d]", clubName, number)
}

// RefundDocumentBody is the HTML body for a document a card refund issued.
// Nothing is to be paid either way: a credit note documents money already
// returned to the card, and the new faktura after a failed refund is
// settled by the money that came back. The PDF is attached by the caller.
func RefundDocumentBody(memberName, clubName string, number int, total float64) string {
	if clubName == "" {
		clubName = "klubben"
	}
	if memberName == "" {
		memberName = "medlem"
	}
	name, club, amount := html.EscapeString(memberName), html.EscapeString(clubName), formatNOK(math.Abs(total))
	if total < 0 {
		return fmt.Sprintf(`<p>Hei %s,</p><p>Vedlagt er kreditnota #%d fra %s på %s for kortbetalingen som er refundert. `+
			`Pengene kommer tilbake på kortet du betalte med, vanligvis innen 5–10 dager. Du skal ikke gjøre noe.</p>`,
			name, number, club, amount)
	}
	return fmt.Sprintf(`<p>Hei %s,</p><p>Refusjonen av kortbetalingen din kunne ikke gjennomføres, så pengene ble værende hos %s. `+
		`Vedlagt er faktura #%d på %s for beløpet. Den er allerede betalt, og du skal ikke betale noe.</p>`,
		name, club, number, amount)
}

// DisputeNotice is what the treasurer is told about a card dispute. Nothing
// here identifies the payer or the card: only the amount, what it was for,
// Stripe's reason code and the deadline.
type DisputeNotice struct {
	ClubName string
	// AmountOre is the disputed amount in øre; nil when Stripe did not say.
	AmountOre *int64
	// Reference says what was paid, such as "faktura 1042" or "ordre ab12cd34";
	// empty when the payment cannot be tied to one.
	Reference string
	// Reason is Stripe's reason code, such as "fraudulent".
	Reason string
	// EvidenceDueBy is the deadline for answering; nil when unknown.
	EvidenceDueBy *time.Time
	// DashboardURL is the dispute's page in the Stripe Dashboard.
	DashboardURL string
}

// disputeReasonLabels name Stripe's dispute reason codes in Norwegian.
var disputeReasonLabels = map[string]string{
	"bank_cannot_process":       "banken kunne ikke behandle betalingen",
	"check_returned":            "sjekk returnert",
	"credit_not_processed":      "kreditering ikke gjennomført",
	"customer_initiated":        "startet av kortholder",
	"debit_not_authorized":      "belastning ikke autorisert",
	"duplicate":                 "dobbeltbetaling",
	"fraudulent":                "svindel",
	"general":                   "generell",
	"incorrect_account_details": "feil kontodetaljer",
	"insufficient_funds":        "manglende dekning",
	"product_not_acceptable":    "varen ikke som avtalt",
	"product_not_received":      "varen ikke mottatt",
	"subscription_canceled":     "abonnement avsluttet",
	"unrecognized":              "kortholder kjenner ikke igjen belastningen",
}

func formatDisputeDeadline(t time.Time) string {
	return t.In(time.Local).Format("02.01.2006")
}

// DisputeSubject is the Subject header for the notice of a card dispute.
func DisputeSubject(dueBy *time.Time) string {
	if dueBy == nil {
		return "Omstridt kortbetaling"
	}
	return "Omstridt kortbetaling – svar innen " + formatDisputeDeadline(*dueBy)
}

// DisputeBody is the HTML body for the notice of a card dispute to the
// treasurer. The answer is given in the Stripe Dashboard; Brygge keeps no
// evidence.
func DisputeBody(n DisputeNotice) string {
	club := n.ClubName
	if club == "" {
		club = "klubben"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<p>Hei,</p><p>En kortbetaling til %s er omstridt: kortholder har bedt kortutstederen om å trekke pengene tilbake.</p><ul>`,
		html.EscapeString(club))
	if n.AmountOre != nil {
		fmt.Fprintf(&b, `<li>Beløp: %s</li>`, formatNOK(float64(*n.AmountOre)/100))
	}
	if n.Reference != "" {
		fmt.Fprintf(&b, `<li>Gjelder: %s</li>`, html.EscapeString(n.Reference))
	}
	if n.Reason != "" {
		label, ok := disputeReasonLabels[n.Reason]
		if !ok {
			label = n.Reason
		}
		fmt.Fprintf(&b, `<li>Årsak: %s</li>`, html.EscapeString(label))
	}
	if n.EvidenceDueBy != nil {
		fmt.Fprintf(&b, `<li>Frist for å svare: <strong>%s</strong></li>`, formatDisputeDeadline(*n.EvidenceDueBy))
	}
	b.WriteString(`</ul><p>Svar med dokumentasjon i Stripe. Svarer klubben ikke innen fristen, tapes beløpet, og Stripe tar et gebyr for tvisten.`)
	if n.DashboardURL != "" {
		u := html.EscapeString(n.DashboardURL)
		fmt.Fprintf(&b, ` Åpne saken: <a href="%s">%s</a>.`, u, u)
	}
	b.WriteString(`</p><p>Brygge bokfører tapet når Stripe trekker pengene, og fører det tilbake hvis klubben vinner. Fakturaen blir stående som betalt.</p>`)
	return b.String()
}

// ContactMessage is one submission of the public contact form. Name, Email
// and Subject are already single-line and free of control characters.
type ContactMessage struct {
	Name    string
	Email   string
	Subject string
	Message string
}

// ContactSubject is the Subject header for a contact-form message: a fixed
// prefix so the club can filter them, then the visitor's subject. It is cut
// to 60 runes because the Q-encoding is not folded: four-byte runes encode to
// twelve characters each, and 150 of them overran the 998-character header
// line limit of RFC 5322. The full subject is in the body.
func ContactSubject(subject string) string {
	const maxRunes = 60
	if r := []rune(subject); len(r) > maxRunes {
		subject = string(r[:maxRunes]) + "…"
	}
	return "[Kontaktskjema] " + subject
}

// ContactBody is the HTML body of a contact-form message to the club. Every
// visitor-supplied value is escaped; the message keeps its line breaks.
func ContactBody(m ContactMessage) string {
	var b strings.Builder
	b.WriteString(`<p>Ny melding fra kontaktskjemaet på nettsiden. Svar på denne e-posten for å svare avsenderen.</p><ul>`)
	fmt.Fprintf(&b, `<li>Navn: %s</li>`, html.EscapeString(m.Name))
	fmt.Fprintf(&b, `<li>E-post: %s</li>`, html.EscapeString(m.Email))
	fmt.Fprintf(&b, `<li>Emne: %s</li>`, html.EscapeString(m.Subject))
	b.WriteString(`</ul><p>`)
	b.WriteString(strings.ReplaceAll(html.EscapeString(m.Message), "\n", "<br>\n"))
	b.WriteString(`</p>`)
	return b.String()
}

// fallbackPayLine is the pay link in the flat summary sent when a template
// fails to render; empty without a link.
func fallbackPayLine(payURL string) string {
	if payURL == "" {
		return ""
	}
	u := html.EscapeString(payURL)
	return fmt.Sprintf(`<p>Betal nå: <a href="%s">%s</a></p>`, u, u)
}

// formatNOK renders a money amount as "kr 1 234,50" with Norwegian
// thousands grouping and decimal comma; a negative amount as "kr -650,00".
// It rounds to whole øre first, so 0.999 is "kr 1,00", not "kr 0,100".
func formatNOK(amount float64) string {
	ore := int64(math.Round(amount * 100))
	sign := ""
	if ore < 0 {
		sign, ore = "-", -ore
	}
	wholeStr := strconv.FormatInt(ore/100, 10)
	// Group with a space every 3 digits from the right.
	var grouped strings.Builder
	for i, r := range wholeStr {
		if i > 0 && (len(wholeStr)-i)%3 == 0 {
			grouped.WriteRune(' ')
		}
		grouped.WriteRune(r)
	}
	return fmt.Sprintf("kr %s%s,%02d", sign, grouped.String(), ore%100)
}

var reminderHTMLTpl = template.Must(template.Must(sharedEmail.Clone()).Parse(`{{define "content"}}
              <p style="margin:0 0 16px 0;">Hei {{.Member}},</p>
              {{if .Overdue}}
              <p style="margin:0 0 8px 0;color:#b91c1c;font-weight:600;">Påminnelse: faktura #{{.InvoiceNumber}} er forfalt.</p>
              <p style="margin:0 0 20px 0;">Vi minner om at fakturaen ikke er registrert som betalt. PDF-kopi følger som vedlegg.</p>
              {{else}}
              <p style="margin:0 0 8px 0;color:#b45309;font-weight:600;">Påminnelse om ubetalt faktura #{{.InvoiceNumber}}.</p>
              <p style="margin:0 0 20px 0;">Vi minner om forfallet under. PDF-kopi følger som vedlegg.</p>
              {{end}}
{{template "invoiceLines" .}}{{template "invoiceDetails" .}}
              <p style="margin:0 0 12px 0;font-size:13px;color:#334155;"><strong>Vi foretrekker bankoverføring med KID</strong> — det koster laget ingenting. Bruk KID-nummeret når du betaler i nettbanken.</p>
{{template "payButton" .}}
              <p style="margin:0 0 8px 0;font-size:13px;color:#64748b;">Hvis betalingen allerede er sendt, kan du se bort fra denne påminnelsen.</p>
              <p style="margin:0;font-size:13px;color:#64748b;">Spørsmål kan sendes til kasserer ved å svare på denne e-posten.</p>{{end}}`))

// invoiceNoPaymentHTMLTpl is sent for a faktura whose lines net to zero (a
// role exemption covers everything on it). It keeps the faktura number for
// reference but omits every payment detail.
var invoiceNoPaymentHTMLTpl = template.Must(template.Must(sharedEmail.Clone()).Parse(`{{define "content"}}
              <p style="margin:0 0 16px 0;">Hei {{.Member}},</p>
              <p style="margin:0 0 20px 0;">Du har mottatt faktura #{{.InvoiceNumber}} fra {{.Club}}. Den er fullt fritatt, så du skal <strong>ikke betale noe</strong>. Fakturaen ligger som PDF-vedlegg til orientering.</p>
{{template "invoiceLines" .}}              <p style="margin:0;font-size:13px;color:#64748b;">Spørsmål om fakturaen kan sendes til kasserer ved å svare på denne e-posten.</p>{{end}}`))

var invoiceHTMLTpl = template.Must(template.Must(sharedEmail.Clone()).Parse(`{{define "content"}}
              <p style="margin:0 0 16px 0;">Hei {{.Member}},</p>
              <p style="margin:0 0 20px 0;">Du har mottatt en ny faktura fra {{.Club}}. Den fullstendige fakturaen ligger som PDF-vedlegg til denne e-posten.</p>
{{template "invoiceLines" .}}{{template "invoiceDetails" .}}
              <p style="margin:0 0 12px 0;font-size:13px;color:#334155;"><strong>Vi foretrekker bankoverføring med KID</strong> — det koster laget ingenting. Bruk KID-nummeret når du betaler i nettbanken, så registreres innbetalingen automatisk.</p>
{{template "payButton" .}}
              <p style="margin:0;font-size:13px;color:#64748b;">Spørsmål om fakturaen kan sendes til kasserer ved å svare på denne e-posten.</p>{{end}}`))
