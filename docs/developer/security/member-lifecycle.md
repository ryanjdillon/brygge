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
| **Erase** (`DELETE /admin/users/{id}?erasure=true`) | For a member with financial records: refused while those records are in-window; once retention has cleared it scrubs their PII in place (the row stays because invoices reference it). For a member with **no** financial records: hard-deletes the row. | No | Rare. Prefer Deactivate; the scheduled purge handles PII erasure automatically. |
| **Purge** (automatic) | A daily background sweep (`accounting.PurgeExpiredMemberPII`) anonymizes the PII of *deactivated* members whose most recent invoice has cleared retention. Never deletes the row. | No | Nothing to do — it runs itself. |

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
immediately, while identity erasure is **deferred** — the member is deactivated
so the purge finishes the erasure once retention clears. The audit row
(`gdpr.deletion_processed`) records `records_wiped`, `identity_erased` and
`retention_clears_at`.

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
