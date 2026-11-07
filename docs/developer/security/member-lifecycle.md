# Member lifecycle: join, cancel, deactivate, erase, purge

A person enters the club through the self-service join (or the board adds
them) and leaves through a cancellation, a deactivation or an erasure.
Removing a member is not a single "delete". Norwegian bookkeeping law
(bokføringsloven §13) requires the club to keep financially-relevant records —
invoices above all — for ~5 years after the accounting year they belong to,
while GDPR requires personal data to be erased once it is no longer needed. The
two obligations meet on the member record, so brygge splits "removal" into
deactivation (reversible, keeps everything) and erasure (anonymises in place,
never deletes the row).

The decisions behind the join and cancel paths are BRD-17, BRD-15, BRD-16
([board-workflows.md](../../adr/board-workflows.md)) and ACC-14
([accounting.md](../../adr/accounting.md)); the board and member guides are
[join.md](../../user/join.md), [members.md](../../user/members.md) and
[membership.md](../../user/membership.md).

## The lifecycle

| Stage | Entered by | State | Code |
|---|---|---|---|
| **Pending join** | `POST /join` | A `membership_applications` row (token hash, typed details, consent versions shown). No user, role or order. The link is valid 24 hours; the hourly sweep deletes the row once it has expired, confirmed or not. | `handlers/join.go` |
| **Applicant** | `POST /join/confirm` | A `users` row (new, or a deactivated former member reactivated) holding only `applicant`, consents recorded, a pending `membership_join` order. Can sign in, pay and see their own fakturas, nothing else ([applicant gate](../reference/invariants.md#an-applicant-only-session-reaches-only-what-finishes-joining)). | `confirmJoinUser` |
| **Member** | Real money settles the join (card/Vipps capture, or a bank payment of the join faktura), or the board gives the `member` role | `member` replaces `applicant`; `member_since` set if unset. | `membership.grantJoinMembershipTx` |
| **Cancelled** | The member cancels online | Still an active member; `membership_cancelled_at`, `membership_ends_on` (31 December), optional reason. Undo while active. | `handlers/membership_cancel.go` |
| **Ended** | The daily job, from the day after `membership_ends_on`; or the board's Deactivate at any time | Deactivated through `DeactivateMemberTx`; waiting-list entries withdrawn (the job only). Reversible by Reactivate. | `handlers.EndCancelledMemberships` |
| **Purged** | The daily purge, once retention clears; or an admin erase, or a processed GDPR request | Identity anonymised in place, audit identity scrubbed. Not reversible. | `accounting.PurgeExpiredMemberPII` |

Side paths:

- **Expiry.** An applicant whose join stays unpaid 60 days after its last activity is deactivated through `revokeMemberAccess`, with a join faktura credited, never deleted. See [An abandoned join ends in deactivation](#an-abandoned-join-ends-in-deactivation).
- **Rejoin.** A deactivated former member (not yet erased) who joins again is reactivated at confirm as an applicant: every role they held is removed, the typed details replace the old ones, and any cancellation is cleared. `member_since` is kept, so the grant does not move it. An erased member's address no longer matches, so they join as a new user. The board blocks a person from rejoining with `users.rejoin_blocked` (BRD-15): the form then creates nothing and mails them a pointer to the board.
- **GDPR deletion.** A member's own request, processed by an admin after 14 days, locks them out and erases identity now or once retention clears. See [GDPR deletion requests](#gdpr-deletion-requests).

## Two deactivation paths

Every deactivation sets `users.deactivated_at`, but there are two services, and
which one a path uses decides what survives:

| | `DeactivateMemberTx` (`handlers/member_lifecycle.go`) | `revokeMemberAccess` (`handlers/gdpr.go`) |
|---|---|---|
| Used by | The board's Deactivate (`HandleDeactivateUser`) and the year-end end of a cancelled membership | Admin erase (`HandleDeleteUser`), GDPR processing (`HandleProcessDeletion`, both outcomes) and join expiry |
| Purpose | Reversible: the person left, and may be reactivated | Scrubbing: end every way to authenticate before the identity goes, or for an applicant who never paid |
| Removes | Sessions and developer tokens | Sessions, developer tokens, passkeys, TOTP and recovery codes, magic links for the email, the durable pay link |
| Keeps | Passkeys and TOTP, so Reactivate restores the member as they were | Nothing to log in with |
| Last-admin guard | Takes `lockClubAdmins` and refuses the club's only active admin (`errLastAdmin`) | None of its own; the erase and GDPR callers take the lock and check first; join expiry selects applicant-only users, who cannot be admins |
| Audit | Writes `user.deactivated` | Writes none; the caller logs its own action (`delete_user`, `gdpr.deletion_processed`, `membership.join_expired`) |

A new path that deactivates someone for a membership reason uses
`DeactivateMemberTx`; one that removes access before erasure uses
`revokeMemberAccess`; these two are the only writers that set `deactivated_at`.

## The actions

| Action | What it does | Reversible | When to use |
|---|---|---|---|
| **Deactivate** (board or admin, `DeactivateMemberTx`) | Sets `users.deactivated_at`. Hides the member from active lists, member counts, the directory, broadcast/bulk-send recipients and dues runs; blocks login and drops their sessions. Their row, invoices and history are fully retained. | Yes — **Reactivate** clears it. | The normal "this person left the club" action. |
| **Erase** (`DELETE /admin/users/{id}`, admin only) | Anonymises the row in place, deactivates it, removes the member's Stalwart mailbox and scrubs their identity from the audit log. The row is never deleted: other tables reference it and payments cascade from it (BRD-10). A `?erasure=true` from an older caller is ignored. Locks the member out first, see [Locking a member out](#locking-a-member-out). Refused for the club's last active admin, see [Who may change an admin](#who-may-change-an-admin). | No | Rare. Prefer Deactivate; the scheduled purge handles PII erasure automatically. |
| **Cancel** (member, `POST /members/me/membership/cancel`) | The member cancels online (BRY-633). Sets `membership_cancelled_at`, `membership_ends_on` (31 December of the year cancelled) and the optional `membership_cancel_reason`; the member stays active until then. From the day after `ends_on` the daily membership-end job deactivates them, see [Cancelling online](#cancelling-online). | While the member is active: `POST …/undo`. After: the board reactivates (the waiting-list place is not restored). | The member leaving at year end. |
| **Purge** (automatic) | A daily background sweep (`accounting.PurgeExpiredMemberPII`) anonymizes the PII of *deactivated* members whose most recent invoice has cleared retention. Never deletes the row. | No | Nothing to do — it runs itself. |

## An abandoned join ends in deactivation

An applicant whose self-service join stays unpaid 60 days after its last
activity (BRY-631, [ACC-14](../../adr/accounting.md)) is ended by the hourly
sweep (`InvoiceHandler.ExpireUnpaidJoins`):

- **Never billed** (only the pending join order): the order is expired and the
  applicant deactivated. With no faktura they are purge-eligible at once, so
  the purge erases their identity on its next run.
- **Billed** (they chose "Betal med bankoverføring"): the join faktura is credited in
  full, never deleted, and the applicant deactivated; the purge erases them
  once the faktura's retention clears.
- **Part of the money in:** nothing is ended; the board gets one review notice.

Deactivation goes through `revokeMemberAccess` (see
[Two deactivation paths](#two-deactivation-paths)), with `deactivated_by` the
club's system actor (or NULL without one). No path deletes the `users` row or
rewrites their audit rows. The rules are in
[invariants.md](../reference/invariants.md#an-unpaid-join-expires-after-60-days-a-faktura-is-credited-never-deleted).

## Cancelling online

Decided in BRD-17 (D5 and R8, BRY-476): a member
cancels from the portal, the cancellation takes effect at the end of the year
(31 December, server-local), nothing paid is refunded, and the confirmation
email is the written notice. The member stays a full member until then and may
undo it for as long as they are active: through `ends_on`, and past it while
the job could not deactivate them (the last admin below). Once deactivated,
undo answers 409 `membership_ended`.

- **Who.** Any session past the applicant gate, including a board member who
  holds no `member` role. An applicant-only session never reaches the routes.
- **What is queued.** The cancel queues the member's receipt
  (`membership_cancel_receipt`) and the board's notice
  (`membership_cancel_board_notice`); an undo queues `membership_cancel_undone`.
  Each is keyed by `payload->>'ref'` (the member and the `cancelled_at` instant),
  so a repeated request queues nothing, and cancel, undo, cancel is a new
  cancellation with its own notices. The emails are rendered when the worker
  sends them: the end date and (for the receipt) whether the member was on
  the waiting list come from the payload, fixed at cancel time, but the
  member's name and **reason are read from the user row at send time**, so a
  reason cleared by an undo before the board notice is sent does not appear
  in it.
- **The end.** The daily job (`handlers.EndCancelledMemberships`, run from the
  daily loop in `cmd/api/main.go`) picks every active person whose
  `membership_ends_on` is before today and, in one transaction per member and as
  the club's system actor, re-reads the cancellation under the users row lock
  (an undo, or a board deactivate and reactivate, since the list was read
  wins), deactivates them through `DeactivateMemberTx` (the
  same service as the board's button: sessions dropped, developer tokens
  revoked, `user.deactivated` audited), withdraws their waiting-list entries
  (`WithdrawWaitingListTx`, active and offered) and writes `membership.ended`.
  Roles, slips and the harbor share are left for the board.
- **The last admin.** If the member is the club's only active admin,
  `DeactivateMemberTx` returns `errLastAdmin`: the transaction rolls back,
  nothing is withdrawn, and the board gets one `membership_end_refused` notice
  per member and end date. Once another admin exists, the next daily run ends
  the membership.
- **Reactivation.** `HandleReactivateUser` clears a cancellation whose
  `ends_on` has passed, or the next daily run would deactivate the member again.
  A cancellation still running is kept. Reactivation does **not** restore the
  waiting-list entries the job withdrew: the member's place is lost, and the
  reactivate confirmation says so.
- **Erasure.** `membership_cancel_reason` is the member's own text: every
  erasure path clears it (`accounting.EraseMemberMembershipData`, which also
  clears the address-verification columns and deletes the club's
  `membership_applications` for the member's address), and it never enters the
  audit log (`membership.cancelled` records only `reason_given`). See
  [invariants.md](../reference/invariants.md#erasure-clears-the-cancellation-reason-the-address-check-and-the-join-requests).

## The system actor is outside the lifecycle

A club's automatic-booking actor (`users.kind = 'system'`, "Automatisk bokføring", ACC-13) is a `users` row only so that every "who did it" column stays a real reference. It is not a member: it holds no personal data, has no role, cannot log in, files no deletion request and is never deactivated. Every lifecycle action treats it as absent. The admin by-id endpoints (deactivate, reactivate, erase) answer 404 for it (`loadPersonUser`), `revokeMemberAccess` refuses it, the purge sweep selects only `kind = 'person'`, and `audit.ScrubMemberPII` leaves its audit rows alone: its id in `actor_id` is the intended record of "the automatic run", and an id is not personal data. See [invariants.md](../reference/invariants.md#the-system-actor-is-not-a-person).

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
identity is anonymised because magic links are keyed by the old email.
`HandleProcessDeletion`, `HandleDeleteUser` and the join expiry (BRY-631) call
it. The reversible deactivation (`DeactivateMemberTx`) removes less, so that
reactivation keeps passkeys and TOTP; the two are compared in
[Two deactivation paths](#two-deactivation-paths).

### Developer tokens

A developer token authenticates as its minter's admin role, so the member's
tokens are revoked outright (`revoked_at` set, by `revokeDeveloperTokens`, in
the same transaction) whenever one of these happens: the member is deactivated,
they lose the `admin` role (a role change that keeps `admin` leaves them alone),
an admin disables their TOTP (they can no longer mint), or `revokeMemberAccess`
runs (erase, GDPR processing, join expiry). Reactivating or regranting the role never brings
a token back. `devtoken.Authenticate` still refuses a token whose minter is
deactivated or no longer holds the admin role; that is the backstop for paths
that do not revoke (direct SQL, a future role writer).

`HandleDeleteUser` runs the same lock-out step, deletes the member's mail log
(see [GDPR deletion requests](#gdpr-deletion-requests)), then anonymises the row. Its
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
exactly one. The paths are `HandleDeleteUser`, `HandleDeactivateUser` and the
membership-end job (both through `DeactivateMemberTx`, which takes the lock
itself; it is re-entrant, so a handler that took it first may call it),
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
obligation (push subscriptions, notification preferences, consents, and the
mail log: `email_sends` rows naming the member or sent to their address in the
club, and SES events that matched no send but carry that address) is wiped
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
