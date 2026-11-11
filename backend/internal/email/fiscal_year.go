package email

import (
	"fmt"
	"time"
)

// --- Payments waiting for a fiscal year (BRY-575) ---
//
// No automatic booking pass opens a fiscal year or books into a closed one,
// so a payment dated in such a year waits. The treasurer (else the board) is
// told once per year and state, in Bokmål or Nynorsk by the club's default
// language (JoinLocale), with a link that opens the right action on the Bilag
// page.

// Fiscal year waiting states, as accounting.WaitingFiscalYear.State.
const (
	FiscalYearMissing = "missing"
	FiscalYearClosed  = "closed"
	FiscalYearLocked  = "locked"
)

// FiscalYearWaiting is what the notice is rendered from.
type FiscalYearWaiting struct {
	ClubName string
	Year     int
	// State is FiscalYearMissing, FiscalYearClosed or FiscalYearLocked.
	State string
	// Payments is how many payments wait; Oldest the earliest one's date.
	Payments int
	Oldest   time.Time
	// URL opens the action on the Bilag page.
	URL string
}

type fiscalYearCopyT struct {
	Subject string // fmt: year, then the count line
	// One and Many are the count in the subject ("1 betaling venter").
	One, Many string
	// BodyOne and BodyMany: count (Many only), year, year.
	BodyOne, BodyMany string
	Oldest            string // fmt: date
	Next              string
	CTA               string // fmt: year
	FallbackClub      string
}

var fiscalYearMissingCopy = map[string]fiscalYearCopyT{
	"nb": {
		Subject:      "Opprett regnskapsåret %d",
		BodyOne:      "1 betaling datert i %d venter fordi regnskapsåret %d ikke finnes. Automatisk bokføring oppretter aldri et regnskapsår selv.",
		BodyMany:     "%d betalinger datert i %d venter fordi regnskapsåret %d ikke finnes. Automatisk bokføring oppretter aldri et regnskapsår selv.",
		Oldest:       "Den eldste er fra %s.",
		Next:         "Opprett året på Bilag-siden. Neste synkronisering bokfører da betalingene i sitt eget år. Lukk det gamle året først når det nye finnes.",
		CTA:          "Opprett regnskapsåret %d",
		FallbackClub: "klubben",
	},
	"nn": {
		Subject:      "Opprett rekneskapsåret %d",
		BodyOne:      "1 betaling datert i %d ventar fordi rekneskapsåret %d ikkje finst. Automatisk bokføring opprettar aldri eit rekneskapsår sjølv.",
		BodyMany:     "%d betalingar datert i %d ventar fordi rekneskapsåret %d ikkje finst. Automatisk bokføring opprettar aldri eit rekneskapsår sjølv.",
		Oldest:       "Den eldste er frå %s.",
		Next:         "Opprett året på Bilag-sida. Neste synkronisering bokfører då betalingane i sitt eige år. Lukk det gamle året først når det nye finst.",
		CTA:          "Opprett rekneskapsåret %d",
		FallbackClub: "klubben",
	},
}

var fiscalYearClosedCopy = map[string]fiscalYearCopyT{
	"nb": {
		Subject:      "Regnskapsåret %d er lukket – %s",
		One:          "1 betaling venter",
		Many:         "%d betalinger venter",
		BodyOne:      "1 betaling datert i %d venter fordi regnskapsåret %d er lukket. Den bokføres ikke før året er åpnet igjen.",
		BodyMany:     "%d betalinger datert i %d venter fordi regnskapsåret %d er lukket. De bokføres ikke før året er åpnet igjen.",
		Oldest:       "Den eldste er fra %s.",
		Next:         "Gjenåpne året på Bilag-siden (du blir bedt om å bekrefte deg på nytt), la neste synkronisering bokføre betalingene, og lukk året igjen etterpå.",
		CTA:          "Gjenåpne regnskapsåret %d",
		FallbackClub: "klubben",
	},
	"nn": {
		Subject:      "Rekneskapsåret %d er lukka – %s",
		One:          "1 betaling ventar",
		Many:         "%d betalingar ventar",
		BodyOne:      "1 betaling datert i %d ventar fordi rekneskapsåret %d er lukka. Ho blir ikkje bokført før året er opna igjen.",
		BodyMany:     "%d betalingar datert i %d ventar fordi rekneskapsåret %d er lukka. Dei blir ikkje bokførte før året er opna igjen.",
		Oldest:       "Den eldste er frå %s.",
		Next:         "Opna året igjen på Bilag-sida (du blir beden om å stadfeste deg på nytt), la neste synkronisering bokføre betalingane, og lukk året igjen etterpå.",
		CTA:          "Opna rekneskapsåret %d igjen",
		FallbackClub: "klubben",
	},
}

var fiscalYearLockedCopy = map[string]fiscalYearCopyT{
	"nb": {
		Subject:      "Regnskapsåret %d er låst – %s",
		One:          "1 betaling venter",
		Many:         "%d betalinger venter",
		BodyOne:      "1 betaling datert i %d venter fordi regnskapsåret %d er låst.",
		BodyMany:     "%d betalinger datert i %d venter fordi regnskapsåret %d er låst.",
		Oldest:       "Den eldste er fra %s.",
		Next:         "Et låst år kan ikke åpnes igjen i Brygge. Det må låses opp utenfor appen før betalingene kan bokføres; snakk med den som drifter Brygge for klubben.",
		CTA:          "Vis regnskapsåret %d",
		FallbackClub: "klubben",
	},
	"nn": {
		Subject:      "Rekneskapsåret %d er låst – %s",
		One:          "1 betaling ventar",
		Many:         "%d betalingar ventar",
		BodyOne:      "1 betaling datert i %d ventar fordi rekneskapsåret %d er låst.",
		BodyMany:     "%d betalingar datert i %d ventar fordi rekneskapsåret %d er låst.",
		Oldest:       "Den eldste er frå %s.",
		Next:         "Eit låst år kan ikkje opnast igjen i Brygge. Det må låsast opp utanfor appen før betalingane kan bokførast; snakk med den som driftar Brygge for klubben.",
		CTA:          "Vis rekneskapsåret %d",
		FallbackClub: "klubben",
	},
}

func fiscalYearCopy(state, locale string) fiscalYearCopyT {
	copies := fiscalYearMissingCopy
	switch state {
	case FiscalYearClosed:
		copies = fiscalYearClosedCopy
	case FiscalYearLocked:
		copies = fiscalYearLockedCopy
	}
	c, ok := copies[locale]
	if !ok {
		c = copies[defaultLocale]
	}
	return c
}

// FiscalYearWaitingSubject is the Subject of the treasurer's notice.
func FiscalYearWaitingSubject(locale string, n FiscalYearWaiting) string {
	c := fiscalYearCopy(n.State, locale)
	if n.State == FiscalYearMissing || n.State == "" {
		return fmt.Sprintf(c.Subject, n.Year)
	}
	count := c.One
	if n.Payments != 1 {
		count = fmt.Sprintf(c.Many, n.Payments)
	}
	return fmt.Sprintf(c.Subject, n.Year, count)
}

// FiscalYearWaitingBody is its HTML body.
func FiscalYearWaitingBody(locale string, n FiscalYearWaiting) string {
	c := fiscalYearCopy(n.State, locale)
	club := n.ClubName
	if club == "" {
		club = c.FallbackClub
	}
	body := fmt.Sprintf(c.BodyOne, n.Year, n.Year)
	if n.Payments != 1 {
		body = fmt.Sprintf(c.BodyMany, n.Payments, n.Year, n.Year)
	}
	if !n.Oldest.IsZero() {
		body += " " + fmt.Sprintf(c.Oldest, norDate(n.Oldest))
	}
	return renderMembership(membershipData{
		Club: club, Body: body, Next: c.Next,
		URL: n.URL, CTA: fmt.Sprintf(c.CTA, n.Year),
	})
}
