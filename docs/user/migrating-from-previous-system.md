# Moving from a previous system

> Audience: the treasurer, and whoever operates Brygge for the club. This guide covers the move from the club's previous accounting and invoicing system into Brygge: what to export before the old system goes away, how its fakturas come in, and how the books carry over. Day-to-day invoicing is in [faktura.md](faktura.md).

## How the move works

The move is built around one date, the **cutover**: the first day of the club's first year in Brygge (normally 1 January).

- **Before the cutover, the previous system keeps the books.** Its annual accounts for those years are the books of record. Brygge doesn't re-book them.
- **From the cutover, Brygge keeps the books.** An **opening balance** (inngående balanse) dated on the cutover carries the closing balances over, including what members still owed (1500 Kundefordringer).
- **Which old fakturas were still unpaid comes from the previous system**, from its list of open customer items (kundereskontro, åpne poster) at the cutover. Brygge doesn't work it out from bank matching: the old fakturas have no Brygge KID and many share an amount, so a bank row is no proof of which one it paid.
- **The year before the cutover is a reference year in Brygge.** Its data stays for comparison and for momskompensasjon, but it isn't the club's accounts.

## 1. Export everything before cancelling the old system

Once the old account is gone, its data may be gone too. Don't cancel until every export below is downloaded, opened and checked, and stored in two places (for example the club's document archive and an offline copy). Keep them for the retention period bokføringsloven requires; confirm the current rule with the accountant.

For the year before the cutover, get these **dated on the last day before the cutover**:

| Export | Usually called | What Brygge uses it for |
|---|---|---|
| Open customer items, per faktura: number, customer, amount still owed, due date | Kundereskontro – åpne poster | Decides paid or unpaid for every imported faktura, and makes up the 1500 line of the opening balance |
| Trial balance with every account's closing balance | Saldobalanse | Its balance-sheet accounts (1xxx–2xxx) become the opening balance |
| Open supplier items, if any supplier invoices were unpaid | Leverandørreskontro – åpne poster | Supplier debt (2400) in the opening balance |
| Year-end accruals the accountant posted (prepaid or accrued) | Periodiseringer | Their balance-sheet side in the opening balance |
| The approved annual accounts | Årsregnskap | Cross-checking Brygge's reference year and the opening balance |
| The standard accounting export, one file per year | SAF-T Financial | Complete archive of the old books; can be used to bring the reference year into Brygge |

Also export, for every year the old system holds: the general ledger (hovedbok), every voucher with its attachments, every outgoing faktura as PDF, bank reconciliations, momskompensasjon filings, and the customer register (this holds personal data: store it accordingly and delete it when no longer needed). CSV or Excel plus PDF is best.

Before cancelling, ask the provider:

- when access ends, and whether read-only access is possible afterwards;
- whether the bank's **KID/OCR agreement** (or AvtaleGiro/eFaktura) is registered through them. If so, move it to the club first, or KID payments stop being recognised. Brygge's KID check method (`Admin → Økonomiinnstillinger`) must match the method on that agreement.

If the open-items list is dated before the last day of the year, note every faktura paid between the export and the cutover.

## 2. Import the old fakturas

Go to **Admin → Bank og Vipps** and use **Import invoice journal (Uni24.no)**. Upload the previous system's faktura journal as CSV, with the columns `fakturanr`, `kunde_navn`, `dato`, `forfall`, `status` and `nettosum` (`kunde_nr` optional), and pick the date range to import. The import is built for the Uni24 faktura journal; another system's export needs these columns.

- Each row becomes a faktura on the **Imported** tab of `Admin → Faktura`. It gets a Brygge number, and its line names the old number. It is linked to a member when the customer name matches a member's full name (ignoring upper and lower case); otherwise the name is kept as written.
- A row with status *Kreditert* comes in as voided. Every other row comes in as open, whatever its status says. Paid status is set from the open-items list (next step).
- Importing the same file again skips the fakturas already there.
- Imported fakturas are read-only: they can't be edited, sent, voided or deleted, and they are never booked in Brygge's accounts. See [Imported](faktura.md#imported).

## 3. Mark which imported fakturas are still owed

Every imported faktura on the open-items list is still owed at the cutover; every other one was settled in the previous system. Brygge has no screen or import for this step yet: whoever runs the Brygge server applies the list directly in the database, taking a backup first. Don't expect a button. Bring the list to the operator; don't try to recreate it by assigning old bank payments in Tildel.

## 4. Post the opening balance

The opening balance is one entry dated on the cutover, built from the saldobalanse: the bank accounts, 1500 Kundefordringer, supplier debt, accruals and equity. Check before posting:

- 1500 equals the total of the open-items list. Count every faktura still owed at the cutover, including ones a Brygge credit note later cancels.
- The entry balances, and each account matches the saldobalanse.

Date the entry **1 January** of the first Brygge year, never 31 December: an entry dated in the reference year is refused (the year takes only costs), and the opening balance belongs to the year Brygge keeps.

The entry must be marked as an opening balance (source `opening_balance`). Brygge can't do that yet: every bilag made on the Bilag page or through the API is recorded as a manual entry and doesn't count. For now, whoever runs the Brygge server posts it directly in the database (tracked as BRY-365).

Until the opening balance is posted:

- a payment of an imported faktura can't be booked. Assigning it in **Tildel** is refused with a message saying the opening balance is missing, and the payment stays in Tildel;
- such a faktura has no pay button on the member's payables page, and **Copy pay link** is refused; the member can still pay by bank transfer;
- a Brygge credit note can't be linked to an imported faktura.

Once it is posted, assign the waiting payments again. Only payments dated on or after the cutover can be booked against an imported faktura; earlier ones belong to the previous system's books. Neither the automatic matching (KID, name, and the automatic bank and Vipps booking) nor **Synk bank** ever books or marks paid an imported faktura: assign those payments by hand in Tildel. See [Payments for fakturas from the previous system](faktura.md#payments-for-fakturas-from-the-previous-system).

Afterwards, compare the trial balance at the cutover (`Admin → Accounting → Reports`) with the saldobalanse; they should agree.

## The year before the cutover

The previous system keeps the books of record for this year, so in Brygge it is a **reference year**. Its data stays, for comparison and for momskompensasjon, but Brygge books nothing in it except costs, and the year feeds no queue, count or balance.

### Mark the year

1. Go to `Admin → Regnskap → Bilag` and pick the year in the period bar.
2. Click **Merk som referanseår**. Only a treasurer or admin sees the button, and you are asked for a fresh two-factor code first. Read the dialog and confirm.

The button is unavailable for the year that covers today and for a year that holds an opening balance: the page says which. Do it after the opening balance for the first Brygge year is posted and before the year's leftovers start to get in your way. Nothing is deleted or changed in the year by marking it, and you can undo it while the year is open (**Fjern referansemerking**), for example if you marked the wrong year.

What changes for a reference year:

- **Out of the way.** It is left out of Tildel and its counts, the Vipps queue, automatic matching and booking, the cash-flow chart and the balance of later years. The opening balance carries what the year closed with, so the year's own figures must not be added again.
- **Nothing needs to be cleaned up.** Its unbooked incoming payments, its Vipps drafts and its bank transfers stay as they are and appear nowhere. **Bokfør Vipps-utkast**, **Avstem periode** and **Korriger inntektsfordeling** are not offered for the year. Don't try to post its drafts: that income is in the previous system's annual accounts already, and Brygge refuses to post it.
- **Its reports still open** when you pick the year in `Admin → Regnskap → Rapporter`, with a **Referanseår** banner saying the figures are for comparison.
- **Only costs can be booked in it.** An outgoing bank payment can be booked to a cost account, a cost can be moved between cost accounts, and a refund from a supplier can be booked back against its cost account. Anything else is refused with the message that the year is a reference year: income, receivables (1500), supplier debt, equity, transfers between bank accounts, and anything on the Vipps or card clearing accounts.

### Book the year's costs

Momskompensasjon is calculated from the costs booked in the year, so book the year's outgoing bank payments:

1. Go to `Admin → Bank og Vipps → Tildel` and pick the **Referanseår** chip. It appears whenever a year is marked as a reference year. The list shows only outgoing rows; incoming payments of a reference year are not shown.
2. For each row, **Lag bilag** to the cost account it belongs to, or **Avvis** if it is not a cost (a transfer between the club's own accounts, for example). The **Avviste** button shows the dismissed rows, and **Angre avvisning** puts one back if it was dismissed by mistake. A dismissal as a duplicate, or of a row that a settled row replaced, is final and is not offered for undoing: bringing it back would count the cost twice. The year picker is disabled while the chip is on: the list covers every reference year.

### File momskompensasjon, then close the year

`Admin → Regnskap → Rapporter → Momskompensasjon` reads a reference year as any other year, from the costs booked in it. Check the result against the årsregnskap. Once the year's momskompensasjon is filed, close the year (**Lukk**) so no more costs can be booked in it. Closing works even though the year still holds drafts: they can never be posted, and nothing needs deleting.

### Settlements that were still on their way at year-end

A Vipps settlement whose charges were booked in the reference year, but whose payout reached the bank after the year ended, is carried in the **opening balance**: add a debit on the clearing account (2900 for Vipps) for the settlement's amount, dated 1 January, as the previous system's Vipps receivable at 31 December shows it. The year's own charges and fee stay as entries that never post. What the new-year deposit needs depends on where it stands:

- **The deposit is already booked** (the debit bank, credit clearing entry is posted in the new year): the opening balance's clearing debit is all that is missing. Book nothing by hand and dismiss nothing.
- **The deposit is still unbooked**: it depends on the settlement. If its charges were posted before the year was marked and it has no fee, **Synk Vipps** clears it like any deposit (debit bank, credit the clearing account, dated in the new year), and the opening balance's debit is all that is missing. Otherwise **Synk Vipps** lists it as skipped, with a reason pointing to the opening balance: book it with a bilag on the Bilag page that debits the bank account and credits the Vipps clearing account (2900 by default; the club's own setting counts), dated the day the money arrived, and dismiss the deposit's bank row in Tildel with the reason "Intern overføring", the closest one. Don't also assign it to a faktura or an account in Tildel; Brygge refuses to book a provider payout by hand there.

Compare the clearing account at 1 January with the previous system's Vipps receivable at 31 December; they should agree.

Card payments through Stripe work the same way: a payout that was still on its way at year-end is carried in the opening balance on the Stripe clearing account (1580 by default), and a Stripe deposit dated in a reference year is left waiting by design, with the reason that the date lies in a reference year, never booked there.

### Fakturas that were still open

If the reference year holds booked fakturas that are still unpaid, the dialog for marking the year warns about them. Once the year is marked, they cannot be voided or credited in it: if one has to be cancelled, issue a credit note in the year you keep.
