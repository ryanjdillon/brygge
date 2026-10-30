package paymentfilters

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"
)

// MaxGroups caps the groups a club can have, built-ins included: the chips are
// a row on a phone, and the settings page lists them on one screen.
const MaxGroups = 20

// MaxNameLen is the longest group name, in characters (pfg_name_len).
const MaxNameLen = 40

// Error codes of a refused write. The handler maps them to HTTP statuses; the
// SPA keys on the code.
const (
	CodeNotFound      = "not_found"
	CodeInvalidName   = "invalid_name"
	CodeReservedName  = "reserved_name"
	CodeNameTaken     = "name_taken"
	CodeInvalidIcon   = "invalid_icon"
	CodeTooManyGroups = "too_many_groups"
	CodeBuiltinGroup  = "builtin_group"
	CodeNoDefaultName = "no_default_name"
	CodeUnknownItem   = "unknown_price_item"
	CodeOrderMismatch = "order_mismatch"
)

// Error is a refused write with a stable code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func refuse(code, msg string) error { return &Error{Code: code, Message: msg} }

// CodeOf returns the code of a refused write, or "" for any other error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Exec is what writes need: a transaction.
type Exec = pgx.Tx

// Lock serialises the writes of one club's groups for the rest of the
// transaction, so two admins moving the same item or reordering at once apply
// one after the other instead of interleaving.
func Lock(ctx context.Context, tx Exec, clubID string) error {
	_, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('payment_filter_groups:' || $1::text, 0))`, clubID)
	return err
}

// EnsureBuiltins creates the faktura and other rows a club does not have yet.
// Every write starts with it, after Lock, so a club created after the
// migration gets its built-ins on the first change.
func EnsureBuiltins(ctx context.Context, tx Exec, clubID string) error {
	for _, b := range defaultBuiltins() {
		if _, err := tx.Exec(ctx,
			`INSERT INTO payment_filter_groups (club_id, slug, label_key, icon, sort_order, builtin)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT DO NOTHING`,
			clubID, b.Slug, *b.LabelKey, b.Icon, b.SortOrder, *b.Builtin); err != nil {
			return err
		}
	}
	return nil
}

// begin takes the club lock and makes sure the built-ins exist.
func begin(ctx context.Context, tx Exec, clubID string) error {
	if err := Lock(ctx, tx, clubID); err != nil {
		return err
	}
	return EnsureBuiltins(ctx, tx, clubID)
}

// defaultLabels are the rendered names of the seeded label keys in the SPA
// locales that name the keys differently; a new group may not be given one of
// them, since two chips would read the same. The SPA checks the labels of the
// current locale as well.
var defaultLabels = map[string][]string{
	"guest":     {"guest berth", "gjesteplass"},
	"motorhome": {"motorhome", "bobil", "bubil"},
	"slipping":  {"slipping", "haul-out"},
	"faktura":   {"invoice", "faktura"},
	"other":     {"other", "annet", "anna"},
}

// NormalizeName brings a group name to the form that is stored: Unicode NFC
// (so the same word typed two ways is one name), trimmed, and free of control
// and format characters (zero-width spaces and joiners, bidi marks, byte-order
// marks), which would make two chips look alike. It checks the name fits a chip.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(norm.NFC.String(name))
	if name == "" || utf8.RuneCountInString(name) > MaxNameLen {
		return "", refuse(CodeInvalidName, "name must be 1 to 40 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", refuse(CodeInvalidName, "name must not contain control or invisible characters")
		}
	}
	return name, nil
}

// reservedLabels are the names of the "all" chip in the SPA locales
// (recentPayments.kind.all): a group named like it would sit next to the
// no-filter chip and read the same.
var reservedLabels = []string{"all", "alle", "tous", "tutti", "wszystkie"}

func isReservedLabel(name string) bool {
	return slices.Contains(reservedLabels, strings.ToLower(name))
}

// nameTaken reports whether another group of the club already reads as name:
// the same stored name (case-insensitive) or the rendered default label of a
// seeded row.
func nameTaken(ctx context.Context, tx Exec, clubID, name, exceptSlug string) (bool, error) {
	rows, err := tx.Query(ctx,
		`SELECT slug, name, label_key FROM payment_filter_groups WHERE club_id = $1`, clubID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	want := strings.ToLower(name)
	for rows.Next() {
		var slug string
		var n, key *string
		if err := rows.Scan(&slug, &n, &key); err != nil {
			return false, err
		}
		if slug == exceptSlug {
			continue
		}
		if n != nil {
			if strings.ToLower(*n) == want {
				return true, nil
			}
			continue
		}
		if key != nil {
			for _, l := range defaultLabels[*key] {
				if l == want {
					return true, nil
				}
			}
		}
	}
	return false, rows.Err()
}

func getBySlug(ctx context.Context, tx Exec, clubID, slug string) (Group, error) {
	g, err := scanGroup(tx.QueryRow(ctx,
		`SELECT `+groupColumns+` FROM payment_filter_groups WHERE club_id = $1 AND slug = $2`, clubID, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return Group{}, refuse(CodeNotFound, "payment filter group not found")
	}
	return g, err
}

// CreateInput is a new item group.
type CreateInput struct {
	Name         string
	Icon         string
	Enabled      bool
	PriceItemIDs []string
}

// ItemMove records an item that left another group for the one being edited.
type ItemMove struct {
	PriceItemID string
	FromSlug    string
	// FromID is the row id of the group it left.
	FromID string
}

// ItemChange is what a change of a group's item set did.
type ItemChange struct {
	Added   []string
	Removed []string
	Moved   []ItemMove
}

// Changed reports whether the set differs from before.
func (c ItemChange) Changed() bool { return len(c.Added) > 0 || len(c.Removed) > 0 }

// Create adds an item group just before the faktura built-in, so it ranks above
// Faktura like the groups a migration seeds do (an order that pays a faktura and
// holds an item of the new group is shown as the new group), and after the item
// groups that already rank above Faktura. The other groups' positions are
// renumbered (10, 20, ...) in the same locked transaction, the way a reorder
// does. Its slug comes from the name and is never changed afterwards.
func Create(ctx context.Context, tx Exec, clubID string, in CreateInput) (Group, ItemChange, error) {
	if err := begin(ctx, tx, clubID); err != nil {
		return Group{}, ItemChange{}, err
	}
	name, err := NormalizeName(in.Name)
	if err != nil {
		return Group{}, ItemChange{}, err
	}
	if !ValidIcon(in.Icon) {
		return Group{}, ItemChange{}, refuse(CodeInvalidIcon, "unknown icon")
	}
	existing, err := ListGroups(ctx, tx, clubID, true)
	if err != nil {
		return Group{}, ItemChange{}, err
	}
	if len(existing) >= MaxGroups {
		return Group{}, ItemChange{}, refuse(CodeTooManyGroups, "a club can have at most 20 filter groups")
	}
	taken := map[string]bool{}
	for _, g := range existing {
		taken[g.Slug] = true
	}
	base := Slugify(name)
	if reservedSlugs[base] || isReservedLabel(name) {
		return Group{}, ItemChange{}, refuse(CodeReservedName, "that name is reserved")
	}
	if dup, err := nameTaken(ctx, tx, clubID, name, ""); err != nil {
		return Group{}, ItemChange{}, err
	} else if dup {
		return Group{}, ItemChange{}, refuse(CodeNameTaken, "another group has that name")
	}
	slug := uniqueSlug(base, taken)

	if _, err = scanGroup(tx.QueryRow(ctx,
		`INSERT INTO payment_filter_groups (club_id, slug, name, icon, sort_order, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+groupColumns,
		clubID, slug, name, in.Icon, sortOther-1, in.Enabled)); err != nil {
		return Group{}, ItemChange{}, err
	}
	// Before faktura among the groups that are not the fallback.
	var order []string
	for _, e := range existing {
		if e.isOther() {
			continue
		}
		if e.Builtin != nil && *e.Builtin == BuiltinFaktura {
			order = append(order, slug)
		}
		order = append(order, e.Slug)
	}
	if err := setOrder(ctx, tx, clubID, order); err != nil {
		return Group{}, ItemChange{}, err
	}
	g, err := getBySlug(ctx, tx, clubID, slug)
	if err != nil {
		return Group{}, ItemChange{}, err
	}
	var change ItemChange
	if len(in.PriceItemIDs) > 0 {
		change, err = setItems(ctx, tx, clubID, g, in.PriceItemIDs)
		if err != nil {
			return Group{}, ItemChange{}, err
		}
	}
	return g, change, nil
}

// UpdateInput is a partial change of a group. A nil field is left alone.
type UpdateInput struct {
	Name      *string
	ResetName bool
	Icon      *string
	Enabled   *bool
}

// Update changes a group's name, icon or enabled flag. It returns the group
// before and after, and whether anything changed (a no-op writes nothing).
func Update(ctx context.Context, tx Exec, clubID, slug string, in UpdateInput) (before, after Group, err error) {
	if err = begin(ctx, tx, clubID); err != nil {
		return
	}
	if before, err = getBySlug(ctx, tx, clubID, slug); err != nil {
		return
	}
	after = before
	if in.ResetName && in.Name != nil {
		err = refuse(CodeInvalidName, "set a name or reset it, not both")
		return
	}
	if in.Name != nil {
		var name string
		if name, err = NormalizeName(*in.Name); err != nil {
			return
		}
		if isReservedLabel(name) || (reservedSlugs[Slugify(name)] && !before.IsBuiltin()) {
			err = refuse(CodeReservedName, "that name is reserved")
			return
		}
		var dup bool
		if dup, err = nameTaken(ctx, tx, clubID, name, slug); err != nil {
			return
		} else if dup {
			err = refuse(CodeNameTaken, "another group has that name")
			return
		}
		after.Name = &name
	}
	if in.ResetName {
		if before.LabelKey == nil {
			err = refuse(CodeNoDefaultName, "this group has no default name")
			return
		}
		after.Name = nil
	}
	if in.Icon != nil {
		if !ValidIcon(*in.Icon) {
			err = refuse(CodeInvalidIcon, "unknown icon")
			return
		}
		after.Icon = *in.Icon
	}
	if in.Enabled != nil {
		if !*in.Enabled && before.isOther() {
			err = refuse(CodeBuiltinGroup, "the fallback group cannot be disabled")
			return
		}
		after.Enabled = *in.Enabled
	}
	if sameGroup(before, after) {
		return
	}
	_, err = tx.Exec(ctx,
		`UPDATE payment_filter_groups SET name = $3, icon = $4, enabled = $5, updated_at = now()
		  WHERE club_id = $1 AND slug = $2`,
		clubID, slug, after.Name, after.Icon, after.Enabled)
	return
}

func sameGroup(a, b Group) bool {
	if (a.Name == nil) != (b.Name == nil) || (a.Name != nil && *a.Name != *b.Name) {
		return false
	}
	return a.Icon == b.Icon && a.Enabled == b.Enabled
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// SetItems replaces the price items of an item group. An item that is in
// another group moves to this one; the change lists what moved so the caller
// can say so. A built-in has no items (refused).
func SetItems(ctx context.Context, tx Exec, clubID, slug string, ids []string) (Group, ItemChange, error) {
	if err := begin(ctx, tx, clubID); err != nil {
		return Group{}, ItemChange{}, err
	}
	g, err := getBySlug(ctx, tx, clubID, slug)
	if err != nil {
		return Group{}, ItemChange{}, err
	}
	if g.IsBuiltin() {
		return Group{}, ItemChange{}, refuse(CodeBuiltinGroup, "a built-in group has no price items")
	}
	change, err := setItems(ctx, tx, clubID, g, ids)
	return g, change, err
}

// cleanItemIDs lowercases the ids, drops repeats and refuses anything that is
// not a UUID before it reaches the database.
func cleanItemIDs(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.ToLower(id)
		if !uuidRE.MatchString(id) {
			return nil, refuse(CodeUnknownItem, "unknown price item")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// requireClubItems refuses ids that are not all price items of the club.
func requireClubItems(ctx context.Context, tx Exec, clubID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM price_items WHERE club_id = $1 AND id = ANY($2::uuid[])`, clubID, ids).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		return refuse(CodeUnknownItem, "unknown price item")
	}
	return nil
}

// itemHolders maps each of the given price items that is in a group other than
// exceptGroupID to the group it is in.
func itemHolders(ctx context.Context, tx Exec, ids []string, exceptGroupID string) (map[string]ItemMove, error) {
	rows, err := tx.Query(ctx,
		`SELECT gi.price_item_id::text, og.slug, og.id::text
		   FROM payment_filter_group_items gi
		   JOIN payment_filter_groups og ON og.id = gi.group_id
		  WHERE gi.price_item_id = ANY($1::uuid[]) AND gi.group_id <> $2::uuid`, ids, exceptGroupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ItemMove{}
	for rows.Next() {
		var m ItemMove
		if err := rows.Scan(&m.PriceItemID, &m.FromSlug, &m.FromID); err != nil {
			return nil, err
		}
		out[m.PriceItemID] = m
	}
	return out, rows.Err()
}

// setItems applies the new item set of g, under the lock and with built-ins in
// place. Every id must be a price item of the club.
func setItems(ctx context.Context, tx Exec, clubID string, g Group, ids []string) (ItemChange, error) {
	var change ItemChange
	want, err := cleanItemIDs(ids)
	if err != nil {
		return change, err
	}
	if err := requireClubItems(ctx, tx, clubID, want); err != nil {
		return change, err
	}

	// What the group holds now, and where the wanted items are today.
	held, err := ListGroupItemsOf(ctx, tx, g.ID)
	if err != nil {
		return change, err
	}
	current := map[string]bool{}
	for _, id := range held {
		current[id] = true
	}
	elsewhere, err := itemHolders(ctx, tx, want, g.ID)
	if err != nil {
		return change, err
	}

	wanted := map[string]bool{}
	for _, id := range want {
		wanted[id] = true
		if from, moved := elsewhere[id]; moved {
			change.Moved = append(change.Moved, from)
			change.Added = append(change.Added, id)
		} else if !current[id] {
			change.Added = append(change.Added, id)
		}
	}
	for _, id := range held {
		if !wanted[id] {
			change.Removed = append(change.Removed, id)
		}
	}

	if len(change.Removed) > 0 {
		if _, err := tx.Exec(ctx,
			`DELETE FROM payment_filter_group_items WHERE group_id = $1 AND price_item_id = ANY($2::uuid[])`,
			g.ID, change.Removed); err != nil {
			return change, err
		}
	}
	if len(change.Added) > 0 {
		// DO UPDATE moves an item out of its old group in the same statement.
		if _, err := tx.Exec(ctx,
			`INSERT INTO payment_filter_group_items (price_item_id, group_id)
			 SELECT unnest($1::uuid[]), $2::uuid
			 ON CONFLICT (price_item_id) DO UPDATE SET group_id = EXCLUDED.group_id`,
			change.Added, g.ID); err != nil {
			return change, err
		}
	}
	return change, nil
}

// Reorder sets the display order of the club's groups other than "other". The
// slugs must be exactly those groups; it returns the slugs before and after.
func Reorder(ctx context.Context, tx Exec, clubID string, slugs []string) (before, after []string, err error) {
	if err = begin(ctx, tx, clubID); err != nil {
		return
	}
	groups, err := listLocked(ctx, tx, clubID)
	if err != nil {
		return nil, nil, err
	}
	have := map[string]bool{}
	for _, g := range groups {
		if !g.isOther() {
			before = append(before, g.Slug)
			have[g.Slug] = true
		}
	}
	seen := map[string]bool{}
	for _, s := range slugs {
		if !have[s] || seen[s] {
			return nil, nil, refuse(CodeOrderMismatch, "the order must list each group except the fallback exactly once")
		}
		seen[s] = true
	}
	if len(slugs) != len(before) {
		return nil, nil, refuse(CodeOrderMismatch, "the order must list each group except the fallback exactly once")
	}
	after = slugs
	if equalStrings(before, after) {
		return before, after, nil
	}
	if err = setOrder(ctx, tx, clubID, slugs); err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

// setOrder numbers the given groups 10, 20, ... in that order, touching only
// the rows whose number changes. The fallback keeps its own position.
func setOrder(ctx context.Context, tx Exec, clubID string, slugs []string) error {
	_, err := tx.Exec(ctx,
		`UPDATE payment_filter_groups g
		    SET sort_order = o.pos * $3, updated_at = now()
		   FROM unnest($2::text[]) WITH ORDINALITY AS o(slug, pos)
		  WHERE g.club_id = $1 AND g.slug = o.slug AND g.sort_order <> o.pos * $3`,
		clubID, slugs, sortStep)
	return err
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// listLocked lists all groups of a club (disabled too) inside the write
// transaction.
func listLocked(ctx context.Context, tx Exec, clubID string) ([]Group, error) {
	return ListGroups(ctx, tx, clubID, true)
}

// Delete removes an item group; its items become unassigned. It returns the
// deleted group and the items it held.
func Delete(ctx context.Context, tx Exec, clubID, slug string) (Group, []string, error) {
	if err := begin(ctx, tx, clubID); err != nil {
		return Group{}, nil, err
	}
	g, err := getBySlug(ctx, tx, clubID, slug)
	if err != nil {
		return Group{}, nil, err
	}
	if g.IsBuiltin() {
		return Group{}, nil, refuse(CodeBuiltinGroup, "a built-in group cannot be deleted")
	}
	items, err := ListGroupItemsOf(ctx, tx, g.ID)
	if err != nil {
		return Group{}, nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM payment_filter_groups WHERE id = $1`, g.ID); err != nil {
		return Group{}, nil, err
	}
	return g, items, nil
}

// ListGroupItemsOf returns the price item ids of one group.
func ListGroupItemsOf(ctx context.Context, q Querier, groupID string) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT price_item_id::text FROM payment_filter_group_items WHERE group_id = $1 ORDER BY created_at, price_item_id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
