package accounting

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Teaching a payer alias from bank Tildel (BRY-383). Assigning a deposit to a
// member's faktura can remember the row's payer name for that member, in the
// same store the Vipps queue teaches (vipps_customer_aliases), so the bank
// and Vipps auto-assign rules resolve the payer's next payment. The alias is
// saved in the faktura-lock transaction before the row is linked and posted:
// a refused or failed booking saves no alias.

// Where an alias was taught, recorded on its audit row.
const (
	PayerAliasSourceBankTildel = "bank_tildel"
	PayerAliasSourceVippsQueue = "vipps_queue"
)

// How a payer resolves today, as PayerAliasState.Pass reports it.
const (
	payerPassAlias = "alias"
	payerPassName  = "name"
	payerPassNone  = "none"
)

// ErrAliasConflict refuses to remember a payer whose name is another
// member's own name or already that member's alias: saving it would send
// that member's future payments elsewhere. The request may override it.
var ErrAliasConflict = errors.New("alias_conflict")

// ErrNotClubMember refuses an alias for a user who is not in the club.
var ErrNotClubMember = errors.New("user is not a member of this club")

// PayerAliasState is how a bank row's payer resolves today, for the Tildel
// "remember this payer" box. Taken is true when the name is some member's
// own name or an alias of one, so remembering it for anyone else would take
// it from them. It never says whose: which faktura the payer already
// resolves to is InvoiceSuggestion.PayerResolvesHere, per candidate.
type PayerAliasState struct {
	Payer   string `json:"payer"`
	Pass    string `json:"pass"`
	Taken   bool   `json:"taken"`
	OwnClub bool   `json:"own_club"`
}

// PayerAliasRequest asks a faktura assign to remember the row's payer for the
// faktura's member. ConfirmOverride saves it despite a conflict.
type PayerAliasRequest struct {
	Remember        bool
	ConfirmOverride bool
}

// TaughtAlias is the alias an assign saved. Override is true when it took the
// name over from another member.
type TaughtAlias struct {
	ID       string
	UserID   string
	Override bool
}

// payerResolution is what a payer name means today: the member a taught
// alias names, and the members whose own name it is.
type payerResolution struct {
	aliasUser   string
	nameMembers map[string]bool
}

func resolvePayer(ctx context.Context, q pgxQuerier, clubID, payer string) (payerResolution, error) {
	res := payerResolution{aliasUser: aliasMember(ctx, q, clubID, payer), nameMembers: map[string]bool{}}
	cands, err := loadMemberCands(ctx, q, clubID)
	if err != nil {
		return res, err
	}
	needle := normalizeName(payer)
	for _, c := range cands {
		if (c.fullN != "" && c.fullN == needle) || (hasFirstLast(c) && c.firstLastN == needle) {
			res.nameMembers[c.id] = true
		}
	}
	return res, nil
}

// member is the member the payer resolves to through a strong pass, as
// resolveStrongMember decides it: the alias, else a unique own name.
func (p payerResolution) member() (string, string) {
	if p.aliasUser != "" {
		return p.aliasUser, payerPassAlias
	}
	if len(p.nameMembers) == 1 {
		for id := range p.nameMembers {
			return id, payerPassName
		}
	}
	return "", payerPassNone
}

// conflictsWith reports whether remembering the payer for memberID would take
// the name from another member: it is another member's alias, or any other
// member's own name.
func (p payerResolution) conflictsWith(memberID string) bool {
	if p.aliasUser != "" && p.aliasUser != memberID {
		return true
	}
	for id := range p.nameMembers {
		if id != memberID {
			return true
		}
	}
	return false
}

// bankRowPayerState is PayerAliasState for one bank row, plus the member the
// payer already resolves to ("" for none). The member stays server-side: it
// only marks the candidates that are that member's fakturas.
func (s *Service) bankRowPayerState(ctx context.Context, clubID string, r partnerRow) (*PayerAliasState, string, error) {
	st := &PayerAliasState{Payer: bankRowPayer(r.Description, r.Counterpart), Pass: payerPassNone}
	if st.Payer == "" {
		return st, "", nil
	}
	sc, err := s.loadPartnerScoring(ctx, s.db, clubID)
	if err != nil {
		return nil, "", err
	}
	if sc.isOwn(partnerRow{Cents: r.Cents, Counterpart: st.Payer, FromAccount: r.FromAccount, ToAccount: r.ToAccount}) {
		st.OwnClub = true
		return st, "", nil
	}
	res, err := resolvePayer(ctx, s.db, clubID, st.Payer)
	if err != nil {
		return nil, "", err
	}
	memberID, pass := res.member()
	st.Pass = pass
	st.Taken = res.aliasUser != "" || len(res.nameMembers) > 0
	return st, memberID, nil
}

// bankRowResolvedMember is the member a bank row's payer already resolves to
// through a strong pass, or "".
func (s *Service) bankRowResolvedMember(ctx context.Context, clubID, bankRowID string) (string, error) {
	var r partnerRow
	if err := s.db.QueryRow(ctx,
		`SELECT round(amount * 100)::bigint, COALESCE(description, ''), COALESCE(counterpart, ''),
		        COALESCE(from_account, ''), COALESCE(to_account, '')
		   FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
		bankRowID, clubID,
	).Scan(&r.Cents, &r.Description, &r.Counterpart, &r.FromAccount, &r.ToAccount); err != nil {
		return "", fmt.Errorf("load row payer: %w", err)
	}
	_, memberID, err := s.bankRowPayerState(ctx, clubID, r)
	return memberID, err
}

// markPayerResolvesHere flags the candidates whose member the payer already
// resolves to: assigning one of them has nothing to teach.
func markPayerResolvesHere(invs []InvoiceSuggestion, memberID string) {
	if memberID == "" {
		return
	}
	for i := range invs {
		invs[i].PayerResolvesHere = invs[i].UserID == memberID
	}
}

// teachPayerAlias saves the payer of rowIDs as an alias of the faktura's
// member, inside the caller's faktura-lock transaction. It saves nothing,
// and returns nil, when there is nothing to learn: no request, a faktura
// without a member, an empty or club payer, rows from different payers, or
// a payer that already resolves to the member. A conflict is ErrAliasConflict
// unless the request confirms the override.
func (s *Service) teachPayerAlias(ctx context.Context, tx pgx.Tx, clubID, invoiceID string, rowIDs []string, req PayerAliasRequest) (*TaughtAlias, error) {
	if !req.Remember {
		return nil, nil
	}
	var memberID *string
	if err := tx.QueryRow(ctx,
		`SELECT user_id::text FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID,
	).Scan(&memberID); err != nil {
		return nil, fmt.Errorf("load faktura member: %w", err)
	}
	if memberID == nil {
		return nil, nil
	}
	sc, err := s.loadPartnerScoring(ctx, tx, clubID)
	if err != nil {
		return nil, err
	}
	payer := ""
	for i, id := range rowIDs {
		var r partnerRow
		if err := tx.QueryRow(ctx,
			`SELECT round(amount * 100)::bigint, COALESCE(description, ''), COALESCE(counterpart, ''),
			        COALESCE(from_account, ''), COALESCE(to_account, '')
			   FROM bank_import_rows WHERE id = $1 AND club_id = $2`,
			id, clubID,
		).Scan(&r.Cents, &r.Description, &r.Counterpart, &r.FromAccount, &r.ToAccount); err != nil {
			return nil, fmt.Errorf("load row payer: %w", err)
		}
		p := bankRowPayer(r.Description, r.Counterpart)
		if p == "" || sc.isOwn(partnerRow{Cents: r.Cents, Counterpart: p, FromAccount: r.FromAccount, ToAccount: r.ToAccount}) {
			return nil, nil
		}
		if i == 0 {
			payer = p
		} else if normalizeName(p) != normalizeName(payer) {
			return nil, nil
		}
	}

	res, err := resolvePayer(ctx, tx, clubID, payer)
	if err != nil {
		return nil, err
	}
	if resolved, _ := res.member(); resolved == *memberID {
		return nil, nil
	}
	conflict := res.conflictsWith(*memberID)
	if conflict && !req.ConfirmOverride {
		return nil, ErrAliasConflict
	}
	a, err := upsertCustomerAlias(ctx, tx, clubID, payer, *memberID)
	if err != nil {
		return nil, fmt.Errorf("save payer alias: %w", err)
	}
	return &TaughtAlias{ID: a.ID, UserID: a.UserID, Override: conflict}, nil
}
