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
- **Decision:** each club configures which role or person handles each function (accounting, harbour, membership, the contact form, GDPR, …), with reserves. Board status comes from holding any board role. A task goes to the holder of its function, then to the reserve, then to the site admin. Role-specific tasks go only to that role; the site admin is the exception and sees everything. GDPR is a function, assigned to the site admin by default. The contact-form recipient is configurable (secretary, chair or vice-chair).
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
