# Faktura — Brygge invoicing guide

> Audience: club treasurers and admins. This guide walks through the full life-cycle of a faktura in Brygge, from setting up the price catalogue once a year to sending invoices in bulk and reconciling payments.

The faktura system lives at **Admin → Faktura** (`/admin/accounting/faktura`). Pricing setup is at **Admin → Pricing** (`/admin/pricing`).

## Before you send anything

A faktura run depends on three pieces of data being in good shape. Verify them once a year, ideally before the spring billing cycle.

### 1. Fiscal period

Fakturas are bound to a fiscal period (typically one calendar year). Check at **Admin → Accounting** that the current year's period exists and is **open**. If you're rolling into a new year, create the new period there before generating any fakturas — the system refuses to write to a closed period.

### 2. Price items (Admin → Pricing)

Each line that can appear on a faktura is a **price item** with these fields:

| Field | What it does |
|---|---|
| **Category** | Groups items. `harbor_membership` (membership fee) and `slip_fee` (plassleie) are the two recognised by the member-list status chips. Other categories are free-form. |
| **Pricing kind** | `flat` (single amount, same for everyone) or `tiered` (amount varies by boat beam or length). |
| **Tier dimension** | Only for tiered items: `beam` or `length`. |
| **Tier min / max** | The range of beam/length (in metres) this row covers, inclusive of `min`, exclusive of `max`. Tiers must tile without overlap and **must cover the full range your boats span**, including small boats (e.g. a `0 – 2 m` tier is needed if any member has a sub-2m beam — otherwise their line is silently dropped at faktura time). |
| **Amount** | NOK per year for yearly items. |
| **Show in batch** | Must be on for the item to appear in the bulk-faktura modal. |
| **Audience** | `all` (default), `member`, or `non_member`. Lets you show the same service with two different prices on the public pricing page. |

**Retiring a price item.** You can only delete a price item that nothing has used yet. If it already appears on a faktura, an order or a QR poster, Brygge offers to **archive** it instead, meaning it's set to inactive. An archived item stays on old fakturas and in the accounts, but it no longer appears when you create fakturas, on the price list or in QR checkout, and its QR posters stop taking payments. Archived items are listed under **Arkiverte priser** below the active ones, which is collapsed by default. To bring one back, expand that section, edit the item and tick **Active**.

#### Tiered example — slip rental

For a plassleie that depends on slip width, create one row per beam tier, all in the same category (`slip_fee`):

```
Category    Kind     Dim    Min   Max   Amount    Description
slip_fee    tiered   beam   0     2.0   1200      Plassleie 0–2 m
slip_fee    tiered   beam   2.0   2.5   1500      Plassleie 2–2.5 m
slip_fee    tiered   beam   2.5   3.0   1800      Plassleie 2.5–3 m
slip_fee    tiered   beam   3.0   4.0   2200      Plassleie 3–4 m
```

The bulk-faktura flow picks one tier per member based on the beam of the boat assigned to their primary slip. If a member's boat falls outside every tier (e.g. you forgot the 0–2 m row), the line is dropped and you'll see a yellow warning banner in the result screen — see "Bulk faktura" below.

### 3. Members and slip assignments

Bulk faktura uses the member list as its input. Filters on that page (`Admin → Members`) let you narrow to who you actually want to bill:

- **Dock** — restrict to one section of the harbour.
- **Spot** — Permanent / Seasonal / None.
- **Notes** — `with` (review backlog) / `without` (default-clean members) / `any`.

Toggle the new **Membership** and **Slip rental** columns (Columns menu, top right) to see at a glance who's already been billed this period and who's still missing.

## Generating fakturas — bulk

Most clubs send one round of fakturas per year covering membership + plassleie. The bulk flow is:

### Step 1. Filter the member list

Open `Admin → Members`. Apply filters so the rows in the table are exactly who you want to bill. Common slices:

- **Just permanent slip holders, no notes** → Spot=Permanent, Notes=without
- **A specific dock** → Dock=A

### Step 2. Select members

Tick the checkbox on each row you want to bill, or the header checkbox to select the whole page. The selection counter at the top right shows how many are picked.

### Step 3. Open "Generate fakturas"

Click the **Generate fakturas** button (top right of the toolbar). A modal opens with:

- **Fiscal period** — defaults to the current year's open period.
- **Flat items** (left pane) — tick each `flat`-kind price item you want on every selected member's faktura. Common: dues / membership fee.
- **Beam categories** (right pane) — tick each tiered category. The system picks the right tier per member from their boat's beam. Common: slip_fee.
- **Due date** — defaults to ~30 days out.
- **Advanced → Allow re-billing of already-invoiced lines** — leave OFF for normal use. ON only when you intentionally need to re-bill a line that was already invoiced this period (e.g. correcting an amount mid-year).

Click **Generate**.

### Step 4. Read the result panel

For each selected member you'll see one of:

- A row in the green **Created** table with the new faktura number, line count, and total.
- A row in the amber **Skipped** table with a reason (e.g. "no active slip assignment" for a non-slip-holder you accidentally included in a slip_fee run).

A yellow **warning banner** at the top appears if any *individual lines* (not whole invoices) were dropped from created fakturas — usually because a beam doesn't match any tier. Each affected faktura shows the drop reasons under its row. Common causes:

| Drop reason | Fix |
|---|---|
| `Plassleie 0–2m already invoiced (override available)` | The member already has a non-voided faktura for that price item this period. If correcting, use Advanced → Allow re-billing. |
| `beam 1.85m has no matching tier` | Pricing has a gap. Go to `Admin → Pricing` and add the missing tier (e.g. `0–2 m`). |
| `boat has no beam recorded` | Edit the member's boat in `Admin → Members → (member) → Boats` and fill in the beam. |
| `no active slip assignment` | Member has no slip — usually you didn't mean to bill them for slip_fee. Use the Spot filter to exclude. |

If the club has [role exemptions](#role-exemptions) set up, a review step comes first: it lists every line that would be exempted, per member and role. Untick any line that should be billed in full this time (a member with two boats gets one line per boat, so you can untick just one), then click **Generate**. Changing the exemption settings while a run is in progress doesn't affect that run. The result panel says how many lines were exempted. A yellow banner warns if exemptions are set up but no valid expense account is chosen; in that case nobody is exempted.

Fakturas land in **draft** state — generated but not yet sent.

## Role exemptions

Some clubs don't charge board members for certain things — for example slip rent for the treasurer. Set this up once in **Admin → Økonomiinnstillinger → Faktura → Fritak for roller**:

1. Each board role has its own row. Use **Legg til fritak…** to pick a whole price category (e.g. *Plassleie*) or a single price item.
2. Choose the **expense account** the waived amount is booked to (for example 5000, or an account the club creates for board benefits). It is required as soon as any exemption is set.
3. Click **Save** (you will be asked for your authenticator code).

Nothing is exempt until you add it. Changes are logged.

What the member gets: the faktura still lists the item at full price, followed by a line **"Fritak – <rolle>"** for the same amount with a minus sign. If everything on the faktura is exempt, the total is 0 kr: the PDF and email say there is nothing to pay, there is no KID or Vipps link, and the faktura never shows as overdue or gets reminders. A mixed faktura (say exempt slip rent plus normal dues) only asks for the dues. In the member portal a 0 kr faktura shows as **Ingenting å betale**. Credit notes are never given an exemption line.

In the accounts, the item's income is booked in full and the exemption is booked as an expense on the chosen account, so the reports show what the exemption costs the club.

Exemptions follow the member's roles **when the faktura is generated**. If someone leaves the board, remove the role in **Admin → Members**; their next faktura is billed normally. Single fakturas (**New faktura** on a member) apply exemptions too.

## Bank account setup — once per club

Before issuing any fakturas, register the club's bank accounts under `Admin → Economy → Settings → Bank accounts` (migration 000048 introduced this; the legacy single-field `Bank account` field in Site settings is kept as a deprecated fallback for one release).

Each account has:

- **Kontonummer** — Norwegian `xxxx.xx.xxxxx` format.
- **Role** — `drift` (operating), `hoyrente` (savings), or `other`. Semantic-only so far; future Vipps integration uses this to pick the merchant settlement account.
- **GL code** — the NS 4102 chart-of-accounts code (default 1920). Statement uploads bind to this.
- **Faktura default** — exactly one account per club can hold this flag. The faktura PDF prints this account's kontonummer.

Switching the faktura default takes effect on the next faktura issuance. **Already-sent fakturas keep the kontonummer that was printed at issue time** — that's the bokføringsloven §13 requirement for retaining what the recipient saw. If you need to correct an already-sent batch, use the bulk regenerate + purring flow described below.

## Reviewing and sending — Admin → Faktura

Open `Admin → Faktura`. Four tabs: **New**, **Drafts**, **Sent**, **Voided**.

### Drafts

Newly created fakturas. You can:

- **Preview the PDF** — click the row to open the modal, then "Download PDF".
- **Edit** — open the row, change line amounts/descriptions, save.
- **Delete** — drafts only; gone forever, including the assigned invoice number.
- **Send** — emails the PDF to the member, stamps `sent_at`, books the faktura in the accounts (DR 1500 / CR the income account of each line), and moves the row to the Sent tab. The send is idempotent — clicking send on an already-sent faktura returns a 409 from the server, so accidental double-clicks don't double-deliver. If the booking fails (for example because the accounting year is closed) the email still goes out and a message says why; fix the cause and run **Avstem periode** in Bilag to book it.
- **Copy emails** — comma-separated, deduplicated recipient emails land on the clipboard. Use when you want to paste into a personal Gmail/Outlook draft (e.g. heads-up that an automated message is on its way).

You can also tick multiple rows and use the **Send selected** button. The bulk-send loop is sequential and continues past any single failure, so a partial run will leave the failed rows still in Drafts where you can retry them.

### Sent

Fakturas that have been emailed. Filter chips at the top of the toolbar show **paid / waiting / past-due** counts so you can answer "how many fakturas in forfall?" at a glance.

Per-row actions:

- **Preview PDF** — current version.
- **Resend** — re-emails the stored PDF (no re-generation; just delivery).
- **Copy pay link** (link icon) — copies this faktura's durable Vipps pay link (`/betal/{token}`) to the clipboard so you can send it however you like (SMS, chat, read it out) — handy for members who don't cope with email or magic-link login.
- **History** (clock icon) — opens the **Previous PDF versions** popover. Every PDF that was overwritten by a regenerate is archived here with timestamp, who did it, and view/download links. See [PDF archive](#pdf-archive).
- **Void** — moves the row to Voided without deletion (audit trail preserved).

> **Per-member payables link.** For a member with several open fakturas, open them under `Admin → Members`, and use **Copy pay link** in their detail to get their personal `/betal/bruker/…` page. It lists *all* their open fakturas with a Vipps button on each — no login — so a less technical member pays everything from one link. Already-paid and already-received (awaiting settlement) fakturas are hidden so nobody is asked to pay twice.

Bulk actions on the selection:

- **Copy emails** — same as Drafts; useful when emails are routing to spam and you want to send a personal heads-up before/after the automated send.
- **Send purring** — bulk reminder email. Skips already-paid rows automatically; uses the stored PDF as attachment. The reminder copy automatically shifts between "ubetalt" and "forfalt" tone based on the due date. The confirm dialog has a **Vipps payment link** choice: *Leave as set* (default — each invoice follows its own setting), *Include for all selected*, or *Bank transfer only*. The last two are saved on the selected invoices, so the choice also holds when the member opens the link later.
- **Regenerer PDF** — rebuilds `invoices.pdf_data` in place using the **current** club bank-account default. Invoice number, KID, dates, recipient, and lines stay identical. No email is sent. Each regenerate archives the prior PDF under [bokføringsloven §13](https://lovdata.no/lov/2004-11-19-73/§13) so the original is still retrievable.
- **Void** — same as per-row but for the selection.

> **Large payments → bank transfer.** To avoid Vipps fees on big invoices, set a **max amount for Vipps** under `Admin → Economy → Settings`. By default any invoice over that amount hides the Vipps button everywhere — on the faktura, on reminders, on the copy-link, and on the member's payables page — so the member pays by bank transfer (KID). Leave it empty for no limit.
>
> It's a default, not a hard rule: each Sent-tab row has a small **Vipps link** selector — *Auto* (follow the amount limit), *Vipps on* (always offer, even over the limit), or *Vipps off* (never) — so you can make an exception for a specific invoice either way.

**Typical incident recovery flow** (we built this for the operator who issued a batch of fakturas with the wrong bank account):

1. Set the correct account as faktura-default under `Admin → Economy → Settings → Bank accounts`.
2. Sent tab → filter `Past due` and/or `Waiting` → select all.
3. **Regenerer PDF** — stored PDFs now carry the correct kontonummer; the prior PDFs are archived for the legal record.
4. **Send purring** — members receive the corrected PDF as a reminder.
5. **Copy emails** → paste into personal email → send a "heads-up, the reminder isn't spam" note.

### Voided

Read-only history of voided fakturas. Cannot be un-voided; create a new one if you need a corrected version. Voiding a sent faktura reverses its booking in the same step, so it nets to zero in the accounts. A faktura booked in a closed accounting year can't be voided; issue a credit note instead.

**Avstem periode** in Bilag is a repair step, not part of the normal flow: it books any sent faktura that is still missing from the accounts and reverses voided ones that are still booked.

### Credit notes (kreditnotaer)

A credit note is a faktura with a negative amount. It is never a debt: it is never overdue, never reminded, and members see it as **Kreditnota**, not "ubetalt". The **Kreditnotaer** chip in the Sent and Importert lists filters them.

A credit note only settles the faktura it cancels once you link them:

1. Click **Motregn mot faktura** (the scales icon) on the credit note. Brygge asks for a fresh TOTP.
2. Pick the faktura from the list. Only fakturas with the same payer and the same amount are offered, and not ones already credited. If the one you want isn't there, pick **Søk etter en annen faktura …** at the bottom of the list.
3. The result depends on that faktura:
   - **Open** → both count as settled (**Motregnet mot #N**), and the faktura reads **Kreditert av #M**.
   - **Already paid** → the member has the amount owed back (**Tilgode medlem**). Refund it or carry it forward.

#### Credit notes without a member, or for another payer

Some imported credit notes have no member, so no faktura matches the payer and the list is empty. Brygge then opens a search instead: type the faktura number or the payer's name and click **Motregn** on the right faktura. The search shows the club's open and paid fakturas that aren't voided or already credited, newest first, with the amount, date and payer. It shows at most 25; if there are more, it says so, and you narrow the search by number or name. Matching by amount alone isn't enough (many imported fakturas share an amount), so check the number or payer before you pick.

- A faktura with a different amount is marked **Annet beløp** and can't be picked: partial credits aren't supported.
- If the credit note has a member and the faktura is for someone else, it is marked **Annen betaler**. Brygge asks "Kreditnotaen og fakturaen har ulik betaler" before linking them. A credit note without a member needs no such confirmation.

The audit log records that the faktura was picked by hand and whether the payers differed. **Angre motregning** undoes a wrong pick.

Nothing new is booked in the accounts. A booked credit note already cancels its faktura's receivable, and imported history isn't booked at all. Brygge refuses links it can't make safely: a partial credit (settle the rest separately), different payers you haven't confirmed, or one side booked and the other not. **Angre motregning** (the undo icon) reopens the faktura. A linked credit note can't be voided until you undo the link.

## PDF archive

Every regenerate inserts the prior PDF bytes into `invoice_pdf_archive` inside the same transaction as the overwrite, so a failure mid-flight leaves the original PDF in place. Two ways to access an archived version:

1. **From the FakturaList row** — click the history (clock) icon next to the PDF download icon. Opens a popover with every archived version.
2. **From `Admin → Accounting → PDF arkiv`** — search by invoice number. Shows the current PDF link plus every archived version. Use this when you only know the invoice number (e.g. a member dispute).

The audit log records every regenerate with the `prior_pdf_bytes` field so the trail confirms what was preserved.

## What can go wrong — and how to spot it

### "Email server returned 502 during bulk send"

The mail server briefly rejected the message. The faktura **stays as draft** (no `sent_at` stamp) — so no risk of double-sending. Just retry the bulk send for whichever drafts remain.

### "Status chip shows Draft but the member says they received the faktura"

A rare race where the email went out but the post-send DB stamp failed. Reconciliation:

```bash
# On the mail server, find the actual sent-mail log for the time window
ssh root@mail.<domain> -- \
  'journalctl -u stalwart-mail --since "today 10:00" --no-pager | grep -i "to=<"'
```

If you see the member's address in the log but the faktura is still in Drafts, manually stamp it:

```sql
UPDATE invoices SET sent_at = now() WHERE id = '<invoice-id>';
```

Do **not** click "Send" again — that would double-deliver.

### "Membership column is empty even though I sent a faktura"

The chip is keyed on `price_items.category IN ('membership', 'harbor_membership')`. Annual dues should use `membership`; the one-time harbor-equity payment uses `harbor_membership`. If your membership fee is set up under a different category, the chip won't pick it up — edit the price item's category to `membership` and the chip will appear on the next reload.

### "Slip rental chip is empty for a member who got a plassleie line"

Same thing — `slip_fee` is the recognised category. Edit the tiered price items to use `slip_fee` and the chip will appear.

## Single faktura — for ad-hoc bills

For one-off bills (a returned cheque, a guest-slip fee, a non-member service), use **Admin → Faktura → New** instead of bulk:

- **Recipient** — pick a member, OR enter an organisation's name + email + org-number for non-member fakturas (used for invoices to companies hosting events at the club).
- **Lines** — pick price items, override amounts if needed, or add free-text lines.
- **Due date** — defaults to 30 days; adjust as needed.

Single fakturas go through the same draft → send → paid flow.

## Reconciliation tips

- **End of year:** run `Admin → Accounting` to view the income summary for the period. The faktura-status donut and headline cards (Fakturert / Motteke / Utestående / Forfalle) show counts beside amounts. Compare against bank statements. Voided fakturas don't appear in the income totals.
- **Recurring members who never pay:** Sent tab → filter `Past due` chip → bulk **Send purring** to nudge.
- **Closing the period:** once everything is reconciled, mark the fiscal period as closed in `Admin → Accounting`. After that, no new fakturas can be issued against that period — protects historical balance sheets. Closing is refused while the period still has **draft** (kladd) bilag, and the message lists them per source. Post them, or delete an abandoned draft with the bin icon on the Bilag page. Drafts that came from a bank or Vipps row are undone from **Tildel** instead.

  **Vipps drafts from before automatic posting.** Vipps bilag created before posting became automatic are still drafts. On the Bilag page, pick the year and click **Bokfør Vipps-utkast**. A preview shows:
  - how many drafts will be posted as they are, with totals per account;
  - which parked payments will be rebooked, and where to: an account, or the faktura they pay. These are payments sitting on 3900 Andre inntekter, or on 1500 Kundefordringer with no faktura.
  - which drafts are left alone, and why: unbalanced, linked to no row, already booked elsewhere, or a 1500 payment with no faktura that no rule can place.

  Rebooking uses the same rules as automatic Vipps assignment. Turn that on first under `Økonomiinnstillinger → Vipps` (with the tie category). Otherwise the 3900 payments are posted there unclassified, and the 1500 payments with no faktura are left alone. Running it again does nothing new.

  **Payments on 1500 with no faktura.** An early Vipps import booked some payments to 1500 as if they paid a faktura, without saying which one. Before rebooking one, Brygge checks whether it may have paid a faktura after all. It may have if a faktura is marked paid for the same amount (within 1 kr) and no bank or Vipps booking on that faktura explains the payment, and either:
  - the faktura is the same payer's, or its KID is in the Vipps message; or
  - the faktura was marked paid within 7 days of the Vipps payment, whoever it belongs to (people often pay a partner's faktura).

  Then:
  - **It may have paid a faktura:** it is left alone, and the reason names the faktura. It can't be released either. There is no way to sort this out in Brygge yet: the draft stays until the faktura link is resolved (BRY-397). Contact the developer in the meantime.
  - **It paid nothing, and a rule places it:** it is rebooked to the price item's account, or assigned to the payer's single open faktura of that amount.
  - **It paid nothing, but no rule places it** (or automatic assignment is off): it is left alone and gets a **Frigi** button under the preview. This includes an amount that only fits a price as several nights (2,700 kr = 9 × 300 kr): it is never rebooked automatically, so use Frigi and assign it in Tildel. Frigi deletes the draft and puts the payment back in the Vipps queue, where you assign it by hand in Tildel. Frigi asks for a fresh 2FA code, and is refused once the year is locked or closed.

### Bank statement → invoice reconciliation

When you upload a bank statement (`Admin → Accounting → Bank & Vipps`), the KID auto-matcher links each incoming row to the corresponding invoice and back-links `invoices.payment_id` so the faktura-status widgets update. After deploying improvements to the matcher, click **Synk Vipps** (Vipps drafts) or **Synk bank** (KID matches) to retroactively re-run the cascade over already-imported rows.

### Automatic booking of bank payments without a KID

Some members pay by plain transfer, without the KID. Turn on **Bokfør entydige bankinnbetalinger uten KID automatisk** under **Admin → Økonomiinnstillinger → Bankinnbetalinger uten KID** to have Brygge book such a payment by itself after each bank upload and **Synk bank**, but only when the match is clear:

- the payer's name (the "Fra:" name, or the bank's sender) matches exactly one member, or a name you taught (in Tildel with **Hugs betalaren**, see below, or in the Vipps queue);
- that member has exactly one open faktura dated on or before the payment, with nothing paid on it yet;
- the amount is the faktura total to the øre.

It never books a payment that mentions a KID or a faktura number (the normal matching handles those), a payment from the club's own accounts, two payments for the same faktura, a row that seems to belong with another row (**Mogleg par**), an imported faktura from the old system, or a payment in a closed year. Everything else stays in **Tildel**.

The upload and **Synk bank** results list what was booked, and each line has **Angre**, which puts the payment back in Tildel and reopens the faktura. The setting is separate from the Vipps one and is off until you turn it on. Until then, Brygge keeps using its older rule that books a payment whose sender has the same full name as one member with one open faktura of that amount.

### Teaching Brygge who a payer is (Hugs betalaren)

A member's payment often comes from someone else's account: a spouse, or the member's company. Brygge can't match that sender's name to the member by itself. When you assign such a payment to the member's faktura in **Tildel**, the box **Hugs betalaren** above the suggested fakturas (and in the dialog for several rows) remembers the sender for that member. The next payment from the same sender is then matched to the member automatically, by both the bank and the Vipps automatic booking.

- The box is ticked by default. Untick it when the sender paid for the member just this once.
- It is hidden when there is nothing to learn: the sender already is the member (by name or an earlier alias), the payment comes from the club's own account, or the row has no sender name. For several rows it shows only when all the rows have the same sender.
- When the sender's name is already another member's name, or remembered for another member, the box starts unticked with the warning **Namnet høyrer alt til eit anna medlem**. Ticking it anyway moves the name to this member, so that member's own payments would no longer be matched to them. Do that only when you are sure.
- To undo, go to **Admin → Økonomi → Vipps-kø** and delete the name under **Lærte alias**. Teaching and removing a name both ask for a fresh two-factor code.

### Export bank statements up to yesterday

A transfer still under way when you export a statement appears as a bare **Overførsel**, with no sender and no reference text, dated on the export day. The settled row (for a Vipps payout, **Utb. … Vippsnr …**) only appears in a later export. So:

- Export up to **yesterday**. If the statement does run to today, the upload result warns you; upload that day again once it has settled.
- When a later statement brings the settled row, Brygge updates the provisional row with the settled text (and date, if the bank booked it a day or two later), or dismisses it as **Erstattet av oppgjort rad**. The upload result shows how many. The Vipps payout then clears on the next **Synkroniser**.
- If the settled row has the same reference but another amount, or a date more than two days later, nothing is changed and the upload result lists it. Look at it in **Tildel**.
- If an upload stops partway, upload the same file again. Rows already stored are skipped.
- In **Tildel**, such rows carry a **Foreløpig** badge. Don't book them by hand; upload the day again instead.

### Finding the right account in Lag bilag

**Lag bilag** in Tildel books a row or a Vipps payment to an account instead of a faktura. Under each account the picker shows, in grey, the price categories and price items that book to it by default, for example `3300 — Salgs- og tjenesteinntekter` with *Tenester · Slipping liten båt, Slipping stor båt*. The search box searches those too: typing **slipping** finds the account slipping is booked to, whatever the account is called. Case and æ/ø/å don't matter, and every word you type must match.

When the amount fits a price item exactly (or a whole number of nights, days or hours of one), its account comes first under **Foreslåtte** with *Beløpet passar prisartikkel: …*. It is **Sannsynleg** when every fitting price item books to that account and one of them fits as a single price (one night, one season), and **Svak** when they spread over several or the amount fits only as several nights ("9x Bobilplass per døgn"): such an amount may as well be a price no item lists. Members-only prices are suggested only when the payer is recognised as a member. Nothing is picked for you: press **Tildel** on the account you want.

Price items have no account of their own: the account comes from the item's category. A faktura line can still be booked to another account when the faktura is made.

**Renaming an account.** Clubs created before October 2026 have 3300 named *Salgsinntekter*; new clubs get *Salgs- og tjenesteinntekter*. Rename it in `Admin → Accounting → Kontoplan` (`/admin/accounting/accounts`): click the row, change the name or description, and save. This works for the standard (system) accounts too. Their MVA status is locked, and the account number, type and deletion stay as they are. Bookings and reports follow the account number, so renaming changes nothing already booked.

### Rows that belong together (Mogleg par)

Some bank rows only make sense together: a harbour-share buy-back paid out and the overpayment the recipient sent back, or a payment and its refund. Booked one at a time, the return would land as income. Tildel marks such rows **Mogleg par** when they go opposite ways on the same bank account within 60 days and share the other party's account number or name, or reverse each other exactly within 14 days. **Sjå mogleg par** is a weaker hint: the two rows only net to a price item's amount.

- **Lag bilag** on such a row asks **Høyrer denne rada saman med ei anna?** For a **Mogleg par** you must choose before you can assign: **Bokfør saman** with the suggested row, or **Nei, bokfør berre denne rada**.
- **Bokfør saman** makes one bilag for the net (e.g. −63 000 and +1 000 → −62 000), with one bank line and the net on the account you pick. Any account except a bank account or Kundefordringer (1500) can be picked, so a buy-back can go to a balance account rather than revenue.
- Selecting both rows and pressing **Lag bilag** does the same. A selection that leaves a likely partner out asks you to confirm that the rows should be booked separately.
- **Angre** on either row unbooks both.
- The two rows must be on the same bank account and in the same fiscal year, and must not net to zero (that is a transfer between the club's own accounts: use **Synk bank** or dismiss them).

### Vipps reconciliation cascade

For Vipps payouts (the consolidated daily Utb. row in the bank statement), the reconciliation books a bilag per payer using this cascade (DIL-367). Vipps bilags are posted as soon as they are created, so they count in the reports immediately (BRY-339):

1. **Member + matching open invoice** → CR 1500 receivables + back-link `invoices.payment_id`. The faktura-status donut reflects the payment immediately.
2. **Amount → price-article match** → CR the mapped revenue account (3120 plassleie, 3200 gjestehavn, 3210 bobil/camping, 3300 tjenester such as slipping, etc.). Handles guest-slip × N nights, motorhome × N nights, sesongplass, slipping fees — the typical non-member walk-up payments. Only a single price matches: an exact once/year/season price, or one night/day/hour. An amount that fits only as several nights is left to the next step.
3. **Member resolved but no clear classification** → CR 1500 receivables for manual reconciliation.
4. **Fallback** → CR 3900 Andre inntekter. (Previously this was 2900, which implied "we owe the payer money" — wrong for revenue.)

Member resolution uses fuzzy normalization (æøå/diacritics, hyphens, case, whitespace) plus a full-name → first+last → unique-last-name cascade. Most recurring members resolve without operator intervention.

### Automatic Vipps booking (per-charge Tildel)

Turn on **Bokfør entydige Vipps-betalinger automatisk** under **Admin → Økonomiinnstillinger → Vipps**. After each Vipps upload and **Synk Vipps**, Brygge then books a payment by itself, but only when the match is clear:

- **The payer is a member with exactly one open faktura for the same amount.** The faktura is marked paid. The name must match exactly (or through a taught alias or a KID); close-but-not-exact names, and names two members share, are left for you. So is a payment made before the faktura's date, or to a faktura that already has a payment on it.
- **The amount is one price in one price category.** Examples: 1,600 kr is "Slipping <26 fot", or 300 kr is one night at 300 kr. The payment is booked to that category's income account. An amount that fits only as several nights (900 kr = 3 nights at 300 kr) is never booked by itself: it may as well be a price no item lists, so it stays in Tildel with the price item as a suggestion.

If an amount fits several categories (one night at a guest berth or a motorhome spot at the same price, for example), choose which category wins under **Når et beløp passer flere priskategorier**, or leave it for review. Prices for members only are booked automatically only when the payer is a known member.

Everything else stays in **Tildel**. The upload and Synk Vipps results list what was booked, and each line has **Angre**, which puts the payment back in Tildel and reopens the faktura.

### Vipps payouts clear by themselves

You do not need to clear the daily Vipps payout (the **Utb. … Vippsnr …** deposit in the bank statement) by hand. As soon as every payment in that settlement is booked or dismissed, the next **Synkroniser**, **Synk Vipps**, Vipps upload or **Bokfør Vipps-utkast** books the Vipps fee and the deposit, and the result shows how many payouts were cleared. Payouts that stay open are listed with the reason:

- **Venter på belastninger** — some payments in the settlement are still in **Tildel**. Book or dismiss them and sync again.
- **Ingen bankinnbetaling** — the bank statement with that deposit is not uploaded yet, or the deposit text has no Utb./Vippsnr reference.
- **Bare en foreløpig «Overførsel»** — the deposit is in the bank only as a provisional row (see below). Export that day again and upload it.
- **Ikke importert** — the bank deposit names a settlement whose Vipps export is not uploaded yet.

## Reference

- Pricing admin: `/admin/pricing`
- Faktura admin: `/admin/accounting/faktura`
- Member list (with status chips): `/admin/users`
- Accounting overview: `/admin/accounting`
