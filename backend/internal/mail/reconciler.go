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

// Reconciler converges per-mailbox ACLs in Stalwart with the set of
// users currently holding the mailbox's mapped role. Idempotent:
// `desired_hash == applied_hash` short-circuits the Stalwart call.
type Reconciler struct {
	db    *pgxpool.Pool
	admin *AdminClient
	audit *audit.Service
	spec  []MailboxSpec
	dry   bool
	log   zerolog.Logger
}

// NewReconciler. `dryRun` mirrors the BRYGGE_RECONCILER_DRY_RUN env
// flag and short-circuits the write path so cutovers can be staged.
func NewReconciler(db *pgxpool.Pool, admin *AdminClient, auditSvc *audit.Service, spec []MailboxSpec, dryRun bool, log zerolog.Logger) *Reconciler {
	return &Reconciler{
		db:    db,
		admin: admin,
		audit: auditSvc,
		spec:  spec,
		dry:   dryRun,
		log:   log.With().Str("component", "inbox-reconciler").Logger(),
	}
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
func (r *Reconciler) ReconcileAll(ctx context.Context) {
	for _, m := range r.spec {
		if !m.managed() || !strings.EqualFold(m.Type, "shared") {
			continue
		}
		if err := r.Reconcile(ctx, m.Address); err != nil {
			r.log.Warn().Err(err).Str("address", m.Address).Msg("reconcile failed")
		}
	}
}

// Reconcile a single address. Idempotent. Returns nil even when the
// Stalwart call was skipped due to hash match.
func (r *Reconciler) Reconcile(ctx context.Context, address string) error {
	spec, ok := r.findSpec(address)
	if !ok {
		return fmt.Errorf("no spec for %s", address)
	}
	if !spec.managed() || !strings.EqualFold(spec.Type, "shared") {
		return nil
	}

	desired, err := r.computeDesired(ctx, spec)
	if err != nil {
		return fmt.Errorf("compute desired: %w", err)
	}
	desiredHash := hashACLs(desired)

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

	if err := r.admin.SetMailboxACLs(ctx, address, desired); err != nil {
		_ = r.persistState(ctx, address, desiredHash, &priorApplied, err)
		r.logACLFailed(ctx, address, err)
		return fmt.Errorf("apply: %w", err)
	}

	r.logACLChanged(ctx, address, priorApplied, desiredHash, desired)
	return r.persistState(ctx, address, desiredHash, &desiredHash, nil)
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

// computeDesired builds the ACL set for a mailbox from the current
// user_roles view: every active user with the mapped role gets
// `read|append` (and `send_as` when the spec allows).
func (r *Reconciler) computeDesired(ctx context.Context, spec MailboxSpec) ([]MailboxACL, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id::text, u.email
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id AND ur.club_id = u.club_id
		WHERE ur.role = $1::user_role
		GROUP BY u.id, u.email
		ORDER BY u.email
	`, spec.Role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var acls []MailboxACL
	for rows.Next() {
		var uid, email string
		if err := rows.Scan(&uid, &email); err != nil {
			return nil, err
		}
		principal, err := r.admin.LookupPrincipal(ctx, email)
		if err != nil {
			// Skip this user but keep going — a single missing
			// principal shouldn't strand the rest of the ACL.
			r.log.Warn().Err(err).Str("email", email).Msg("principal lookup failed")
			continue
		}
		if principal == "" {
			// User has the role but no Stalwart account yet.
			// Common during onboarding; reconciler will pick
			// them up once the account is provisioned.
			continue
		}
		rights := []string{"read", "append"}
		if spec.SendAs {
			rights = append(rights, "send_as")
		}
		acls = append(acls, MailboxACL{PrincipalID: principal, Rights: rights})
	}
	return acls, rows.Err()
}

// hashACLs produces a canonical-form fingerprint of an ACL list so
// reconciler short-circuits work when nothing has changed since the
// last apply.
func hashACLs(acls []MailboxACL) string {
	type entry struct {
		ID     string   `json:"id"`
		Rights []string `json:"rights"`
	}
	canon := make([]entry, len(acls))
	for i, a := range acls {
		rights := append([]string(nil), a.Rights...)
		sort.Strings(rights)
		canon[i] = entry{ID: a.PrincipalID, Rights: rights}
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

func (r *Reconciler) logACLChanged(ctx context.Context, address, before, after string, acls []MailboxACL) {
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
			"member_count": len(acls),
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

