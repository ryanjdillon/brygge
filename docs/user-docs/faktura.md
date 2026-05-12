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

Fakturas land in **draft** state — generated but not yet sent.

## Reviewing and sending — Admin → Faktura

Open `Admin → Faktura`. Three tabs:

### Drafts

Newly created fakturas. You can:

- **Preview the PDF** — click the row to open the modal, then "Download PDF".
- **Edit** — open the row, change line amounts/descriptions, save.
- **Delete** — drafts only; gone forever, including the assigned invoice number.
- **Send** — emails the PDF to the member, stamps `sent_at`, moves the row to the Sent tab. The send is idempotent — clicking send on an already-sent faktura returns a 409 from the server, so accidental double-clicks don't double-deliver.

You can also tick multiple rows and use the **Send selected** button. The bulk-send loop is sequential and continues past any single failure, so a partial run will leave the failed rows still in Drafts where you can retry them.

### Sent

Fakturas that have been emailed. Two further sub-states:

- **Unpaid** — member hasn't paid yet. Wait, or send a reminder by clicking the row → "Resend".
- **Paid** — `payment_id` is linked; shown with a green badge.

You can **void** a sent faktura from this tab if it turns out to be wrong — voiding moves it to the Voided tab without deleting it, so the audit trail stays intact. The voided line frees up for re-billing.

### Voided

Read-only history of voided fakturas. Cannot be un-voided; create a new one if you need a corrected version.

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

The chip is keyed on `price_items.category = 'harbor_membership'`. If your membership fee is set up under a different category (e.g. `dues`), the chip won't pick it up. Fix by editing the price item's category to `harbor_membership` and the chip will appear on the next reload.

### "Slip rental chip is empty for a member who got a plassleie line"

Same thing — `slip_fee` is the recognised category. Edit the tiered price items to use `slip_fee` and the chip will appear.

## Single faktura — for ad-hoc bills

For one-off bills (a returned cheque, a guest-slip fee, a non-member service), use **Admin → Faktura → New** instead of bulk:

- **Recipient** — pick a member, OR enter an organisation's name + email + org-number for non-member fakturas (used for invoices to companies hosting events at the club).
- **Lines** — pick price items, override amounts if needed, or add free-text lines.
- **Due date** — defaults to 30 days; adjust as needed.

Single fakturas go through the same draft → send → paid flow.

## Reconciliation tips

- **End of year:** run `Admin → Accounting` to view the income summary for the period. Compare against bank statements. Voided fakturas don't appear in the income totals.
- **Recurring members who never pay:** use the member list with `Membership=Sent` filter (sortable column) to surface unpaid invoices. A "red dashed" chip means sent-but-not-paid.
- **Closing the period:** once everything is reconciled, mark the fiscal period as closed in `Admin → Accounting`. After that, no new fakturas can be issued against that period — protects historical balance sheets.

## Reference

- Pricing admin: [`/admin/pricing`](/admin/pricing)
- Faktura admin: [`/admin/accounting/faktura`](/admin/accounting/faktura)
- Member list (with status chips): [`/admin/users`](/admin/users)
- Accounting overview: [`/admin/accounting`](/admin/accounting)
