package accounting

// AccountType represents the category of an account in the chart of accounts.
type AccountType string

const (
	AccountTypeAsset     AccountType = "asset"
	AccountTypeLiability AccountType = "liability"
	AccountTypeRevenue   AccountType = "revenue"
	AccountTypeExpense   AccountType = "expense"
)

// MVAEligibility indicates whether expenses posted to an account
// qualify for momskompensasjon (VAT compensation for non-profits).
type MVAEligibility string

const (
	MVAEligible      MVAEligibility = "eligible"
	MVAIneligible    MVAEligibility = "ineligible"
	MVAPartial       MVAEligibility = "partial"
	MVANotApplicable MVAEligibility = "not_applicable"
)

// AccountDef defines a default account in the kontoplan.
type AccountDef struct {
	Code         string
	Name         string
	Type         AccountType
	ParentCode   string
	MVAEligible  MVAEligibility
	Description  string
	SortOrder    int
}

// DefaultKontoplan returns the default chart of accounts for a Norwegian
// boat club, based on NS 4102 simplified. Each account has an MVA eligibility
// flag that determines whether expenses qualify for momskompensasjon.
func DefaultKontoplan() []AccountDef {
	return []AccountDef{
		// Assets (Eiendeler)
		{Code: "1500", Name: "Kundefordringer", Type: AccountTypeAsset, ParentCode: "1000", MVAEligible: MVANotApplicable, SortOrder: 10},
		{Code: "1900", Name: "Kontanter/kasse", Type: AccountTypeAsset, ParentCode: "1000", MVAEligible: MVANotApplicable, SortOrder: 20},
		{Code: "1920", Name: "Bankkonto drift", Type: AccountTypeAsset, ParentCode: "1000", MVAEligible: MVANotApplicable, SortOrder: 30},

		// Liabilities & Equity (Gjeld og egenkapital)
		{Code: "2000", Name: "Egenkapital", Type: AccountTypeLiability, ParentCode: "2000", MVAEligible: MVANotApplicable, SortOrder: 40},
		{Code: "2050", Name: "Annen egenkapital", Type: AccountTypeLiability, ParentCode: "2000", MVAEligible: MVANotApplicable, SortOrder: 50},
		{Code: "2400", Name: "Leverandørgjeld", Type: AccountTypeLiability, ParentCode: "2000", MVAEligible: MVANotApplicable, SortOrder: 60},
		{Code: "2900", Name: "Annen kortsiktig gjeld", Type: AccountTypeLiability, ParentCode: "2000", MVAEligible: MVANotApplicable, SortOrder: 70},

		// Revenue (Inntekter)
		{Code: "3100", Name: "Medlemskontingent", Type: AccountTypeRevenue, ParentCode: "3000", MVAEligible: MVANotApplicable, SortOrder: 80},
		{Code: "3110", Name: "Havneavgift", Type: AccountTypeRevenue, ParentCode: "3000", MVAEligible: MVANotApplicable, SortOrder: 90},
		{Code: "3120", Name: "Plassleie", Type: AccountTypeRevenue, ParentCode: "3000", MVAEligible: MVANotApplicable, SortOrder: 100},
		{Code: "3200", Name: "Gjestehavninntekter", Type: AccountTypeRevenue, ParentCode: "3000", MVAEligible: MVANotApplicable, SortOrder: 110},
		{Code: "3300", Name: "Salgsinntekter", Type: AccountTypeRevenue, ParentCode: "3000", MVAEligible: MVANotApplicable, SortOrder: 120},
		{Code: "3400", Name: "Momskompensasjon", Type: AccountTypeRevenue, ParentCode: "3000", MVAEligible: MVANotApplicable, SortOrder: 130},
		{Code: "3900", Name: "Andre inntekter", Type: AccountTypeRevenue, ParentCode: "3000", MVAEligible: MVANotApplicable, SortOrder: 140},

		// Expenses (Kostnader) — MVA eligibility determines momskompensasjon qualification
		{Code: "4300", Name: "Innkjøp varer for salg", Type: AccountTypeExpense, ParentCode: "4000", MVAEligible: MVAIneligible, Description: "Varer kjøpt for videresalg (kiosk, merch)", SortOrder: 150},
		{Code: "5000", Name: "Lønn og godtgjørelser", Type: AccountTypeExpense, ParentCode: "5000", MVAEligible: MVAEligible, Description: "Honorar, lønn til ansatte", SortOrder: 160},
		{Code: "5400", Name: "Arbeidsgiveravgift", Type: AccountTypeExpense, ParentCode: "5000", MVAEligible: MVAEligible, SortOrder: 170},
		{Code: "6100", Name: "Vedlikehold bryggeanlegg", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAIneligible, Description: "Brygge, fortøyning, utriggere — ikke momskompensasjon", SortOrder: 180},
		{Code: "6110", Name: "Vedlikehold slipp/kran", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAIneligible, Description: "Slipp, kran, løfteutstyr — ikke momskompensasjon", SortOrder: 190},
		{Code: "6200", Name: "Vedlikehold klubbhus", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAEligible, Description: "Klubbhus, fellesarealer — kvalifiserer for momskompensasjon", SortOrder: 200},
		{Code: "6300", Name: "Strøm og oppvarming", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAPartial, Description: "Delt mellom klubbhus (kompensasjon) og bryggeanlegg (ikke)", SortOrder: 210},
		{Code: "6400", Name: "Leie av lokaler", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAEligible, SortOrder: 220},
		{Code: "6500", Name: "Forsikring", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAEligible, Description: "Ansvarsforsikring, bygningsforsikring", SortOrder: 230},
		{Code: "6600", Name: "Kontorrekvisita", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAEligible, SortOrder: 240},
		{Code: "6700", Name: "IT og programvare", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAEligible, Description: "Nettside, programvare, domener", SortOrder: 250},
		{Code: "6800", Name: "Telefon og internett", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAEligible, SortOrder: 260},
		{Code: "6900", Name: "Porto og frakt", Type: AccountTypeExpense, ParentCode: "6000", MVAEligible: MVAEligible, SortOrder: 270},
		{Code: "7100", Name: "Reise- og møtekostnader", Type: AccountTypeExpense, ParentCode: "7000", MVAEligible: MVAEligible, SortOrder: 280},
		{Code: "7300", Name: "Arrangementer og aktiviteter", Type: AccountTypeExpense, ParentCode: "7000", MVAEligible: MVAEligible, Description: "Sosiale arrangementer, kurs, regattaer", SortOrder: 290},
		{Code: "7500", Name: "Gaver og kontingenter", Type: AccountTypeExpense, ParentCode: "7000", MVAEligible: MVAEligible, Description: "Forbundskontingent, gaver til frivillige", SortOrder: 300},
		{Code: "7700", Name: "Bankgebyrer", Type: AccountTypeExpense, ParentCode: "7000", MVAEligible: MVAIneligible, SortOrder: 310},
		{Code: "7790", Name: "Andre driftskostnader", Type: AccountTypeExpense, ParentCode: "7000", MVAEligible: MVAEligible, SortOrder: 320},
	}
}
