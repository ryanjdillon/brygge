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

The entry must be marked as an opening balance (source `opening_balance`). Brygge can't do that yet: every bilag made on the Bilag page or through the API is recorded as a manual entry and doesn't count. For now, whoever runs the Brygge server posts it directly in the database (tracked as BRY-365).

Until the opening balance is posted:

- a payment of an imported faktura can't be booked. Assigning it in **Tildel** is refused with a message saying the opening balance is missing, and the payment stays in Tildel;
- such a faktura has no pay button on the member's payables page, and **Copy pay link** is refused; the member can still pay by bank transfer;
- a Brygge credit note can't be linked to an imported faktura.

Once it is posted, assign the waiting payments again. Only payments dated on or after the cutover can be booked against an imported faktura; earlier ones belong to the previous system's books. Neither the automatic matching (KID, name, and the automatic bank and Vipps booking) nor **Synk bank** ever books or marks paid an imported faktura: assign those payments by hand in Tildel. See [Payments for fakturas from the previous system](faktura.md#payments-for-fakturas-from-the-previous-system).

Afterwards, compare the trial balance at the cutover (`Admin → Accounting → Reports`) with the saldobalanse; they should agree.

## The year before the cutover

The previous system keeps the books of record for this year, so in Brygge it is a reference year:

- Don't post its leftover drafts or run **Bokfør Vipps-utkast** for it, and leave its unbooked bank deposits alone. That income is already in the previous system's accounts.
- **Momskompensasjon** for this year can still be filed from Brygge (`Admin → Accounting → Reports → Momskompensasjon`), as long as the year's expense bookings in Brygge are complete. Book any missing expenses there, and check the result against the årsregnskap. Keep the year open until this is done.
- A "reference only" state for such a year is planned: reports will leave it out of the balance carried forward, and new postings will be refused, except for the expense bookings momskompensasjon needs. Until it exists, keep to the rules above by hand.
