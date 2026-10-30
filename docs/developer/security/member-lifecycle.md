# Member lifecycle: deactivate, erase, purge

Removing a member is not a single "delete". Norwegian bookkeeping law
(bokføringsloven §13) requires the club to keep financially-relevant records —
invoices above all — for ~5 years after the accounting year they belong to,
while GDPR requires personal data to be erased once it is no longer needed. The
two obligations meet on the member record, so brygge splits "removal" into three
distinct states.

## The three actions

| Action | What it does | Reversible | When to use |
|---|---|---|---|
| **Deactivate** | Sets `users.deactivated_at`. Hides the member from active lists, member counts, the directory, broadcast/bulk-send recipients and dues runs; blocks login and drops their sessions. Their row, invoices and history are fully retained. | Yes — **Reactivate** clears it. | The normal "this person left the club" action. |
| **Erase** (`DELETE /admin/users/{id}`, admin only) | Anonymises the row in place, deactivates it, removes the member's Stalwart mailbox and scrubs their identity from the audit log. The row is never deleted: other tables reference it and payments cascade from it (BRD-10). A `?erasure=true` from an older caller is ignored. Locks the member out first, see [Locking a member out](#locking-a-member-out). Refused for the club's last active admin, see [Who may change an admin](#who-may-change-an-admin). | No | Rare. Prefer Deactivate; the scheduled purge handles PII erasure automatically. |
| **Purge** (automatic) | A daily background sweep (`accounting.PurgeExpiredMemberPII`) anonymizes the PII of *deactivated* members whose most recent invoice has cleared retention. Never deletes the row. | No | Nothing to do — it runs itself. |

## Anonymised address

`users` has `UNIQUE (club_id, email)`, so an anonymised member cannot share an
address with another. Every path that erases identity (the purge sweep, the
admin delete, a processed GDPR request) writes
`gen_random_uuid()::text || accounting.ErasedEmailSuffix` (`<uuid>@deleted.local`),
a fresh value per member. The suffix is the one exported constant; never spell it
out elsewhere. The sweep skips rows that carry it, which is what makes erasure
idempotent, and it also skips the constant `deleted@deleted` that earlier versions
wrote (one per club at most), so such a row is harmless but keeps that address
until someone rewrites it.

## Locking a member out

`revokeMemberAccess` (`handlers/gdpr.go`) is the single step that ends every way
a member can authenticate. It runs in the caller's transaction, before the
identity is anonymised because magic links are keyed by the old email. Both
`HandleProcessDeletion` and `HandleDeleteUser` call it; `HandleDeactivateUser`
(reversible) only sets `deactivated_at`, drops sessions and revokes the member's
developer tokens, since reactivation must keep passkeys and TOTP.

### Developer tokens

A developer token authenticates as its minter's admin role, so the member's
tokens are revoked outright (`revoked_at` set, by `revokeDeveloperTokens`, in
the same transaction) whenever one of these happens: the member is deactivated,
they lose the `admin` role (a role change that keeps `admin` leaves them alone),
an admin disables their TOTP (they can no longer mint), or `revokeMemberAccess`
runs (erase, GDPR processing). Reactivating or regranting the role never brings
a token back. `devtoken.Authenticate` still refuses a token whose minter is
deactivated or no longer holds the admin role; that is the backstop for paths
that do not revoke (direct SQL, a future role writer).

`HandleDeleteUser` runs the same lock-out step, then anonymises the row. Its
`delete_user` audit row records only `sessions_revoked`; the member is named by
`resource_id`. The refusal to delete your own account and the admin guards
happen before anything is revoked. `HandleDeleteUser`, `HandleDeactivateUser`
and `HandleReactivateUser` compare the canonical form of the `{userID}`
parameter with the caller's id (a non-UUID answers 404), because Postgres also
accepts uppercase and braced spellings of the same UUID.

### The mailbox

A member with a Stalwart mailbox (`user_mail_credentials`) has it removed as
the last step of the delete, still inside the transaction, by
`UserProvisioner.DeleteUserPrincipal` (idempotent: a principal already gone is
not an error). The call fails closed: if Stalwart cannot remove it the call
answers 502 `mailbox_deprovision_failed`, the transaction rolls back and the
member is exactly as before, so the admin retries. The alternative, committing
the erasure and logging an orphaned mailbox, would leave a live mailbox nobody
can find again, because the credentials row that names the principal is what
the removal reads. The only window left is a commit that fails after Stalwart
removed the mailbox: the member stays active without one and the retry
succeeds. The mailbox is only recreated when something calls
`EnsureUserPrincipal`: a magic-link login, a grant of a board-mailbox role, or
creating the user (not a passkey login), and a recreated mailbox is empty, so
mail that was stored in the removed one is lost. GDPR processing and the purge
sweep do not touch the mailbox yet (tracked as a follow-up).

## Who may change an admin

Decided in BRD-9 (docs/adr/board-workflows.md). The board reaches the
`/admin/users` writes, so the handlers add rules the route gate cannot:

| Action | Caller | Refusal |
|---|---|---|
| Delete (`DELETE /admin/users/{id}`) | admin only (route gate) | 403 from the gate for the board |
| Edit profile and email (`PATCH /admin/users/{id}`), set slips (`PUT …/slips`), deactivate, reactivate | board or admin for a member who does not hold `admin`; admin only for one who does | 403 `admin_required` |
| Role change (`PUT …/roles`), create with the `admin` role | board or admin, but admin only when the target holds `admin` now or the new set grants it | 403 `admin_required` |
| Delete, deactivate, demote, or process the GDPR deletion of the club's **last active admin** | admin | 409 `last_admin` |

The email is a login credential (magic links are sent to it, with no second
factor), so editing an admin's profile is a takeover path and sits under the same
rule as deactivating them. Every other write that takes a `{userID}` was checked
for the same gap:

- Admin-only at the route, so no board caller reaches them: boat create, update,
  delete and boat-slip (`/{userID}/boats…`), `POST …/totp/disable`,
  `DELETE …/passkeys`, CSV import and export.
- Reads (`GET /admin/users`, `/{userID}`, `…/boats`) are open to board viewers and
  change nothing.
- Outside `/admin/users` and left as they are, because they change nothing about
  the member's identity or access: the treasurer's pay-link read
  (`/admin/financials/users/{id}/pay-link`), waiting-list enroll (adds a queue
  entry), and the faktura routes (money, not identity). There is no
  impersonation route and no `/admin/members` group.

A new route that takes a `{userID}` and the board can reach must call
`requireAdminForAdminTarget` (or `checkAdminTarget`).

An active admin holds the `admin` role and has `deactivated_at IS NULL`; a
deactivated admin does not count. Every path that can remove one takes
`lockClubAdmins` first, a transaction-scoped advisory lock per club, then the
users row lock (the order matters: the reverse can deadlock), and counts the
others under it, so two simultaneous removals of the only two admins leave
exactly one. The paths are `HandleDeleteUser`, `HandleDeactivateUser`,
`HandleUpdateUserRoles` and `HandleProcessDeletion` (both outcomes deactivate
the member). The purge sweep only erases members who are already deactivated,
so it cannot reduce the active admins. A refused change writes no audit row.

An admin's own GDPR deletion request is therefore refused while they are the
last admin (409, request stays pending); once another admin exists, processing
deactivates and erases them like any member. Nobody can process their own
request, and an admin cannot delete or deactivate themselves here either.

## The audit log keeps no identity

`audit_log` rows are the record of who did what; they must not keep a name or
address once the member is erased. New rows about a member therefore hold ids
only: `resource_id` names the member, and `delete_user`, `user.deactivated`,
`user.reactivated` carry no name or email, `create_user` carries the roles,
`update_user` the names of the fields changed (never their values).

Erasing a member scrubs the identity keys (`audit.MemberPIIKeys`: email, names,
phone, address, `admin_notes`, `old_email`, `old_full_name`) from the details of
every row whose resource is that user or whose details name them under
`user_id`, through `audit.ScrubMemberPII`, in the same transaction as the
anonymisation. It runs from all three erasure paths: `HandleDeleteUser`,
`HandleProcessDeletion` when identity is erased now, and the purge sweep's
`purgeOneMember`. A GDPR request whose erasure is deferred behind retention does
not scrub; the sweep does when it erases. The rows stay and keep every other
key.

The table is append-only by convention, not by mechanism: no trigger, rule or
privilege stops an `UPDATE`, and the scrub is the one sanctioned rewrite.
Migration `000124` applies the same scrub once to rows written before this, for
members who are already erased (an `@deleted.local` or legacy `deleted@deleted`
address, or no users row); members who are active or only deactivated keep their
rows until they are erased. Not covered: `actor_ip` on rows the member acted in,
and details outside user-keyed rows that name a recipient (`invoice.emailed`,
`invoice.reminded`, `invoice.delivery_log_viewed`).

## Retention rule

`accounting.MemberFinancialRetention` is the single source of truth. A member's
most recent invoice from accounting year *Y* keeps their record in-window until
**1 January of Y+6** (5 full years past the year-end). A member with no invoices
has no obligation and is eligible for erasure immediately. Never re-derive this
date math elsewhere — call the helper.

## What survives erasure

Invoices snapshot the member's `member_name` / `member_email` at issue time, and
all display/PDF paths read the snapshot first. So after a member's PII is
anonymized, their historical invoices still show the original name and email —
the record stays legible and §13-compliant. See
[invariants.md](../reference/invariants.md) → *"A member with in-window financial
records is deactivated, never erased"* and *"Invoices snapshot the member's
identity at issue"*.

## GDPR deletion requests

A member-initiated deletion request (My page → Privacy) still runs after its
14-day cooling-off window, but reconciles with retention: data with no bookkeeping
obligation (push subscriptions, notification preferences, consents) is wiped
immediately, while identity erasure is **deferred** when retention still
applies, so the purge finishes the erasure once it clears. Either way the member
is locked out in the same transaction (`revokeMemberAccess`): the account is
deactivated, and sessions, passkeys, TOTP enrolment, recovery codes, every
magic link for the member's email, the durable pay link
(`users.payables_token`, so `/betal/bruker/{token}` answers 404 and no new link
can be minted for a deactivated member) and developer tokens the member created
are removed or revoked. An immediately erased member keeps `deactivated_at`
and an `@deleted.local` email, which the purge sweep skips. The request row is
locked (`FOR UPDATE`) while it is processed, so a second run is refused; an id
that is not a UUID answers 404. The audit row (`gdpr.deletion_processed`)
records the processing admin as actor with their IP, the erased user as the
resource, and `request_id`, `records_wiped`, `identity_erased`,
`retention_clears_at` and `sessions_revoked` (session rows deleted, including
expired ones the purge job had not yet removed) in its details. An admin cannot
process their own deletion request (400). Processing needs a fresh TOTP on top of
the admin step-up (BRY-509): the admin page asks for it on the click, before
the confirm dialog, and the route answers `totp_fresh_required` if the window
has lapsed.

### Timeline

| Moment | Date | Effect |
|---|---|---|
| Request | `requested_at` | The member's request is stored `pending`; `grace_end` is `requested_at` + 14 days (`deletionGracePeriod`). |
| Grace end | `grace_end` | Before this, `POST /admin/gdpr/deletion-requests/{id}/process` is refused (400 "grace period has not expired"). From here the request is actionable. The member can cancel any `pending` request until it is processed. |
| Deadline | `requested_at` + 1 month | GDPR Art. 12(3) gives the club one month to respond. A request still `pending` after this is overdue. |

The grace period is shorter than the legal deadline on purpose: it leaves about
two weeks to act. Anything that surfaces these requests as work (the planned
board task inbox, BRY-462) should show an item from `grace_end` and mark it
overdue at `requested_at` + 1 month, not from `requested_at`, when nobody can
act yet.

The queue and the legal-document editor (`/admin/gdpr/*`) are admin-only in the
API as well as the SPA: GDPR is one function, assigned to the site admin by
default (docs/adr/board-workflows.md, BRD-3). The member's own routes under
`/members/me` (data export, delete request, consents) are unaffected.
