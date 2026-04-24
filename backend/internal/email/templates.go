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
// The clock time is appended so consecutive emails get unique subjects and
// Gmail (and similar) doesn't collapse them into a single thread.
func MagicLinkSubject(locale, clubName string, now time.Time) string {
	ts := now.Format("15:04")
	tpl, ok := magicLinkSubjects[locale]
	if !ok {
		tpl = magicLinkSubjects[defaultLocale]
	}
	if clubName == "" {
		clubName = tpl.fallbackClub
	}
	return fmt.Sprintf("%s %s · %s", tpl.verb, clubName, ts)
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
