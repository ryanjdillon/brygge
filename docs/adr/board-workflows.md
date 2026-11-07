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

- **Status:** superseded by BRD-17 · 2026-10-29 · BRY-476
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

### BRD-13: Task participant lists honour the hide setting, with the BRD-12 board exception

- **Status:** accepted · 2026-11-04 · BRY-550
- **Decision:** the participant list of a volunteer task (`GET /tasks/{taskID}/participants`) shows other members a participant's name unless that participant set "hide me" (`users.hide_in_directory`); a hidden participant's row stays, with an empty name. The BRD-12 board exception applies: a participant holding a role in `roles.Board` keeps the name. A member always sees their own name. Board and admin viewers (`roles.CanViewAdmin`) see every name, as they do on the admin member pages. Only the club's own tasks are listed; another club's task answers as if it did not exist.
- **Why:** the profile form promises a hidden member that their name will not be shown, and the participant list was the one member-facing list that still showed it. Keeping the row (rather than dropping it, as the directory does) keeps the task's head count true. An empty name rather than a text placeholder leaves the wording to the client's locale. The board's names are in the directory whatever the setting, so naming them here discloses nothing new.
- **Consequences:** `taskParticipantsQuery` and `HandleListTaskParticipants` in `backend/internal/handlers/volunteer.go`. This replaces the BRD-12 consequence that the volunteer participant lists never honoured the hide setting; BRD-12 otherwise stands. Rule in [invariants.md](../developer/reference/invariants.md).

### BRD-14: Harbor share (moloandel) ownership is a member field with an optional paid date

- **Status:** accepted · 2026-11-04 · BRY-77, BRY-139
- **Decision:** whether a member owns a paid-up harbor share is a member field, `users.harbor_share_owner`, not a role; is-owner is always that flag. The date the share was paid is a separate, optional `users.harbor_share_paid_on`, a `DATE` rather than a `TIMESTAMPTZ`. The date is optional because buy-in dates before 2026 are unknown: NULL means the date is not known, and no fake or default date is ever stored to fill it. A date without ownership is refused (CHECK `users_harbor_share_paid_on_requires_owner`), and removing ownership clears the date. The import from the club's member spreadsheet (BRY-139) is additive: it sets ownership and dates where none are recorded, and never overwrites or clears what is there; differences are reported for the board to fix by hand.
- **Why:** ownership is a fact about the member that other features read (slip type, BRY-76; pricing, BRY-376; key eligibility, BRY-152), not a permission, so it does not belong in the role set (BRY-374). The owner chose to keep when the share was paid, but most existing owners bought in before records were kept, so a date-only column would have forced made-up dates that later read as facts. A `DATE` cannot shift a day: handlers have no per-club timezone (only the host's, and UTC on Compose deploys), and nothing that writes the field has a time of day. The frontend shows it as a local calendar date (`parseDate` in `frontend/src/lib/format.ts` builds a bare `YYYY-MM-DD` in local time rather than as UTC midnight), so the display cannot shift a day west of UTC either. An import that overwrote would let a stale spreadsheet undo corrections made in the app.
- **Consequences:** migration 000133; the admin member list, detail, CSV export and PATCH in `backend/internal/handlers/admin_users.go`, with the `harbor_share=owner|not_owner` filter combining with `spot=` so the board can see owners with and without an active slip. `dev_masked.users` passes both columns through, since they are not identity. `slip_assignments.harbor_membership_*`, written only by the seed, is superseded; dropping it is a follow-up. Rule in [invariants.md](../developer/reference/invariants.md).

### BRD-15: Self-service rejoin is open to former members unless the board has set rejoin_blocked

- **Status:** accepted · 2026-11-06 · BRY-476, BRY-629
- **Decision:** a former (deactivated) member rejoins through the self-service join form like anyone else, with no board step. The board can mark a member "Kan ikkje melde seg inn att" (`users.rejoin_blocked`); for such an address, active or deactivated, the join form creates nothing and emails a pointer to the board instead of a confirmation link, and a confirm still pending when the mark is set is refused.
- **Why:** BRD-6 makes joining fully online, and most former members who come back are welcome back, so an approval step for all of them would be friction for nothing. The few the board has decided against (expelled for cause, for example) need a stop that does not depend on someone noticing the application.
- **Consequences:** migration 000137; the join flow in `backend/internal/handlers/join.go` (email kind `join_contact_board`, audit outcome `blocked`, confirm code `rejoin_blocked`); the flag is set through `PATCH /admin/users/{id}` behind the deactivation roles and audited as `user.rejoin_blocked_set`. The answer to the join form stays the same 202, so the mark is not disclosed to whoever submits the address. The board UI checkbox is BRY-634. Rule in [invariants.md](../developer/reference/invariants.md).

### BRD-16: Residency for self-service joiners is derived from a Kartverket-verified address

- **Status:** accepted · 2026-11-07 · BRY-639
- **Decision:** the join form requires a phone number and the home address, and no longer asks "do you live in the municipality?". The address is searched in Kartverket's open address register (Adresse-API, free, no key, CC BY 4.0) and picked from the matches; the server looks the picked address up again, stores the register's street, postal code and city, and sets `is_local` from whether the address's municipality number is one of the club's `municipality_codes`. An address the register cannot confirm (not found, the register down or slow, or typed by hand) is saved unverified, with residency unknown, and the join goes through anyway. A verified address proves the address exists, not that the person lives there.
- **Why:** the self-declared answer was easy to get wrong in both directions, and a typed address had no check against typos. Proving residency (Folkeregisteret, or a login that carries it such as BankID or Vipps Login) costs money or needs a legal basis the club does not have, so it was rejected. The register gives a fuzzy, typo-forgiving search and a municipality for free, which is the most the club can know without those.
- **Consequences:** migration 000140 (`address_verified`, `address_kommunenummer`, `address_verified_at` on `users` and `membership_applications`); `GET /join/address-search` and the re-lookup in `backend/internal/handlers/join_address.go`; `KARTVERKET_ADDRESS_URL` in [configuration.md](../developer/configuration.md). `is_local` stays NOT NULL: an unverified join stores `false` with `address_verified = false`, which reads as "unknown" for the board to settle. The board sees no note about an unverified address yet (follow-up to BRY-634). Residency is only as right as the club's `municipality_codes`: they must hold the current municipality numbers, and a municipal merger or split (new numbers) needs the codes updated. A club with no codes gets verified addresses with residency left unknown (`is_local = false`, a logged warning), never computed as non-local. Rule in [invariants.md](../developer/reference/invariants.md).

### BRD-17: Self-service membership: join online, pay, member; cancel online at year end

- **Status:** accepted · 2026-10-29, 2026-11-06 · BRY-476 (implemented in BRY-627 to BRY-634, BRY-637, BRY-639)
- **Decision:** joining and leaving the club are online, with no board step. This replaces the interim rule in BRD-6 that applications are inbox tasks.
  - **D1 Membership only.** The join form (`/bli-medlem`, also `/join`) takes no boat details; a member joins the waiting list afterwards.
  - **D2 Identity by email confirmation.** The joiner confirms a link sent to their address. Vipps Login was rejected because it has been a paid service since 2024-08. No new login method: returning applicants use the normal magic link.
  - **D3 Full-year price.** Joining costs the club's join price (Innmeldingspris) for the current calendar year, whatever the date.
  - **D4 Member when paid.** Before payment the person is an applicant who can sign in, pay and see their own fakturas and profile, nothing more.
  - **D5 Cancel online, effective at year end.** Membership runs to 31 December with no refund and no billing for the next year; the member is deactivated from 1 January. The confirmation email is the written notice. This is separate from GDPR erasure, which follows retention.
  - **D6 Consents at join.** One required checkbox covers the privacy policy, statutes, harbour rules and terms of sale (each published by the club and recorded at its version), plus an optional, unticked newsletter opt-in. Phone and home address are required (BRD-16).
  - **D7 Board overview, no approval.** The member list has "Søkjarar" and "Ny i år" filters, a "Medlem sidan" column and an "Utmeld" badge. The board is told by email when a join is paid and when a member cancels or undoes a cancellation, at the club's board notification address (a club setting, defaulting to the shared `post@` mailbox, else the secretary or chair; never hard-coded).
  - **D8 Renewal and non-payment stay manual.** The board deactivates non-payers by hand; automating renewal and non-payment is deferred (BRY-626).
  - **D9 Low friction.** Few fields and steps, one consent checkbox, and it works on a phone.
- **Why:** the owner wants one path to membership: follow the steps online, pay, and you are a member; to leave, cancel on the website. The board only needs an overview. The old join page posted to a route that did not exist, so every online application was lost.
- **Consequences:** a review of the spec shaped the implementation:
  - **R1** Membership is granted only on real money, inside the settlement's transaction in a savepoint, so a failed grant never blocks the payment. The grant is idempotent, and an hourly sweep heals a failed one.
  - **R2** An applicant-only session is walled off from member routes, and optional-session routes treat it as anonymous.
  - **R3** Confirming creates an order, not a faktura. A faktura exists only when the joiner chooses bank transfer ("Betal med bankoverføring").
  - **R4** The join price is an explicit club setting (`clubs.join_price_item_id`), not inferred from the price list.
  - **R5** The emailed link opens a page that only reads; confirming is a POST, because link scanners prefetch GETs.
  - **R6** Pending joins live in `membership_applications`, which stores only the token's hash. No user exists before confirmation.
  - **R7** Every address gets the same answer: new, applicant, deactivated, member or blocked.
  - **R8** Deactivation and waiting-list withdrawal are shared services. A daily job ends cancelled memberships, and the dues run skips members whose membership has ended.
  - **R9** Board notices are emails sent through the `deliveries` queue, not in-app notifications.
  - **R10** The join form has a fail-closed per-IP limit, a keyed per-recipient throttle and a club-wide daily mail cap.
  - **R11** Consent versions are resolved on the server; a stale version answers 409.

  Later decisions refine this record: rejoin is blocked only by the board's mark (BRD-15); residency comes from a Kartverket-verified address (BRD-16); an unpaid join ends after 60 days with its faktura credited, never deleted (ACC-14, [accounting.md](accounting.md)). The rules the code keeps are the membership invariants in [invariants.md](../developer/reference/invariants.md#an-applicant-only-session-reaches-only-what-finishes-joining) (from the applicant gate to the year-end end); the lifecycle from applicant to purge is in [member-lifecycle.md](../developer/security/member-lifecycle.md); the board and member guides are [join.md](../user/join.md), [members.md](../user/members.md) and [membership.md](../user/membership.md).
