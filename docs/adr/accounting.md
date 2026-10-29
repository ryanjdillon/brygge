# Accounting decisions

See [README.md](README.md) for the format. Implementation: [`backend/internal/accounting/README.md`](../../backend/internal/accounting/README.md); rules: [invariants.md](../developer/reference/invariants.md).

### ACC-1: Keep the books as if MVA-registered

- **Status:** accepted · 2026-10-29 · BRY-446
- **Decision:** Brygge's bookkeeping follows the rules for an MVA-registered, bookkeeping-obliged organisation, whether or not a given club is registered today. In particular, any change to the amount of a sale (a refund, a price reduction) is documented by a credit note against the original faktura or order, never by a bare ledger entry.
- **Why:** a club may cross the MVA registration threshold later, and other organisations deploying Brygge may already be registered. Bookkeeping that is compliant for the strictest case is still correct for a club that isn't obliged, while the reverse isn't true. The rule for credit notes is Regnskapsstiftelsen's GBS 1 (*Kreditnota – innhold, utstedelse, bokføring og oppbevaring*).
- **Consequences:** automatic workflows generate the documents a compliant club needs (see ACC-2), so compliance costs the treasurer no extra work. Faktura lines don't carry MVA rates or accounts yet; that is separate, filed work, and credit notes copy lines as they are until it lands.

### ACC-2: A refund that reduces a sale issues a credit note automatically

- **Status:** accepted · 2026-10-29 · BRY-446
- **Decision:** when a card refund on a faktura or an order is booked, Brygge issues a credit note for the refunded amount (lines copied from the faktura or order, scaled proportionally for a partial refund), books it (DR revenue / CR 1500), links it to the faktura, and sends it to the payer. The refund itself books DR 1500 / CR the provider clearing account. A partial refund leaves the faktura paid; the refund-generated credit note never counts as credit owed to the member, because the refund already paid it.
- **Why:** of the options considered, booking the refund straight against revenue without a credit note is not compliant (ACC-1); a manual credit note per refund creates work and is easy to forget; sending every partial refund to manual review creates work for no added safety. Automatic generation is compliant and costs nothing per refund.
- **Consequences:** the rule that a linked credit equals the faktura's total (BRY-342) is relaxed only for refund-generated credit notes whose amount equals the booked refund; manual partial credits are still refused. A charge that the treasurer assigned to an account by hand isn't a sale through Brygge, so its refund books against that account with no credit note.

### ACC-3: A full refund of a faktura waits for the treasurer's choice

- **Status:** accepted · 2026-10-29 · BRY-446
- **Decision:** a full refund means either the sale is cancelled (**credit the faktura**) or the money must be collected another way (**collect again**). A refund started in Brygge asks which, with crediting as the default. A full refund made in the provider's dashboard holds the faktura as "refunded – awaiting a choice": no reminders, no pay link, and a task for the treasurer with the same two one-click choices.
- **Why:** only the treasurer knows why the money went back. Reopening the faktura automatically would send reminders to a member who was just refunded; crediting automatically would write off money the club may still be owed.
- **Consequences:** "collect again" reopens the receivable and detaches the faktura from the refunded order so that a new payment link can be made.

### ACC-4: A disputed card payment is a loss until the club wins

- **Status:** accepted · 2026-10-29 · BRY-446
- **Decision:** when the card provider withdraws disputed money, Brygge books it to a per-club dispute account (default 7830 Tap på fordringer) plus the dispute fee to the fee account, and reverses it if the dispute is won. The faktura stays paid. The treasurer is emailed and gets a task.
- **Why:** simpler than holding the amount on a "disputed" receivable and moving it at the outcome, and a dispute is rare. The member did pay; whether to invoice again after a lost dispute is a board decision, not an automatic one.

### ACC-5: A provider payment is booked only once the provider confirms it

- **Status:** accepted · 2026-10-27 · BRY-440, BRY-445
- **Decision:** the ledger and the faktura's paid state follow confirmed provider events (captured charges, refund balance transactions), booked by the provider booking pass. A click in Brygge (a refund request) or a webhook never books optimistically.
- **Why:** a request can fail or be reversed after the click; the provider's balance transaction is the only record the payout reconciles against.
- **Consequences:** webhooks for refunds and disputes keep only the provider state tables up to date; order and faktura state change in the booking pass.

### ACC-6: Fakturas imported from a previous system are never booked without an opening balance

- **Status:** accepted · 2026-10-26 · BRY-386, BRY-365
- **Decision:** importing fakturas creates records only, with no journal entries. A payment or credit can be booked against an imported faktura only once an opening-balance entry carries it. Which imported fakturas were unpaid at cutover comes from the previous system's open-items list, not from bank matching.
- **Why:** booking DR bank / CR 1500 for an imported faktura with no opening balance drives 1500 negative.
- **Consequences:** the credit link and the bank assignment refuse imported fakturas without a qualifying opening balance. See [migrating-from-previous-system.md](../user/migrating-from-previous-system.md).

### ACC-7: Exemptions are configured per club, with no defaults

- **Status:** accepted · 2026-10-16 · BRY-351
- **Decision:** exemptions (for example a free slip for a board role) are configured as role × price item per club, and the configuration starts empty. An exempt faktura is still issued and booked gross, with an exemption line, and nets to 0 kr.
- **Why:** which roles get a perk differs between clubs (see PLAT-1); booking gross keeps the cost of the perk visible in the accounts.
