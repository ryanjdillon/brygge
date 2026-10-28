package accounting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// BRY-380: bank rows that only make sense together (a buy-back payout and
// the overpayment the recipient sent back, a payment and its refund) are
// suggested as partners in Tildel and can be booked as one bilag.
//
// One candidate loader (loadPartnerRows) and one pure scorer
// (scorePartner) feed every consumer: the /partners endpoint, the
// partner_hint on the unmatched list and the guard that refuses to book a
// row with a strong partner on its own. They therefore always agree.

const (
	partnerWindowDays = 60
	reverseWindowDays = 14
	maxPartners       = 3
)

// Partner strengths. A strong partner gates single-row booking; a weak one
// is informational.
const (
	PartnerStrong = "strong"
	PartnerWeak   = "weak"
)

// Partner reasons, in the order the UI lists them.
const (
	partnerSameAccount = "same_account"
	partnerSameName    = "same_name"
	partnerReverses    = "reverses"
	partnerNetsToPrice = "nets_to_price"
)

// Reasons a group booking is refused. Handlers answer 400 with the message.
var (
	ErrGroupRowCount          = fmt.Errorf("ei samla tildeling må ha mellom %d og %d rader", minGroupRows, maxGroupRows)
	ErrGroupTargetAccount     = errors.New("vel ein konto som ikkje er ein bankkonto eller kundefordringar")
	ErrGroupRowNotBookable    = errors.New("ei av radene er avvist eller foreløpig")
	ErrGroupMixedBankAccounts = errors.New("radene må høyra til same bankkonto")
	ErrGroupNetZero           = errors.New("radene går i null: dette er ei overføring. Bruk Synk bank, eller avvis radene som intern overføring")
	ErrGroupCrossesPeriod     = errors.New("radene ligg i ulike rekneskapsår; bokfør dei kvar for seg")
	ErrGroupPeriodClosed      = errors.New("rekneskapsperioden er lukka")
)

// ErrBankRowHasPartner is returned when a row with a strong partner is
// booked on its own without the operator confirming it.
var ErrBankRowHasPartner = errors.New("bank row has a likely partner row")

// PartnerSuggestion is a row suggested as belonging with another row.
type PartnerSuggestion struct {
	ID          string    `json:"id"`
	RowDate     time.Time `json:"row_date"`
	Amount      float64   `json:"amount"`
	Counterpart string    `json:"counterpart"`
	Description string    `json:"description"`
	Strength    string    `json:"strength"`
	Reasons     []string  `json:"reasons"`
	DaysApart   int       `json:"days_apart"`
	// Net is the two rows' sum: what one bilag for both would book.
	Net float64 `json:"net"`
}

// partnerRow is a bank row as the scorer sees it. Amounts are in øre.
type partnerRow struct {
	ID              string
	Date            time.Time
	Cents           int64
	Counterpart     string
	Description     string
	FromAccount     string
	ToAccount       string
	BankAccountCode string
	// Eligible is false for rows that never get partners: booked,
	// dismissed, refund-paired, provisional or a provider payout deposit.
	Eligible bool
}

// otherParty is the counterparty's account number, digits only: the
// recipient for an outbound row, the payer for an inbound one.
func (r partnerRow) otherParty() string {
	if r.Cents < 0 {
		return digitsOnly(r.ToAccount)
	}
	return digitsOnly(r.FromAccount)
}

// partnerScoring is the per-club context the scorer needs.
type partnerScoring struct {
	clubName    string          // normalised
	ownAccounts map[string]bool // the club's own account numbers, digits only
	priceCents  map[int64]bool  // active price item amounts
}

// isOwn reports whether a row is the club moving money between its own
// accounts, which is never a partner pair (Synk bank books those).
//
// A known counterparty account number decides it: an inbound row's Motpart
// often carries the club's own account label even when the payer's account
// is another party's. The name test applies only when no account is known.
func (sc partnerScoring) isOwn(r partnerRow) bool {
	if acct := r.otherParty(); acct != "" {
		return sc.ownAccounts[acct]
	}
	return sc.isClubName(r.Counterpart)
}

func (sc partnerScoring) isClubName(name string) bool {
	return sc.clubName != "" && strings.Contains(normalizeName(name), sc.clubName)
}

// partyName is the row's counterparty name for matching, or "" when it is
// the club's own account label rather than the other party.
func (sc partnerScoring) partyName(r partnerRow) string {
	if sc.isClubName(r.Counterpart) {
		return ""
	}
	return r.Counterpart
}

// scorePartner decides whether b is a partner of a. Signals:
//
//   - same_account: the other party's account number is equal.
//   - same_name: the other party's names share two words (or are the same
//     single word), ignoring company suffixes.
//   - reverses: the amounts are equal and opposite within 14 days.
//   - nets_to_price: the rows net to an active price item's amount.
//
// Any of the first three makes the partner strong; nets_to_price alone is
// weak. Both rows must be eligible, on the same bank account, of opposite
// sign, within 60 days of each other and not the club's own transfer.
func scorePartner(a, b partnerRow, sc partnerScoring) (PartnerSuggestion, bool) {
	if a.ID == b.ID || !a.Eligible || !b.Eligible {
		return PartnerSuggestion{}, false
	}
	if a.BankAccountCode != b.BankAccountCode || (a.Cents < 0) == (b.Cents < 0) || a.Cents == 0 || b.Cents == 0 {
		return PartnerSuggestion{}, false
	}
	days := daysBetween(a.Date, b.Date)
	if days > partnerWindowDays || sc.isOwn(a) || sc.isOwn(b) {
		return PartnerSuggestion{}, false
	}

	var reasons []string
	if acct := a.otherParty(); acct != "" && acct == b.otherParty() {
		reasons = append(reasons, partnerSameAccount)
	}
	if namesMatch(sc.partyName(a), sc.partyName(b)) {
		reasons = append(reasons, partnerSameName)
	}
	absA, absB := abs64(a.Cents), abs64(b.Cents)
	if absA == absB && days <= reverseWindowDays {
		reasons = append(reasons, partnerReverses)
	}
	strength := PartnerStrong
	if len(reasons) == 0 {
		strength = PartnerWeak
	}
	if diff := abs64(absA - absB); diff > 0 && sc.priceCents[diff] {
		reasons = append(reasons, partnerNetsToPrice)
	}
	if len(reasons) == 0 {
		return PartnerSuggestion{}, false
	}
	return PartnerSuggestion{
		ID:          b.ID,
		RowDate:     b.Date,
		Amount:      float64(b.Cents) / 100,
		Counterpart: b.Counterpart,
		Description: b.Description,
		Strength:    strength,
		Reasons:     reasons,
		DaysApart:   days,
		Net:         float64(a.Cents+b.Cents) / 100,
	}, true
}

// rankPartners scores every pool row against a and returns the best few:
// strong before weak, then the closest in date.
func rankPartners(a partnerRow, pool []partnerRow, sc partnerScoring) []PartnerSuggestion {
	out := []PartnerSuggestion{}
	for _, b := range pool {
		if s, ok := scorePartner(a, b, sc); ok {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Strength == PartnerStrong) != (out[j].Strength == PartnerStrong) {
			return out[i].Strength == PartnerStrong
		}
		if out[i].DaysApart != out[j].DaysApart {
			return out[i].DaysApart < out[j].DaysApart
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > maxPartners {
		out = out[:maxPartners]
	}
	return out
}

// companySuffixes are dropped before comparing names: two different firms
// would otherwise share "as".
var companySuffixes = map[string]bool{
	"as": true, "asa": true, "ab": true, "sa": true, "da": true,
	"ba": true, "ans": true, "ks": true, "nuf": true,
}

func nameWords(s string) map[string]bool {
	words := map[string]bool{}
	for _, w := range strings.Fields(normalizeName(s)) {
		if len(w) < 2 || companySuffixes[w] {
			continue
		}
		words[w] = true
	}
	return words
}

// namesMatch reports whether two counterparty names are the same party:
// at least two shared words in any order (so "Nordmann Ola" matches "Ola
// Johan Nordmann"), or the same single word when either name has only one.
func namesMatch(a, b string) bool {
	wa, wb := nameWords(a), nameWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	if len(wa) == 1 || len(wb) == 1 {
		return len(wa) == 1 && len(wb) == 1 && shared == 1
	}
	return shared >= 2
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() < 6 {
		return ""
	}
	return b.String()
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func daysBetween(a, b time.Time) int {
	d := int(dayOf(a).Sub(dayOf(b)).Hours() / 24)
	if d < 0 {
		return -d
	}
	return d
}

// partnerEligibleSQL is the predicate for a row that can have partners,
// over the bank row alias a. It leaves out Vipps settlement deposits; the
// loaders drop Stripe payout deposits in Go (providerPayouts), since the
// club's descriptor is a Go regular expression.
func partnerEligibleSQL(a string) string {
	return a + `.journal_entry_id IS NULL
	AND ` + a + `.dismissed_at IS NULL
	AND ` + a + `.refund_paired_with IS NULL
	AND NOT (` + vippsPayoutRowSQL(a) + `)
	AND NOT COALESCE((` + provisionalRowSQL(a) + `), false)`
}

const partnerRowColumns = `bir.id, bir.row_date, round(bir.amount * 100)::bigint,
	bir.counterpart, bir.description, bir.from_account, bir.to_account,
	bi.bank_account_code`

func scanPartnerRows(rows pgx.Rows, withEligible bool) ([]partnerRow, error) {
	defer rows.Close()
	var out []partnerRow
	for rows.Next() {
		var r partnerRow
		dest := []any{&r.ID, &r.Date, &r.Cents, &r.Counterpart, &r.Description,
			&r.FromAccount, &r.ToAccount, &r.BankAccountCode}
		if withEligible {
			dest = append(dest, &r.Eligible)
		} else {
			r.Eligible = true
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadPartnerRows loads the anchors (any state, club-scoped) and every
// eligible row of the club within the partner window of any anchor.
func (s *Service) loadPartnerRows(ctx context.Context, q pgxQuerier, clubID string, ids []string) (anchors, pool []partnerRow, err error) {
	rows, err := q.Query(ctx,
		`SELECT `+partnerRowColumns+`, (`+partnerEligibleSQL("bir")+`)
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.club_id = $1 AND bir.id = ANY($2::uuid[])`,
		clubID, ids,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("load anchor rows: %w", err)
	}
	if anchors, err = scanPartnerRows(rows, true); err != nil {
		return nil, nil, fmt.Errorf("scan anchor rows: %w", err)
	}
	payouts, err := loadProviderPayouts(ctx, q, clubID)
	if err != nil {
		return nil, nil, err
	}
	for i := range anchors {
		if anchors[i].Eligible && payouts.stripeOnly(anchors[i].Description, anchors[i].Counterpart, anchors[i].Cents) {
			anchors[i].Eligible = false
		}
	}
	var from, to time.Time
	for _, a := range anchors {
		if !a.Eligible {
			continue
		}
		if from.IsZero() || a.Date.Before(from) {
			from = a.Date
		}
		if a.Date.After(to) {
			to = a.Date
		}
	}
	if from.IsZero() {
		return anchors, nil, nil
	}
	rows, err = q.Query(ctx,
		`SELECT `+partnerRowColumns+`
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.club_id = $1
		    AND bir.row_date BETWEEN $2::date - $4::int AND $3::date + $4::int
		    AND `+partnerEligibleSQL("bir"),
		clubID, from, to, partnerWindowDays,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("load partner pool: %w", err)
	}
	loaded, err := scanPartnerRows(rows, false)
	if err != nil {
		return nil, nil, fmt.Errorf("scan partner pool: %w", err)
	}
	for _, r := range loaded {
		if !payouts.stripeOnly(r.Description, r.Counterpart, r.Cents) {
			pool = append(pool, r)
		}
	}
	return anchors, pool, nil
}

// pgxQuerier is satisfied by both the pool and a transaction.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// loadPartnerScoring loads the club's name, its own account numbers and its
// active price item amounts.
func (s *Service) loadPartnerScoring(ctx context.Context, q pgxQuerier, clubID string) (partnerScoring, error) {
	sc := partnerScoring{ownAccounts: map[string]bool{}, priceCents: map[int64]bool{}}
	var name string
	if err := q.QueryRow(ctx, `SELECT name FROM clubs WHERE id = $1`, clubID).Scan(&name); err != nil {
		return sc, fmt.Errorf("load club name: %w", err)
	}
	if n := normalizeName(name); len(n) >= 3 {
		sc.clubName = n
	}
	rows, err := q.Query(ctx,
		`SELECT account_number FROM club_bank_accounts WHERE club_id = $1
		 UNION
		 SELECT bank_account_number FROM bank_imports WHERE club_id = $1 AND bank_account_number <> ''`,
		clubID,
	)
	if err != nil {
		return sc, fmt.Errorf("load own accounts: %w", err)
	}
	for rows.Next() {
		var acct string
		if err := rows.Scan(&acct); err != nil {
			rows.Close()
			return sc, fmt.Errorf("scan own account: %w", err)
		}
		if d := digitsOnly(acct); d != "" {
			sc.ownAccounts[d] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return sc, fmt.Errorf("own accounts: %w", err)
	}
	rows, err = q.Query(ctx,
		`SELECT DISTINCT round(amount * 100)::bigint FROM price_items
		  WHERE club_id = $1 AND is_active AND amount > 0`,
		clubID,
	)
	if err != nil {
		return sc, fmt.Errorf("load price amounts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c int64
		if err := rows.Scan(&c); err != nil {
			return sc, fmt.Errorf("scan price amount: %w", err)
		}
		sc.priceCents[c] = true
	}
	return sc, rows.Err()
}

// partnersFor returns the ranked partners of each anchor id that exists in
// the club. Ids of other clubs are simply absent from the result.
func (s *Service) partnersFor(ctx context.Context, q pgxQuerier, clubID string, ids []string) (map[string][]PartnerSuggestion, error) {
	anchors, pool, err := s.loadPartnerRows(ctx, q, clubID, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]PartnerSuggestion, len(anchors))
	if len(pool) == 0 {
		for _, a := range anchors {
			out[a.ID] = []PartnerSuggestion{}
		}
		return out, nil
	}
	sc, err := s.loadPartnerScoring(ctx, q, clubID)
	if err != nil {
		return nil, err
	}
	for _, a := range anchors {
		out[a.ID] = rankPartners(a, pool, sc)
	}
	return out, nil
}

// BankRowPartners returns the rows suggested as belonging with rowID.
func (s *Service) BankRowPartners(ctx context.Context, clubID, rowID string) ([]PartnerSuggestion, error) {
	m, err := s.partnersFor(ctx, s.db, clubID, []string{rowID})
	if err != nil {
		return nil, err
	}
	p, ok := m[rowID]
	if !ok {
		return nil, ErrBankRowNotFound
	}
	return p, nil
}

// StrongBankRowPartners returns only the strong partners of rowID: the ones
// that make booking the row on its own need confirming. A partner booked or
// dismissed in the meantime is no longer a candidate.
func (s *Service) StrongBankRowPartners(ctx context.Context, clubID, rowID string) ([]PartnerSuggestion, error) {
	all, err := s.BankRowPartners(ctx, clubID, rowID)
	if err != nil {
		return nil, err
	}
	strong := []PartnerSuggestion{}
	for _, p := range all {
		if p.Strength == PartnerStrong {
			strong = append(strong, p)
		}
	}
	return strong, nil
}

// bankRowPartnerHints returns the strongest partner strength per row id,
// for rows that have any partner.
func (s *Service) bankRowPartnerHints(ctx context.Context, clubID string, ids []string) (map[string]string, error) {
	hints := map[string]string{}
	if len(ids) == 0 {
		return hints, nil
	}
	m, err := s.partnersFor(ctx, s.db, clubID, ids)
	if err != nil {
		return nil, err
	}
	for id, ps := range m {
		if len(ps) > 0 {
			hints[id] = ps[0].Strength
		}
	}
	return hints, nil
}

// GroupAssignResult is what a group booking created.
type GroupAssignResult struct {
	JournalEntryID string   `json:"journal_entry_id"`
	RowIDs         []string `json:"row_ids"`
	Net            float64  `json:"net"`
}

const (
	minGroupRows = 2
	maxGroupRows = 5
)

// AssignBankRowsToAccountGroup books 2–5 bank rows that belong together as
// one bilag: one bank line for their net and one line on the chosen account
// (credit when money net came in, debit when it went out). The rows must be
// the club's, unbooked, undismissed, not provisional, on the same bank
// account and in the same open fiscal period; their net must not be zero
// (that is a transfer: Synk bank or dismiss it). All rows link to the entry
// or none do. Unassigning any of them later unbooks the whole group.
func (s *Service) AssignBankRowsToAccountGroup(
	ctx context.Context, clubID, actorID string, rowIDs []string, accountCode, description string,
) (*GroupAssignResult, error) {
	ids := uniqueStrings(rowIDs)
	if len(ids) < minGroupRows || len(ids) > maxGroupRows {
		return nil, ErrGroupRowCount
	}
	if strings.HasPrefix(accountCode, "19") || accountCode == receivablesAccountCode {
		return nil, ErrGroupTargetAccount
	}

	loaded, err := s.loadGroupRows(ctx, clubID, ids)
	if err != nil {
		return nil, err
	}
	net, latest, source, err := checkGroupRows(loaded)
	if err != nil {
		return nil, err
	}
	if accountCode == source.bankAcct {
		return nil, ErrGroupTargetAccount
	}

	periodID, periodStatus, err := s.resolvePeriod(ctx, clubID, latest, "")
	if err != nil {
		return nil, fmt.Errorf("resolve period: %w", err)
	}
	if periodStatus != "open" {
		return nil, fmt.Errorf("%w: %s", ErrGroupPeriodClosed, latest.Format("2006-01-02"))
	}
	for _, g := range loaded {
		pid, _, err := s.resolvePeriod(ctx, clubID, g.date, "")
		if err != nil {
			return nil, fmt.Errorf("resolve period: %w", err)
		}
		if pid != periodID {
			return nil, ErrGroupCrossesPeriod
		}
	}

	var accountExists bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM accounts WHERE club_id = $1 AND code = $2 AND is_active)`,
		clubID, accountCode,
	).Scan(&accountExists); err != nil {
		return nil, fmt.Errorf("validate account: %w", err)
	}
	if !accountExists {
		return nil, fmt.Errorf("account %s not found in chart of accounts", accountCode)
	}

	amount := float64(abs64(net)) / 100
	lines := []CreateJournalLineInput{
		{AccountCode: source.bankAcct, Debit: amount},
		{AccountCode: accountCode, Credit: amount},
	}
	if net < 0 {
		lines = []CreateJournalLineInput{
			{AccountCode: accountCode, Debit: amount},
			{AccountCode: source.bankAcct, Credit: amount},
		}
	}
	text := strings.TrimSpace(description)
	if text == "" {
		text = source.desc
	}
	sourceTable := "bank_import_rows"
	entry, err := s.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      latest.Format("2006-01-02"),
		Description:    "Samla tildeling: " + truncate(text, 200),
		Source:         "bank_import",
		SourceID:       &source.id,
		SourceTable:    &sourceTable,
		CreatedBy:      actorID,
		ClubID:         clubID,
		Lines:          lines,
	})
	if err != nil {
		return nil, fmt.Errorf("create journal: %w", err)
	}
	if err := s.linkAndPostBankRows(ctx, clubID, actorID, entry.ID, ids, nil); err != nil {
		return nil, err
	}
	return &GroupAssignResult{JournalEntryID: entry.ID, RowIDs: ids, Net: float64(net) / 100}, nil
}

// groupRow is a bank row as a group booking validates it.
type groupRow struct {
	id, desc, bankAcct     string
	counterpart            string
	date                   time.Time
	cents                  int64
	booked, dismissed, prv bool
}

// loadGroupRows loads the club's rows among ids; any id that is not the
// club's makes the whole group not found, and a provider payout deposit
// among them refuses it (ProviderPayoutDepositError).
func (s *Service) loadGroupRows(ctx context.Context, clubID string, ids []string) ([]groupRow, error) {
	rows, err := s.db.Query(ctx,
		`SELECT bir.id, bir.row_date, round(bir.amount * 100)::bigint, bir.description, COALESCE(bir.counterpart, ''),
		        bi.bank_account_code,
		        bir.journal_entry_id IS NOT NULL,
		        bir.dismissed_at IS NOT NULL OR bir.refund_paired_with IS NOT NULL,
		        COALESCE((`+provisionalRowSQL("bir")+`), false)
		   FROM bank_import_rows bir
		   JOIN bank_imports bi ON bi.id = bir.bank_import_id
		  WHERE bir.club_id = $1 AND bir.id = ANY($2::uuid[])`,
		clubID, ids,
	)
	if err != nil {
		return nil, fmt.Errorf("load bank rows: %w", err)
	}
	defer rows.Close()
	var loaded []groupRow
	for rows.Next() {
		var g groupRow
		if err := rows.Scan(&g.id, &g.date, &g.cents, &g.desc, &g.counterpart, &g.bankAcct, &g.booked, &g.dismissed, &g.prv); err != nil {
			return nil, fmt.Errorf("scan bank row: %w", err)
		}
		loaded = append(loaded, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bank rows: %w", err)
	}
	if len(loaded) != len(ids) {
		return nil, ErrBankRowNotFound
	}
	texts := make([]assignRowText, 0, len(loaded))
	for _, g := range loaded {
		texts = append(texts, assignRowText{id: g.id, description: g.desc, counterpart: g.counterpart, cents: g.cents})
	}
	if err := refuseProviderPayouts(ctx, s.db, clubID, texts...); err != nil {
		return nil, err
	}
	return loaded, nil
}

// checkGroupRows refuses a group that can't be one bilag and returns its
// net in øre, its latest date and the row the entry is sourced to (the
// largest amount).
func checkGroupRows(loaded []groupRow) (net int64, latest time.Time, source groupRow, err error) {
	source = loaded[0]
	for _, g := range loaded {
		switch {
		case g.booked:
			return 0, latest, source, errBankRowRaced
		case g.dismissed:
			return 0, latest, source, ErrGroupRowNotBookable
		case g.prv:
			return 0, latest, source, ErrGroupRowNotBookable
		case g.bankAcct != loaded[0].bankAcct:
			return 0, latest, source, ErrGroupMixedBankAccounts
		}
		net += g.cents
		if g.date.After(latest) {
			latest = g.date
		}
		if abs64(g.cents) > abs64(source.cents) || (abs64(g.cents) == abs64(source.cents) && g.id < source.id) {
			source = g
		}
	}
	if net == 0 {
		return 0, latest, source, ErrGroupNetZero
	}
	return net, latest, source, nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
