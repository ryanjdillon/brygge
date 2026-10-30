# Board workflow decisions

See [README.md](README.md) for the format. The board task inbox spec and its implementation issues are on BRY-462 and BRY-461.

### BRD-1: Run safe steps automatically; show the rest to the person responsible

- **Status:** accepted · 2026-10-28 · BRY-461
- **Decision:** sync and update steps across Brygge run automatically when that is safe. Anything that needs a person appears in a task inbox for the board member responsible, with a link to the right page and a short instruction.
- **Why:** manual steps get forgotten when they sit on a page nobody visits; a board changes every year and new members don't know where to look.

### BRD-2: The task modal shows on each login

- **Status:** accepted · 2026-10-29 · BRY-462
- **Decision:** a board member with tasks due sees the task modal each time they log in. It never blocks: it can be closed. A badge and a "Til behandling" page are always available. Active notifications (email or push) and a board digest are to be discussed separately (BRY-492).

### BRD-3: Tasks are routed by function, through a board configuration with reserves

- **Status:** accepted · 2026-10-29 · BRY-462, BRY-482
- **Decision:** each club configures which role or person handles each function (accounting, harbour, membership, the contact form, GDPR, …), with reserves. Board status comes from holding any board role. A task goes to the holder of its function, then to the reserve, then to the site admin. Role-specific tasks go only to that role; the site admin is the exception and sees everything. GDPR is a function, assigned to the site admin by default; the GDPR admin API is admin-only to match (BRY-481). The contact-form recipient is configurable (secretary, chair or vice-chair).
- **Why:** clubs divide board work differently (PLAT-1); reserves cover holidays and vacant roles without everyone seeing everything.

### BRD-4: Automatic faktura reminders stay off until email is reliable

- **Status:** accepted · 2026-10-29 · BRY-462, BRY-493
- **Decision:** automatic reminders for overdue fakturas are wanted, as an opt-in club setting that is off by default, and they are not enabled until outbound email is reliable. Until then the inbox shows overdue fakturas for a manual reminder.

### BRD-5: Bookings are confirmed by a person; event rental has its own process

- **Status:** accepted · 2026-10-29 · BRY-462, BRY-484
- **Decision:** guest-slip and room bookings are confirmed manually from the inbox. Renting out the venue for events needs its own process with a manual review, specified separately.

### BRD-6: Membership becomes a fully online process

- **Status:** accepted · 2026-10-29 · BRY-476
- **Decision:** applying, paying, becoming a member and cancelling all happen online without board steps, and the board gets an overview of active members. Until that ships, membership applications are inbox tasks.

### BRD-7: Harbour share (moloandel) offers follow a recorded board decision

- **Status:** accepted · 2026-10-29 · BRY-78
- **Decision:** a board decision triggers an offer to the next person on the list by email or text. They accept online, with the terms snapshotted, which issues a faktura; once paid and settled the share is allocated. A timeout or a decline offers the next person, and the person who declined chooses whether to stay on the list. Reordering the list is allowed only with a reason, and is recorded in the board decision log.

### BRD-8: Contact-form messages are mailed to the secretary, else the chairman

- **Status:** accepted · 2026-10-29 · BRY-475, BRY-482
- **Decision:** the public contact form mails each message, synchronously, to the club's `secretary_email`, falling back to `chairman_email`, with Reply-To set to the submitter and From the app's own sender. A failed or impossible delivery is an error the visitor sees (502/503), never a silent success. The recipient moves to the BRD-3 function allocation once BRY-482 ships; until then the address is read from club settings, which works on every deploy.
- **Why:** the old handler only logged the submission and answered success, so every enquiry was lost and the visitor was never told. Settings are used rather than a new mailbox flag because they exist on every deploy, Compose included.
- **Consequences:** the rules a public mail-sending route must keep are in the invariant "A public route that sends mail never lets the visitor choose From or To" in [invariants.md](../developer/reference/invariants.md); the recipient lookup is `contactRecipient` in `backend/internal/handlers/contact.go`, the one function BRY-482 changes.

### BRD-9: Changing an admin is an admin's call, and the club always keeps one

- **Status:** accepted · 2026-10-30 · BRY-535
- **Decision:** deleting a member is admin-only. Deactivating and reactivating stay open to the board for ordinary members, because they are reversible and used for membership work, but any write on a member who holds the `admin` role (profile and email, slips, deactivate, reactivate, roles), and granting or removing that role, needs an admin caller; reads stay open to board viewers, and the other writes that take a user id (boats, TOTP, passkeys, CSV) are admin-only at the route. The email is a login credential, so a board edit of an admin's email would be an account takeover. Deleting, deactivating, demoting or erasing the club's last active admin is refused with a typed 409 (`last_admin`), decided under a per-club lock so two simultaneous calls cannot both pass. Processing a GDPR deletion request for the last admin is refused the same way; the request stays pending until another admin exists.
- **Why:** a board member with a fresh TOTP could delete or deactivate an admin, and since BRY-525 a delete also removes the admin's passkeys, TOTP and developer tokens. If that admin was the club's only one, nobody could undo it and recovery needed SQL. A club cannot be left to depend on a person it can no longer reach.
- **Consequences:** the route gate and the guards (`checkAdminTarget`, `ensureNotLastActiveAdmin`, `lockClubAdmins`) are in `backend/internal/handlers/admin_guard.go`; the rules and the one case left to people (an admin's own erasure request) are in [member-lifecycle.md](../developer/security/member-lifecycle.md#who-may-change-an-admin), and the invariant is "The club always keeps an active admin" in [invariants.md](../developer/reference/invariants.md).

### BRD-10: Deleting a member always anonymises the row in place

- **Status:** accepted · 2026-10-30 · BRY-537
- **Decision:** `DELETE /admin/users/{id}` anonymises the member in place and locks them out; it never deletes the row. The `?erasure=true` option is gone (an old caller that sends it gets the same anonymisation). The member's Stalwart mailbox is removed in the same call, before the credentials row that names it is touched, and a mailbox that cannot be removed fails the call (502 `mailbox_deprovision_failed`) with nothing changed. When the anonymisation waits for the retention window is a separate decision (BRY-538).
- **Why:** a hard delete answered 500 whenever another table pointed at the member without a cascade (audit actor, deletion request, order, journal author), and silently dropped the member's payments, which cascade from the user, when retention only looked at invoices. It also orphaned the Stalwart principal. Anonymising in place is what invoices and the purge sweep already rely on.
- **Consequences:** [member-lifecycle.md](../developer/security/member-lifecycle.md) describes the flow; `HandleDeleteUser` in `backend/internal/handlers/admin_users.go`.

### BRD-11: The audit log holds ids, and erasing a member scrubs their identity from it

- **Status:** accepted · 2026-10-30 · BRY-536
- **Decision:** audit rows about a member store ids only (the member is `resource_id`, or `user_id` in the details), never a name, email, phone or address. Erasing a member, by an admin delete, an immediately erased GDPR request, or the purge sweep, removes the identity keys from the details of every row about them, in the same transaction, and keeps the rows and their other keys. Migration 000124 applies the same scrub once to rows written before this, for members who are already erased; members who are active or only deactivated keep theirs until they are erased.
- **Why:** the delete and deactivate rows stored the member's email and name, and nothing ever removed them, so an erased member stayed named in the log indefinitely, which defeated the erasure. The log is still the record of who did what and when, so the rows stay.
- **Consequences:** `audit.ScrubMemberPII` and `audit.MemberPIIKeys` in `backend/internal/audit/scrub.go`; the invariant "An erased member's identity does not stay in the audit log" in [invariants.md](../developer/reference/invariants.md) and the design in [member-lifecycle.md](../developer/security/member-lifecycle.md#the-audit-log-keeps-no-identity). Not covered, and filed separately: `actor_ip`, and rows that name a recipient on a faktura (`invoice.emailed`, `invoice.reminded`, `invoice.delivery_log_viewed`).

### BRD-12: A board member stays reachable in the member directory

- **Status:** accepted · 2026-10-30 · BRY-545
- **Decision:** a member who holds a board role is always listed in the member directory with name and email, even when they have set "hide me" (`users.hide_in_directory`). Their phone still follows the setting. Board status is the BRD-3 definition: holding any role in `roles.Board` (chair, vice chair, treasurer, secretary, harbour master, deputy, board). A board role alone gets a member listed, without the plain `member` role. A former board member, whose role has been removed, is hidden again at once. `admin` is not a board role, so a site admin who hides stays hidden. Deactivated members and erased members (the `@deleted.local` placeholder address) are never listed, board or not. The response does not say whether a member hid themselves. The profile form tells the member that board members always show with name and email.
- **Why:** members need to be able to contact the board, and the board changes each year, so the club cannot rely on a separate list kept by hand. Hiding is a privacy choice for ordinary members; taking a board role is a public function, and name and email are what it takes to reach the person holding it. The phone is a stronger disclosure and stays the member's choice for now; the owner may extend the exception later.
- **Consequences:** `directoryQuery` in `backend/internal/handlers/members.go`, which reads the board set from `roles.Board` rather than a list of its own. Covered: the member directory. Deliberately not covered: slip occupancy on the harbour map. A hidden board member's last name stays off the map for other members, because who sits in a slip is not a way to contact the board; viewers holding the literal `board`, `harbor_master` or `admin` role still see every occupant (the other board roles get the member view). Not affected: the public contact page, which prints the club's role addresses from site settings and no member data. The portal waiting list shows other members only as "Medlem #N", so it needs no exception. The volunteer participant lists show names to members and never honoured the hide setting. `/members/directory` only requires a session, so any signed-in user, an applicant included, can see a hidden board member's email; that is a deliberate part of contacting the board. Rule in [invariants.md](../developer/reference/invariants.md).
