package mail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
)

// MailboxSpec is one entry from /etc/brygge/board-mailboxes.json. The
// schema is shared with terraform.tfvars (board_mailboxes) and with
// the stalwart-mailbox-config systemd unit; keep field names aligned.
type MailboxSpec struct {
	Address       string `json:"address"`
	Role          string `json:"role"`
	DisplayName   string `json:"display_name"`
	Type          string `json:"type"` // "shared" | "list"
	SendAs        bool   `json:"send_as"`
	BccMembers    bool   `json:"bcc_members"`
	RetentionDays *int   `json:"retention_days,omitempty"`
	Managed       *bool  `json:"managed,omitempty"`
}

func (s MailboxSpec) managed() bool {
	if s.Managed == nil {
		return true
	}
	return *s.Managed
}

// LoadSpec reads the board-mailbox spec from disk. Returns an empty
// slice (not an error) when the file is absent — that's the supported
// "feature disabled" state on dev hosts.
func LoadSpec(path string) ([]MailboxSpec, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path) // #nosec G304 -- path comes from operator-controlled env
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read mailbox spec %s: %w", path, err)
	}
	var specs []MailboxSpec
	if err := json.Unmarshal(b, &specs); err != nil {
		return nil, fmt.Errorf("parse mailbox spec %s: %w", path, err)
	}
	return specs, nil
}

// Reconciler converges per-mailbox sharing in Stalwart with the set
// of users currently holding the mailbox's mapped role. Idempotent:
// `desired_hash == applied_hash` short-circuits the JMAP call.
//
// Mechanism: for each shared principal (e.g. `kasserar@`), the
// reconciler finds its Inbox via JMAP `Mailbox/get` and sets the
// folder's `shareWith` via JMAP `Mailbox/set` so every Brygge user
// currently holding the mapped role gets `mayRead | maySetSeen |
// mayAddItems | mayRemoveItems`. Stalwart 0.15 exposes no admin REST
// surface for ACLs — sharing is RFC 8621 §2.5 only.
type Reconciler struct {
	db        *pgxpool.Pool
	adminJMAP *JMAPClient // admin's own client, used only for Principal/get
	jmapFact  *JMAPFactory
	passwords PrincipalPasswords
	audit     *audit.Service
	spec      []MailboxSpec
	dry       bool
	log       zerolog.Logger

	indexCache  sync.Mutex
	cachedIndex map[string]string // shared between Reconcile() calls during a ReconcileAll loop
}

// NewReconciler. `dryRun` mirrors the BRYGGE_RECONCILER_DRY_RUN env
// flag and short-circuits the write path so cutovers can be staged.
// `passwords` maps each shared principal's address to its service
// password (set by stalwart-mailbox-config.service); an entry must
// exist for every spec address with `type=shared` before the write
// path can run.
func NewReconciler(db *pgxpool.Pool, adminJMAP *JMAPClient, jmapFact *JMAPFactory, passwords PrincipalPasswords, auditSvc *audit.Service, spec []MailboxSpec, dryRun bool, log zerolog.Logger) *Reconciler {
	return &Reconciler{
		db:        db,
		adminJMAP: adminJMAP,
		jmapFact:  jmapFact,
		passwords: passwords,
		audit:     auditSvc,
		spec:      spec,
		dry:       dryRun,
		log:       log.With().Str("component", "inbox-reconciler").Logger(),
	}
}

// principalIndex builds {name: jmap_id} from admin's JMAP
// Principal/get. We index by `name` (Stalwart's login slug) rather
// than `email` because users carry their real email in users.email
// (e.g. gmail.com) while their Stalwart principal carries a
// synthetic `<slug>@<club_domain>` — see DIL-321 / UserProvisioner.
// computeDesired then resolves email→slug via user_mail_credentials.
func (r *Reconciler) principalIndex(ctx context.Context) (map[string]string, error) {
	adminAccounts, err := r.adminJMAP.SessionAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin session: %w", err)
	}
	if len(adminAccounts) == 0 {
		return nil, errors.New("admin JMAP session has no accounts")
	}
	principals, err := r.adminJMAP.ListPrincipals(ctx, adminAccounts[0])
	if err != nil {
		return nil, fmt.Errorf("list principals: %w", err)
	}
	idx := make(map[string]string, len(principals))
	for _, p := range principals {
		if p.Name == "" {
			continue
		}
		idx[strings.ToLower(p.Name)] = p.ID
	}
	return idx, nil
}

// HasMailboxes reports whether anything is configured. Callers should
// skip wiring the cron / role hook when this is false to avoid noisy
// logs on dev hosts.
func (r *Reconciler) HasMailboxes() bool {
	for _, s := range r.spec {
		if s.managed() && strings.EqualFold(s.Type, "shared") {
			return true
		}
	}
	return false
}

// ReconcileAll iterates every managed shared mailbox. Errors on
// individual mailboxes are logged and recorded in mailbox_sync_state
// but do not abort the loop — partial drift recovery is the goal.
//
// The principal index (email→jmap_id) is fetched once and cached on
// the receiver for the duration of this call so per-mailbox
// Reconcile() doesn't repeat the call.
func (r *Reconciler) ReconcileAll(ctx context.Context) {
	idx, err := r.principalIndex(ctx)
	if err != nil {
		r.log.Warn().Err(err).Msg("principal index fetch failed; per-mailbox calls will refetch")
		idx = nil
	}
	r.indexCache.Lock()
	r.cachedIndex = idx
	r.indexCache.Unlock()
	defer func() {
		r.indexCache.Lock()
		r.cachedIndex = nil
		r.indexCache.Unlock()
	}()

	for _, m := range r.spec {
		if !m.managed() || !strings.EqualFold(m.Type, "shared") {
			continue
		}
		if err := r.Reconcile(ctx, m.Address); err != nil {
			r.log.Warn().Err(err).Str("address", m.Address).Msg("reconcile failed")
		}
	}
}

// currentIndex returns the cached principal index for the duration
// of a ReconcileAll loop, or fetches a fresh one for a stand-alone
// Reconcile() call.
func (r *Reconciler) currentIndex(ctx context.Context) (map[string]string, error) {
	r.indexCache.Lock()
	idx := r.cachedIndex
	r.indexCache.Unlock()
	if idx != nil {
		return idx, nil
	}
	return r.principalIndex(ctx)
}

// Reconcile a single address. Idempotent. Returns nil even when the
// JMAP call was skipped due to hash match.
func (r *Reconciler) Reconcile(ctx context.Context, address string) error {
	spec, ok := r.findSpec(address)
	if !ok {
		return fmt.Errorf("no spec for %s", address)
	}
	if !spec.managed() || !strings.EqualFold(spec.Type, "shared") {
		return nil
	}

	idx, err := r.currentIndex(ctx)
	if err != nil {
		return fmt.Errorf("principal index: %w", err)
	}

	desired, err := r.computeDesired(ctx, spec, idx)
	if err != nil {
		return fmt.Errorf("compute desired: %w", err)
	}
	desiredHash := hashShareWith(desired)

	priorApplied, _, err := r.loadState(ctx, address)
	if err != nil {
		r.log.Warn().Err(err).Str("address", address).Msg("load sync state")
	}

	if priorApplied != "" && priorApplied == desiredHash {
		// No-op: hash check covers the steady state where nothing
		// changed since the last successful apply. Still bump
		// last_synced so operators see liveness.
		return r.persistState(ctx, address, desiredHash, &desiredHash, nil)
	}

	if r.dry {
		r.log.Info().
			Str("address", address).
			Str("desired_hash", desiredHash).
			Str("applied_hash", priorApplied).
			Int("members", len(desired)).
			Msg("dry-run: would apply ACLs")
		return r.persistState(ctx, address, desiredHash, &priorApplied, nil)
	}

	pw := r.passwords.Get(address)
	if pw == "" {
		err := fmt.Errorf("no service password for %s — has stalwart-mailbox-config run?", address)
		_ = r.persistState(ctx, address, desiredHash, &priorApplied, err)
		r.logACLFailed(ctx, address, err)
		return err
	}
	jmap := r.jmapFact.AsPrincipal(principalName(address), pw)

	ownerID, inboxID, err := r.resolveInbox(ctx, jmap, address)
	if err != nil {
		_ = r.persistState(ctx, address, desiredHash, &priorApplied, err)
		r.logACLFailed(ctx, address, err)
		return fmt.Errorf("resolve inbox: %w", err)
	}

	if err := jmap.SetMailboxShareWith(ctx, ownerID, inboxID, desired); err != nil {
		_ = r.persistState(ctx, address, desiredHash, &priorApplied, err)
		r.logACLFailed(ctx, address, err)
		return fmt.Errorf("apply: %w", err)
	}

	r.logACLChanged(ctx, address, priorApplied, desiredHash, len(desired))
	return r.persistState(ctx, address, desiredHash, &desiredHash, nil)
}

// resolveInbox finds the principal's account id (from JMAP's session
// — Stalwart's JMAP IDs are alphabetic and differ from the admin REST
// API's numeric ids) and the Inbox folder's mailbox id. Both are
// scoped to the principal we're authenticated as.
func (r *Reconciler) resolveInbox(ctx context.Context, jmap *JMAPClient, address string) (ownerID, mailboxID string, err error) {
	accounts, err := jmap.SessionAccounts(ctx)
	if err != nil {
		return "", "", fmt.Errorf("session: %w", err)
	}
	if len(accounts) == 0 {
		return "", "", fmt.Errorf("no JMAP account for %s", address)
	}
	// A shared principal sees only its own account in its session.
	ownerID = accounts[0]
	mboxes, err := jmap.ListMailboxes(ctx, ownerID)
	if err != nil {
		return "", "", err
	}
	for _, m := range mboxes {
		if strings.EqualFold(m.Role, "inbox") {
			return ownerID, m.ID, nil
		}
	}
	return "", "", fmt.Errorf("inbox folder not found on %s", address)
}

// OnRoleChanged is called by the user-roles mutation path (insert /
// delete) to trigger a low-latency reconcile of mailboxes affected by
// that user's role set. Runs in a detached goroutine so the HTTP
// handler doesn't block on Stalwart.
func (r *Reconciler) OnRoleChanged(userID string) {
	if !r.HasMailboxes() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Phase 1 keeps this simple: reconcile every shared mailbox.
		// Mailbox count is O(handful); per-user filtering can land
		// later if it becomes hot.
		_ = userID
		r.ReconcileAll(ctx)
	}()
}

func (r *Reconciler) findSpec(address string) (MailboxSpec, bool) {
	for _, m := range r.spec {
		if strings.EqualFold(m.Address, address) {
			return m, true
		}
	}
	return MailboxSpec{}, false
}

// computeDesired builds the shareWith set for a shared mailbox from
// the current user_roles view: every active user with the mapped
// role AND a provisioned Stalwart principal gets the standard
// reader/contributor rights. Keyed by the receiving user's JMAP
// account ID, resolved by joining user_mail_credentials (which
// holds the Stalwart slug) against the name→jmap_id index.
func (r *Reconciler) computeDesired(ctx context.Context, spec MailboxSpec, idx map[string]string) (map[string]ShareRights, error) {
	rows, err := r.db.Query(ctx, `
		SELECT umc.jmap_user
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		JOIN user_mail_credentials umc ON umc.user_id = u.id
		WHERE ur.role = $1::user_role
		GROUP BY umc.jmap_user
		ORDER BY umc.jmap_user
	`, spec.Role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	shares := make(map[string]ShareRights)
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		jmapID, ok := idx[strings.ToLower(slug)]
		if !ok || jmapID == "" {
			// User is in our DB but their Stalwart principal
			// vanished — skip silently. The next provisioning
			// pass (lazy on login or eager on role grant) will
			// recreate it; reconciler picks them up next cycle.
			continue
		}
		shares[jmapID] = ShareRights{
			MayReadItems:   true,
			MaySetSeen:     true,
			MaySetKeywords: true,
			MayAddItems:    true,
			MayRemoveItems: true,
		}
		// `send_as` is account-level delegation, not a per-mailbox
		// right in RFC 8621. Skipped here; the compose surface
		// (DIL-278) will need to gate it separately.
		_ = spec.SendAs
	}
	return shares, rows.Err()
}

// hashShareWith produces a canonical-form fingerprint of a shareWith
// map so the reconciler short-circuits when nothing has changed.
func hashShareWith(m map[string]ShareRights) string {
	type entry struct {
		ID     string      `json:"id"`
		Rights ShareRights `json:"rights"`
	}
	canon := make([]entry, 0, len(m))
	for id, r := range m {
		canon = append(canon, entry{ID: id, Rights: r})
	}
	sort.Slice(canon, func(i, j int) bool { return canon[i].ID < canon[j].ID })
	b, _ := json.Marshal(canon)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (r *Reconciler) loadState(ctx context.Context, address string) (applied string, lastSynced *time.Time, err error) {
	var ts *time.Time
	var app *string
	err = r.db.QueryRow(ctx,
		`SELECT applied_hash, last_synced FROM mailbox_sync_state WHERE address = $1`,
		address,
	).Scan(&app, &ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if app != nil {
		applied = *app
	}
	return applied, ts, nil
}

func (r *Reconciler) persistState(ctx context.Context, address, desired string, applied *string, applyErr error) error {
	var lastErr *string
	if applyErr != nil {
		s := applyErr.Error()
		lastErr = &s
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO mailbox_sync_state (address, desired_hash, applied_hash, last_synced, last_error)
		VALUES ($1, $2, $3, now(), $4)
		ON CONFLICT (address) DO UPDATE SET
			desired_hash = EXCLUDED.desired_hash,
			applied_hash = EXCLUDED.applied_hash,
			last_synced  = EXCLUDED.last_synced,
			last_error   = EXCLUDED.last_error
	`, address, desired, applied, lastErr)
	return err
}

func (r *Reconciler) logACLChanged(ctx context.Context, address, before, after string, memberCount int) {
	if r.audit == nil {
		return
	}
	r.audit.Log(ctx, audit.Entry{
		Action:     audit.ActionInboxACLChanged,
		Resource:   "mailbox",
		ResourceID: address,
		Details: map[string]any{
			"before_hash":  before,
			"after_hash":   after,
			"member_count": memberCount,
		},
	})
}

func (r *Reconciler) logACLFailed(ctx context.Context, address string, err error) {
	if r.audit == nil {
		return
	}
	r.audit.Log(ctx, audit.Entry{
		Action:     audit.ActionInboxACLSyncFailed,
		Resource:   "mailbox",
		ResourceID: address,
		Details:    map[string]any{"error": err.Error()},
	})
}

