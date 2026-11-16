package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// Outcomes of one harbor share import row (BRY-139).
const (
	harborShareSet           = "set"
	harborShareDateAdded     = "date_added"
	harborShareUnchanged     = "unchanged"
	harborShareConflict      = "conflict"
	harborShareNotOwnerBut   = "not_owner_but_set"
	harborShareDuplicate     = "duplicate"
	harborShareSkipped       = "skipped"
	harborShareUnmatched     = "unmatched"
	harborShareAmbiguous     = "ambiguous"
	harborShareError         = "error"
	codeHarborSharePlanStale = "plan_changed"

	// harborShareMaxRows caps the data rows of one upload. A club's register
	// is a few hundred rows; the cap keeps the preview a page a person can
	// actually review, and the plan cheap to rebuild on every pick.
	harborShareMaxRows = 5000

	msgHarborSharePickDropped = "matched automatically; your pick was dropped"
)

var harborShareOutcomes = []string{
	harborShareSet, harborShareDateAdded, harborShareUnchanged, harborShareConflict,
	harborShareNotOwnerBut, harborShareDuplicate, harborShareSkipped,
	harborShareUnmatched, harborShareAmbiguous, harborShareError,
}

// harborShareState is a member's harbor share fields, as stored or as an
// import row would leave them.
type harborShareState struct {
	Owner  bool    `json:"owner"`
	PaidOn *string `json:"paid_on"`
}

// harborShareCandidate is one member an ambiguous name could mean. The
// active slips are what tell two members of the same name apart.
type harborShareCandidate struct {
	UserID      string   `json:"user_id"`
	Name        string   `json:"name"`
	Slips       []string `json:"slips"`
	Deactivated bool     `json:"deactivated"`
}

// harborShareImportRow is one row of the import response. Email and Name are
// the uploaded values, never the stored ones, so the response discloses
// nothing the caller did not send.
type harborShareImportRow struct {
	Row         int    `json:"row"`
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	Outcome     string `json:"outcome"`
	Deactivated bool   `json:"deactivated"`
	Resolved    bool   `json:"resolved"`
	// ResolutionDropped marks a dry-run row that now matches by itself, so
	// the resolution sent for it was ignored. The client drops it before
	// applying, as the apply refuses it.
	ResolutionDropped bool                   `json:"resolution_dropped"`
	Current           *harborShareState      `json:"current,omitempty"`
	Proposed          *harborShareState      `json:"proposed,omitempty"`
	Candidates        []harborShareCandidate `json:"candidates,omitempty"`
	Message           string                 `json:"message,omitempty"`
}

// harborShareInput is one parsed data row of the upload.
type harborShareInput struct {
	row    int
	email  string
	name   string
	owner  bool
	paidOn *string
	err    string
}

// harborShareMember is a member the import may match: a person in the
// caller's club whose identity is not erased. Deactivated members are
// included, since they still own their share.
type harborShareMember struct {
	id          string
	email       string
	name        string
	nameKey     string
	owner       bool
	paidOn      *string
	deactivated bool
	slips       []string
}

// harborShareWrite is one UPDATE an apply makes.
type harborShareWrite struct {
	userID  string
	outcome string
	paidOn  *string
}

type harborSharePlan struct {
	rows   []harborShareImportRow
	counts map[string]int
	writes []harborShareWrite
	token  string
}

// errHarborShareResolution is a resolution the server refuses; the request
// is a 400 and nothing is planned.
type errHarborShareResolution struct{ msg string }

func (e *errHarborShareResolution) Error() string { return e.msg }

var nameFolder = cases.Fold()

// normaliseMemberName makes a name comparable across a spreadsheet and the
// member table: Unicode NFC (a name typed on a Mac may carry å as a + ring),
// case-folded, trimmed, inner whitespace collapsed.
func normaliseMemberName(s string) string {
	return strings.Join(strings.Fields(nameFolder.String(norm.NFC.String(s))), " ")
}

// parseHarborShareOwner reads the owner column. A blank cell is "not owner",
// as the club's sheet leaves non-owners empty.
func parseHarborShareOwner(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "ja", "true", "1", "x":
		return true, true
	case "no", "nei", "false", "0", "":
		return false, true
	}
	return false, false
}

// parseHarborSharePaidOn reads the optional paid date as YYYY-MM-DD or the
// Norwegian DD.MM.YYYY, returning it as YYYY-MM-DD.
func parseHarborSharePaidOn(v string) (*string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, true
	}
	for _, layout := range []string{time.DateOnly, "02.01.2006", "2.1.2006"} {
		if d, err := time.Parse(layout, v); err == nil {
			s := d.Format(time.DateOnly)
			return &s, true
		}
	}
	return nil, false
}

// harborShareHeaderAliases are the Norwegian column names a member sheet
// usually already has, so it can be uploaded without renaming its headers.
// Names are matched lower-cased. A club-specific column whose meaning differs
// between clubs (such as "fast plass") is deliberately not an alias.
var harborShareHeaderAliases = map[string][]string{
	"email":                {"e-post", "epost", "e-postadresse"},
	"name":                 {"navn", "fullt navn"},
	"first_name":           {"fornavn"},
	"last_name":            {"etternavn"},
	"harbor_share_owner":   {"moloandel", "moloandelseier", "moloandel eier", "moloandel-eier"},
	"harbor_share_paid_on": {"moloandel betalt", "betalt dato"},
}

// readHarborShareInputs parses the data rows. Wholly blank rows are dropped,
// as spreadsheets often save a few after the data.
func readHarborShareInputs(u *csvUpload) ([]harborShareInput, error) {
	u.alias(harborShareHeaderAliases)
	if !u.has("harbor_share_owner") {
		return nil, errors.New(`missing required column "harbor_share_owner"`)
	}
	hasName := u.has("name") || (u.has("first_name") && u.has("last_name"))
	if !u.has("email") && !hasName {
		return nil, errors.New(`missing a member column: "email", "name", or "first_name" and "last_name"`)
	}

	var inputs []harborShareInput
	rowNum := 1 // the header is row 1
	for {
		rowNum++
		record, err := u.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			inputs = append(inputs, harborShareInput{row: rowNum, err: fmt.Sprintf("parse: %v", err)})
			continue
		}
		if strings.TrimSpace(strings.Join(record, "")) == "" {
			continue
		}

		in := harborShareInput{row: rowNum, email: u.get(record, "email"), name: u.get(record, "name")}
		if in.name == "" {
			in.name = strings.TrimSpace(u.get(record, "first_name") + " " + u.get(record, "last_name"))
		}
		owner, ok := parseHarborShareOwner(u.get(record, "harbor_share_owner"))
		paidOn, dateOK := parseHarborSharePaidOn(u.get(record, "harbor_share_paid_on"))
		switch {
		case in.email == "" && in.name == "":
			in.err = "row has no email or name"
		case !ok:
			in.err = "harbor_share_owner must be yes/ja/true/1/x or no/nei/false/0/blank"
		case !dateOK:
			in.err = "harbor_share_paid_on must be YYYY-MM-DD or DD.MM.YYYY"
		case paidOn != nil && !owner:
			in.err = "harbor_share_paid_on is set on a row that is not an owner"
		}
		in.owner, in.paidOn = owner, paidOn
		inputs = append(inputs, in)
		if len(inputs) > harborShareMaxRows {
			return nil, fmt.Errorf("the file has more than %d data rows; split it into smaller files", harborShareMaxRows)
		}
	}
	return inputs, nil
}

// harborShareQuerier is the subset of pgx used to load members, so the dry
// run reads the pool and the apply reads its transaction.
type harborShareQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// loadHarborShareMembers loads every member the import may match.
func loadHarborShareMembers(ctx context.Context, db harborShareQuerier, clubID string) ([]harborShareMember, error) {
	rows, err := db.Query(ctx,
		`SELECT u.id, u.email, u.first_name, u.last_name, u.harbor_share_owner,
		        to_char(u.harbor_share_paid_on, 'YYYY-MM-DD'), u.deactivated_at IS NOT NULL,
		        COALESCE((
		            SELECT array_agg(CASE WHEN s.number LIKE s.section || '%' THEN s.number
		                                  ELSE s.section || s.number END
		                             ORDER BY s.section, s.number)
		              FROM slip_assignments sa
		              JOIN slips s ON s.id = sa.slip_id
		             WHERE sa.user_id = u.id AND sa.club_id = u.club_id AND sa.released_at IS NULL
		        ), '{}')
		   FROM users u
		  WHERE u.club_id = $1 AND u.kind = 'person'
		    AND u.email NOT LIKE '%' || $2 AND u.email <> $3`,
		clubID, accounting.ErasedEmailSuffix, accounting.LegacyAnonymizedEmail,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []harborShareMember
	for rows.Next() {
		var m harborShareMember
		var first, last string
		if err := rows.Scan(&m.id, &m.email, &first, &last, &m.owner, &m.paidOn, &m.deactivated, &m.slips); err != nil {
			return nil, err
		}
		m.name = strings.TrimSpace(first + " " + last)
		m.nameKey = normaliseMemberName(m.name)
		members = append(members, m)
	}
	return members, rows.Err()
}

func sameDate(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// harborShareOutcome decides what a matched row does to its member. The
// import only adds: it never overwrites a date or clears ownership.
func harborShareOutcome(in harborShareInput, m harborShareMember) (string, *harborShareState) {
	if !in.owner {
		if m.owner {
			return harborShareNotOwnerBut, nil
		}
		return harborShareSkipped, nil
	}
	switch {
	case !m.owner:
		return harborShareSet, &harborShareState{Owner: true, PaidOn: in.paidOn}
	case in.paidOn == nil || sameDate(m.paidOn, in.paidOn):
		return harborShareUnchanged, nil
	case m.paidOn == nil:
		return harborShareDateAdded, &harborShareState{Owner: true, PaidOn: in.paidOn}
	default:
		return harborShareConflict, nil
	}
}

// planHarborShareImport matches every row and decides its outcome. It does
// no I/O, so the dry run and the apply plan identically from the same state.
// A resolution for a row that matches by itself is refused on apply; a dry
// run ignores it and flags the row instead, since members can change between
// the pick and the preview (a 409 re-preview) and the pick is then moot.
func planHarborShareImport(inputs []harborShareInput, members []harborShareMember, resolutions map[int]string, dryRun bool) (*harborSharePlan, error) { //nolint:gocognit,cyclop // one pass of match, resolve, dedupe and decide per row
	byID := make(map[string]*harborShareMember, len(members))
	byEmail := map[string][]*harborShareMember{}
	byName := map[string][]*harborShareMember{}
	for i := range members {
		m := &members[i]
		byID[m.id] = m
		key := strings.ToLower(m.email)
		byEmail[key] = append(byEmail[key], m)
		byName[m.nameKey] = append(byName[m.nameKey], m)
	}

	plan := &harborSharePlan{counts: map[string]int{}}
	for _, o := range harborShareOutcomes {
		plan.counts[o] = 0
	}
	matched := make([]*harborShareMember, len(inputs))
	seenRows := map[int]bool{}

	for i, in := range inputs {
		seenRows[in.row] = true
		row := harborShareImportRow{Row: in.row, Email: in.email, Name: in.name}
		pick, hasPick := resolutions[in.row]
		if in.err != "" {
			if hasPick {
				return nil, &errHarborShareResolution{fmt.Sprintf("row %d has an error and takes no resolution", in.row)}
			}
			row.Outcome, row.Message = harborShareError, in.err
			plan.rows = append(plan.rows, row)
			continue
		}

		// An email shared by several members (case aside) is as ambiguous
		// as a shared name, and is not second-guessed by the name.
		var candidates []*harborShareMember
		if in.email != "" {
			candidates = byEmail[strings.ToLower(in.email)]
		}
		if len(candidates) == 0 && in.name != "" {
			candidates = byName[normaliseMemberName(in.name)]
		}
		var m *harborShareMember
		if len(candidates) == 1 {
			m = candidates[0]
		}

		if hasPick {
			switch {
			case m != nil && dryRun:
				row.ResolutionDropped, row.Message = true, msgHarborSharePickDropped
			case m != nil:
				return nil, &errHarborShareResolution{fmt.Sprintf("row %d matched a member and takes no resolution", in.row)}
			default:
				m = byID[pick]
				if m == nil {
					return nil, &errHarborShareResolution{fmt.Sprintf("the member chosen for row %d is not a member of this club", in.row)}
				}
				row.Resolved = true
			}
		}

		if m == nil {
			if len(candidates) > 1 {
				row.Outcome = harborShareAmbiguous
				for _, c := range candidates {
					row.Candidates = append(row.Candidates, harborShareCandidate{
						UserID: c.id, Name: c.name, Slips: c.slips, Deactivated: c.deactivated,
					})
				}
			} else {
				row.Outcome = harborShareUnmatched
			}
			plan.rows = append(plan.rows, row)
			continue
		}
		matched[i] = m
		row.UserID, row.Deactivated = m.id, m.deactivated
		row.Current = &harborShareState{Owner: m.owner, PaidOn: m.paidOn}
		plan.rows = append(plan.rows, row)
	}
	for r := range resolutions {
		if !seenRows[r] {
			return nil, &errHarborShareResolution{fmt.Sprintf("row %d is not a data row of this file", r)}
		}
	}

	// Rows naming the same member apply once if they agree; if they differ,
	// none of them does, since the file contradicts itself.
	groups := map[string][]int{}
	for i, m := range matched {
		if m != nil {
			groups[m.id] = append(groups[m.id], i)
		}
	}
	for i, m := range matched {
		if m == nil {
			continue
		}
		in, row := inputs[i], &plan.rows[i]
		group := groups[m.id]
		if len(group) > 1 {
			agree := true
			for _, j := range group {
				if inputs[j].owner != in.owner || !sameDate(inputs[j].paidOn, in.paidOn) {
					agree = false
				}
			}
			switch {
			case !agree:
				row.Outcome = harborShareDuplicate
				row.Message = joinMessages(row.Message, fmt.Sprintf("rows %s name the same member with different values; none is applied", joinRowNumbers(inputs, group)))
				continue
			case group[0] != i:
				row.Outcome = harborShareDuplicate
				row.Message = joinMessages(row.Message, fmt.Sprintf("same member and values as row %d", inputs[group[0]].row))
				continue
			}
		}
		row.Outcome, row.Proposed = harborShareOutcome(in, *m)
		if row.Outcome == harborShareSet || row.Outcome == harborShareDateAdded {
			plan.writes = append(plan.writes, harborShareWrite{userID: m.id, outcome: row.Outcome, paidOn: in.paidOn})
		}
	}

	hash := sha256.New()
	for _, row := range plan.rows {
		plan.counts[row.Outcome]++
		paidOn := ""
		if row.Proposed != nil && row.Proposed.PaidOn != nil {
			paidOn = *row.Proposed.PaidOn
		}
		fmt.Fprintf(hash, "%d|%s|%s|%s\n", row.Row, row.UserID, row.Outcome, paidOn)
	}
	plan.token = hex.EncodeToString(hash.Sum(nil))
	return plan, nil
}

func joinMessages(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func joinRowNumbers(inputs []harborShareInput, idx []int) string {
	parts := make([]string, len(idx))
	for k, i := range idx {
		parts[k] = strconv.Itoa(inputs[i].row)
	}
	return strings.Join(parts, ", ")
}

// parseHarborShareResolutions reads the resolutions field: a JSON object of
// row number to the chosen member's id.
func parseHarborShareResolutions(v string) (map[int]string, error) {
	out := map[int]string{}
	if strings.TrimSpace(v) == "" {
		return out, nil
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(v), &raw); err != nil {
		return nil, errors.New(`resolutions must be a JSON object of row number to user id`)
	}
	for k, id := range raw {
		row, err := strconv.Atoi(k)
		if err != nil || row < 2 {
			return nil, fmt.Errorf("resolutions: %q is not a data row number", k)
		}
		out[row] = id
	}
	return out, nil
}

// HandleImportHarborShares imports harbor share (moloandel) ownership from a
// CSV of existing members (BRY-139, BRD-14). It only updates members: owners
// not in Brygge come back unmatched, to be created with the user import first.
// A dry run (the default) plans and writes nothing; its plan_token must come
// back with the apply, which re-plans and answers 409 plan_changed if the
// members changed in between, so the board applies exactly what it reviewed.
// Admin-only (BRD-9 holds for admin targets because the caller is an admin).
func (h *AdminUsersHandler) HandleImportHarborShares(w http.ResponseWriter, r *http.Request) { //nolint:cyclop // parse, plan, then apply in one transaction
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
		Error(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	dryRun := true
	switch strings.ToLower(strings.TrimSpace(r.FormValue("dry_run"))) {
	case "", "true", "1":
	case "false", "0":
		dryRun = false
	default:
		Error(w, http.StatusBadRequest, "dry_run must be true or false")
		return
	}
	resolutions, err := parseHarborShareResolutions(r.FormValue("resolutions"))
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	planToken := strings.TrimSpace(r.FormValue("plan_token"))
	if !dryRun && planToken == "" {
		Error(w, http.StatusBadRequest, "plan_token from the dry run is required to apply")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		Error(w, http.StatusBadRequest, `missing "file" field`)
		return
	}
	defer file.Close()
	upload, err := readCSVUpload(file)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	inputs, err := readHarborShareInputs(upload)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if dryRun {
		members, err := loadHarborShareMembers(ctx, h.db, claims.ClubID)
		if err != nil {
			h.log.Error().Err(err).Msg("failed to load members for harbor share import")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		plan, ok := h.planHarborShares(w, inputs, members, resolutions, true)
		if !ok {
			return
		}
		h.writeHarborShareResponse(w, true, plan)
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to begin transaction")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	members, err := loadHarborShareMembers(ctx, tx, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to load members for harbor share import")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	plan, ok := h.planHarborShares(w, inputs, members, resolutions, false)
	if !ok {
		return
	}
	if plan.token != planToken {
		writeHarborSharePlanChanged(w)
		return
	}
	applied, err := applyHarborShareWrites(ctx, tx, claims.ClubID, claims.UserID, plan)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to apply harbor share import")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !applied {
		// A write's precondition no longer held: a member changed after
		// this transaction read them. The deferred rollback undoes the rest.
		writeHarborSharePlanChanged(w)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("failed to commit harbor share import")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info().
		Str("actor", claims.UserID).
		Int("rows", len(plan.rows)).
		Int("set", plan.counts[harborShareSet]).
		Int("date_added", plan.counts[harborShareDateAdded]).
		Msg("harbor share import applied")
	h.writeHarborShareResponse(w, false, plan)
}

func (h *AdminUsersHandler) planHarborShares(w http.ResponseWriter, inputs []harborShareInput, members []harborShareMember, resolutions map[int]string, dryRun bool) (*harborSharePlan, bool) {
	plan, err := planHarborShareImport(inputs, members, resolutions, dryRun)
	var resErr *errHarborShareResolution
	switch {
	case errors.As(err, &resErr):
		Error(w, http.StatusBadRequest, resErr.Error())
		return nil, false
	case err != nil:
		h.log.Error().Err(err).Msg("failed to plan harbor share import")
		Error(w, http.StatusInternalServerError, "internal error")
		return nil, false
	}
	return plan, true
}

func (h *AdminUsersHandler) writeHarborShareResponse(w http.ResponseWriter, dryRun bool, plan *harborSharePlan) {
	JSON(w, http.StatusOK, map[string]any{
		"dry_run":    dryRun,
		"counts":     plan.counts,
		"rows":       plan.rows,
		"plan_token": plan.token,
	})
}

func writeHarborSharePlanChanged(w http.ResponseWriter) {
	JSON(w, http.StatusConflict, map[string]string{
		"error": "members changed since the preview; nothing was applied, review the new preview",
		"code":  codeHarborSharePlanStale,
	})
}

// applyHarborShareWrites makes the plan's writes and their audit rows in tx.
// Each UPDATE repeats its precondition, including the member filter of
// loadHarborShareMembers, so a member changed or erased after the plan was
// read is not overwritten; it reports false when any write missed.
func applyHarborShareWrites(ctx context.Context, tx pgx.Tx, clubID, actorID string, plan *harborSharePlan) (bool, error) {
	for _, wr := range plan.writes {
		q := `UPDATE users SET harbor_share_owner = true, harbor_share_paid_on = $3::date, updated_at = now()
		       WHERE id = $1 AND club_id = $2 AND kind = 'person'
		         AND email NOT LIKE '%' || $4 AND email <> $5
		         AND NOT harbor_share_owner`
		if wr.outcome == harborShareDateAdded {
			q = `UPDATE users SET harbor_share_paid_on = $3::date, updated_at = now()
			      WHERE id = $1 AND club_id = $2 AND kind = 'person'
			        AND email NOT LIKE '%' || $4 AND email <> $5
			        AND harbor_share_owner AND harbor_share_paid_on IS NULL`
		}
		tag, err := tx.Exec(ctx, q, wr.userID, clubID, wr.paidOn,
			accounting.ErasedEmailSuffix, accounting.LegacyAnonymizedEmail)
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() != 1 {
			return false, nil
		}
		// Ids only: the member is the resource_id, and the values stay out
		// of the log like every other member audit row.
		if _, err := tx.Exec(ctx,
			`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
			 VALUES ($1, $2, $3, 'user', $4, '{"source":"import"}')`,
			clubID, actorID, audit.ActionUserHarborShareSet, wr.userID,
		); err != nil {
			return false, err
		}
	}

	summary, err := json.Marshal(plan.counts)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, details)
		 VALUES ($1, $2, $3, 'user', $4)`,
		clubID, actorID, audit.ActionUserHarborSharesImported, summary,
	); err != nil {
		return false, err
	}
	return true, nil
}
