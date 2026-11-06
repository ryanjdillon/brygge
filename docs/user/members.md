# Members page

> Audience: board members and site admins keeping the member register (**Medlemmer**) current.

This guide covers the signals on the Members page that need someone to act. The register itself (adding, editing, importing members) is described in the app. A member who has cancelled carries an amber **Utmeld** badge with their end date; see [membership.md](membership.md#for-the-board-following-up-a-cancellation).

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
