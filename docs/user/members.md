# Members page

> Audience: board members and site admins keeping the member register (**Medlemmer**) current.

This guide covers the signals on the Members page that need someone to act. The register itself (adding, editing, importing members) is described in the app. A member who has cancelled carries an amber **Utmeld** badge with their end date; see [membership.md](membership.md#for-the-board-following-up-a-cancellation).

## Applicants and new members

The status filter next to the search box (it shows **Aktive** by default) has two choices for people who joined on the website:

- **Søkjarar** lists applicants: people who confirmed the join form but have not paid yet. They carry the **Søkjar** role and cannot use the member portal until they pay. A join left unpaid for 60 days ends on its own.
- **Ny i år: fekk medlemskap i år** lists active members who got their membership this calendar year: those who joined and paid on the website, and those the board gave the **Medlem** role (adding them, ticking the role, or a CSV import with the `member` role).

Both show active people only. **Alle** and **Deaktiverte** still show everyone and former members.

The **Medlem sidan** column shows the date a person got their membership: the day they paid their join on the website, or the day the board first gave them the **Medlem** role. A date already recorded is never moved. It is hidden by default: turn it on under **Kolonnar**, and click the heading to sort by it. It is empty for members registered before Brygge recorded the date, and the detail view shows it as well.

## Can't rejoin

A red **Kan ikkje melde seg inn att** badge next to a name means the board has stopped that person from joining again on the website. When they fill in the join form, they get an email asking them to contact the board instead, and nothing is created.

To set or clear it, open the member and tick or untick **Kan ikkje melde seg inn att** (board or admin). The box appears for deactivated members, and for anyone who already has the mark so it can always be cleared. Brygge asks for a fresh two-factor code when you click it, and every change is logged.

The mark does not deactivate anyone: deactivate the member as usual. Other former members can join again freely.

## Email rejected badge

A small red **E-post avvist** badge next to a member's email address means the last email Brygge sent to that address was refused for good: the member's mail server answered that the mailbox does not exist, is disabled, or will not take mail from the club. Every faktura and reminder to that address will fail the same way until the address is fixed.

The badge looks at the **current** address and at the **latest** email with a final answer:

- an email still on its way (sent minutes ago, or being retried) does not hide an earlier rejection;
- a later email that the member's server accepted clears the badge;
- changing the member's address clears it at once, because the rejection belonged to the old address.

Open the member to see when it happened and what the member's server answered, for example *550 5.1.1 no such user*. The same text shows above the email field when you edit the member.

### What to do

1. Get a current address from the member (phone, the harbour, another board member).
2. Edit the member and save the new address. The badge goes away.
3. Resend the fakturas that bounced from the **Sent** tab in Faktura. Their **Leveringslogg** shows which sends were rejected ([faktura.md § Did the faktura reach the member?](faktura.md#did-the-faktura-reach-the-member)).

If the address is right and the member says their mailbox works (for example a full mailbox they have since emptied), wait for the next email: once it is accepted, the badge clears by itself.

### Email blocked

The badge reads **E-post blokkert** when Amazon SES did not send the email at all: the address bounced hard earlier, so it is on the club's suppression list and every later email to it is dropped. Treat it like a rejection: get a current address from the member and save it. If the address really is right and the bounce will not repeat, the treasurer can lift the block from the faktura's **Leveringslogg** ([faktura.md § Blocked sends](faktura.md#blocked-sends-and-remove-from-suppression-list)).

### Marked as spam

The badge reads **Merket som søppelpost** instead when the member reported club email as spam. Do not send more email to that address, and do not ask to have it unblocked: contact the member another way, and agree on an address they want club mail at.

### How quickly it shows

When the club sends through Amazon SES, the badge appears as soon as SES reports the bounce, usually within minutes. When it sends through the club's own mail server, Brygge reads the server's log every few minutes, so allow 5–10 minutes. A send that is still being retried shows on the faktura as **Delayed, will retry**, not as a badge here; a rejection only appears once the member's server refuses for good.
