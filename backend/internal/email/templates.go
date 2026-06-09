package email

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
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
		LoginURL template.URL
		Copy     magicLinkCopyT
	}{
		Club:     clubName,
		Domain:   domain,
		LoginURL: template.URL(loginURL),
		Copy:     rendered,
	}
	var buf bytes.Buffer
	if err := magicLinkHTMLTpl.Execute(&buf, data); err != nil {
		// template bug — fall back to a plain anchor so the user can still log in
		return fmt.Sprintf(`<p><a href=%q>%s</a></p>`, loginURL, tpl.CTA)
	}
	return buf.String()
}

type magicLinkCopyT struct {
	Subject        string // fmt string with %s for club name
	Greeting       string
	Explanation    string // "Someone requested a login link for {Club}"
	CTA            string // button text
	LinkIntro      string // "Or copy this link..."
	Expiry         string
	Ignore         string // "If you didn't request this..."
	FallbackClub   string
	FooterContact  string // shown beneath the divider
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

var magicLinkHTMLTpl = template.Must(template.New("magic_link").Parse(`<!DOCTYPE html>
<html>
<body style="margin:0;padding:0;background:#f5f5f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
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
              <p style="margin:0 0 16px 0;">{{.Copy.Greeting}}</p>
              <p style="margin:0 0 24px 0;">{{.Copy.Explanation}}</p>
              <p style="margin:0 0 24px 0;text-align:center;">
                <a href="{{.LoginURL}}" style="display:inline-block;background:#0f172a;color:#ffffff;text-decoration:none;padding:12px 28px;border-radius:4px;font-weight:600;font-size:15px;">{{.Copy.CTA}}</a>
              </p>
              <p style="margin:0 0 8px 0;font-size:13px;color:#64748b;">{{.Copy.LinkIntro}}</p>
              <p style="margin:0 0 24px 0;font-family:ui-monospace,Menlo,Consolas,monospace;font-size:12px;color:#475569;word-break:break-all;background:#f8fafc;padding:10px 12px;border-radius:4px;">{{.LoginURL}}</p>
              <p style="margin:0 0 8px 0;font-size:13px;color:#64748b;">{{.Copy.Expiry}}</p>
              <p style="margin:0;font-size:13px;color:#64748b;">{{.Copy.Ignore}}</p>
            </td>
          </tr>
          <tr>
            <td style="padding:20px 32px;border-top:1px solid #e5e7eb;background:#f8fafc;color:#64748b;font-size:12px;line-height:1.5;border-radius:0 0 6px 6px;">
              <p style="margin:0 0 6px 0;">{{.Copy.FooterContact}}</p>
              <p style="margin:0;"><strong style="color:#334155;">{{.Club}}</strong> · <a href="https://{{.Domain}}" style="color:#64748b;text-decoration:underline;">{{.Domain}}</a></p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`))

// --- Invoice email ---
//
// Visually mirrors the magic-link template (same card chrome, same
// header/body/footer rhythm) so members get a consistent look from the
// club. Norwegian only for now — the locale arg is accepted and
// ignored so existing callers don't need to change. Localization will
// be added back when translations are ready.

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
func InvoiceBody(_ /*locale*/, memberName, clubName string, invoiceNumber int, dueDate time.Time, total float64, kid, bankAccount string) string {
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
	}{
		Club:          clubName,
		Member:        memberName,
		InvoiceNumber: invoiceNumber,
		DueDate:       dueDate.Format("02.01.2006"),
		Amount:        formatNOK(total),
		KID:           kid,
		BankAccount:   bankAccount,
	}
	var buf bytes.Buffer
	if err := invoiceHTMLTpl.Execute(&buf, data); err != nil {
		// Template bug — fall back to a flat readable summary so the
		// member still has the payment details.
		return fmt.Sprintf(
			`<p>Hei %s,</p><p>Du har mottatt en ny faktura (#%d) fra %s.</p><p>Forfall: %s · Beløp: %s · KID: %s · Konto: %s</p>`,
			memberName, invoiceNumber, clubName, data.DueDate, data.Amount, kid, bankAccount,
		)
	}
	return buf.String()
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
// by the caller — same as InvoiceBody.
func InvoiceReminderBody(_ /*locale*/, memberName, clubName string, invoiceNumber int, dueDate time.Time, total float64, kid, bankAccount string) string {
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
	}{
		Club:          clubName,
		Member:        memberName,
		InvoiceNumber: invoiceNumber,
		DueDate:       dueDate.Format("02.01.2006"),
		Amount:        formatNOK(total),
		KID:           kid,
		BankAccount:   bankAccount,
		Overdue:       overdue,
	}
	var buf bytes.Buffer
	if err := reminderHTMLTpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf(
			`<p>Hei %s,</p><p>Påminnelse om ubetalt faktura (#%d) fra %s.</p><p>Forfall: %s · Beløp: %s · KID: %s · Konto: %s</p>`,
			memberName, invoiceNumber, clubName, data.DueDate, data.Amount, kid, bankAccount,
		)
	}
	return buf.String()
}

// formatNOK renders a money amount as "kr 1 234,50" with Norwegian
// thousands grouping and decimal comma.
func formatNOK(amount float64) string {
	whole := int64(amount)
	cents := int64((amount-float64(whole))*100 + 0.5)
	if cents < 0 {
		cents = -cents
	}
	wholeStr := fmt.Sprintf("%d", whole)
	// Insert non-breaking spaces every 3 digits from the right.
	var grouped strings.Builder
	for i, r := range wholeStr {
		if i > 0 && (len(wholeStr)-i)%3 == 0 {
			grouped.WriteRune(' ')
		}
		grouped.WriteRune(r)
	}
	return fmt.Sprintf("kr %s,%02d", grouped.String(), cents)
}

var reminderHTMLTpl = template.Must(template.New("reminder").Parse(`<!DOCTYPE html>
<html>
<body style="margin:0;padding:0;background:#f5f5f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
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
              <p style="margin:0 0 16px 0;">Hei {{.Member}},</p>
              {{if .Overdue}}
              <p style="margin:0 0 8px 0;color:#b91c1c;font-weight:600;">Påminnelse: faktura #{{.InvoiceNumber}} er forfalt.</p>
              <p style="margin:0 0 20px 0;">Vi minner om at fakturaen ikke er registrert som betalt. PDF-kopi følger som vedlegg.</p>
              {{else}}
              <p style="margin:0 0 8px 0;color:#b45309;font-weight:600;">Påminnelse om ubetalt faktura #{{.InvoiceNumber}}.</p>
              <p style="margin:0 0 20px 0;">Vi minner om forfallet under. PDF-kopi følger som vedlegg.</p>
              {{end}}

              <table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="margin:0 0 24px 0;background:#f8fafc;border-radius:6px;">
                <tr>
                  <td style="padding:14px 16px;font-size:14px;color:#64748b;width:40%;">Fakturanummer</td>
                  <td style="padding:14px 16px;font-size:14px;color:#0f172a;font-family:ui-monospace,Menlo,Consolas,monospace;">#{{.InvoiceNumber}}</td>
                </tr>
                <tr>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#64748b;">Forfallsdato</td>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#0f172a;font-weight:600;">{{.DueDate}}</td>
                </tr>
                <tr>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#64748b;">Beløp å betale</td>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#0f172a;font-weight:600;">{{.Amount}}</td>
                </tr>
                <tr>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#64748b;">KID</td>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#0f172a;font-family:ui-monospace,Menlo,Consolas,monospace;">{{.KID}}</td>
                </tr>
                <tr>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#64748b;">Kontonummer</td>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#0f172a;font-family:ui-monospace,Menlo,Consolas,monospace;">{{.BankAccount}}</td>
                </tr>
              </table>

              <p style="margin:0 0 8px 0;font-size:13px;color:#64748b;">Hvis betalingen allerede er sendt, kan du se bort fra denne påminnelsen.</p>
              <p style="margin:0;font-size:13px;color:#64748b;">Spørsmål kan sendes til kasserer ved å svare på denne e-posten.</p>
            </td>
          </tr>
          <tr>
            <td style="padding:20px 32px;border-top:1px solid #e5e7eb;background:#f8fafc;color:#64748b;font-size:12px;line-height:1.5;border-radius:0 0 6px 6px;">
              <p style="margin:0;"><strong style="color:#334155;">{{.Club}}</strong></p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`))

var invoiceHTMLTpl = template.Must(template.New("invoice").Parse(`<!DOCTYPE html>
<html>
<body style="margin:0;padding:0;background:#f5f5f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">
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
              <p style="margin:0 0 16px 0;">Hei {{.Member}},</p>
              <p style="margin:0 0 20px 0;">Du har mottatt en ny faktura fra {{.Club}}. Den fullstendige fakturaen ligger som PDF-vedlegg til denne e-posten.</p>

              <table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="margin:0 0 24px 0;background:#f8fafc;border-radius:6px;">
                <tr>
                  <td style="padding:14px 16px;font-size:14px;color:#64748b;width:40%;">Fakturanummer</td>
                  <td style="padding:14px 16px;font-size:14px;color:#0f172a;font-family:ui-monospace,Menlo,Consolas,monospace;">#{{.InvoiceNumber}}</td>
                </tr>
                <tr>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#64748b;">Forfallsdato</td>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#0f172a;font-weight:600;">{{.DueDate}}</td>
                </tr>
                <tr>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#64748b;">Beløp å betale</td>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#0f172a;font-weight:600;">{{.Amount}}</td>
                </tr>
                <tr>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#64748b;">KID</td>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#0f172a;font-family:ui-monospace,Menlo,Consolas,monospace;">{{.KID}}</td>
                </tr>
                <tr>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#64748b;">Kontonummer</td>
                  <td style="padding:0 16px 14px 16px;font-size:14px;color:#0f172a;font-family:ui-monospace,Menlo,Consolas,monospace;">{{.BankAccount}}</td>
                </tr>
              </table>

              <p style="margin:0 0 8px 0;font-size:13px;color:#64748b;">Bruk KID-nummeret når du betaler i nettbanken slik at innbetalingen registreres automatisk.</p>
              <p style="margin:0;font-size:13px;color:#64748b;">Spørsmål om fakturaen kan sendes til kasserer ved å svare på denne e-posten.</p>
            </td>
          </tr>
          <tr>
            <td style="padding:20px 32px;border-top:1px solid #e5e7eb;background:#f8fafc;color:#64748b;font-size:12px;line-height:1.5;border-radius:0 0 6px 6px;">
              <p style="margin:0;"><strong style="color:#334155;">{{.Club}}</strong></p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`))
