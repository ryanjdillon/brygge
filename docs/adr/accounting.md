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
- **Consequences:** refund credit notes are linked to the faktura through their refund, not through the manual credit link, so the rule that a manual credit equals the faktura's total (BRY-342) is unchanged. A faktura whose refund credit notes add up to its total counts as credited. A charge that the treasurer assigned to an account by hand isn't a sale through Brygge, so its refund books against that account with no credit note.

### ACC-3: A full refund of a faktura waits for the treasurer's choice

- **Status:** superseded by ACC-8 · 2026-10-29 · BRY-446
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

### ACC-8: A full refund credits the faktura automatically

- **Status:** accepted · 2026-10-29 · BRY-446 · supersedes ACC-3
- **Decision:** a full card refund credits the faktura automatically (a credit note per ACC-2), whether it was made in Brygge or in the provider's dashboard. A refund started in Brygge can choose **Krev inn på nytt** instead, if no partial refund was credited before: no credit note, and the faktura reopens as an ordinary unpaid faktura. After an automatic credit, the treasurer collects again by issuing a new faktura.
- **Why:** holding a dashboard refund until the treasurer chooses (ACC-3) leaves the ledger and the faktura list disagreeing for as long as nobody decides, possibly across year-end, and needs a waiting state, reminder suppression and a task. Crediting is the usual reason for a full refund, so making it the automatic outcome is the least work and keeps the books consistent.

### ACC-9: An issued credit note is never voided

- **Status:** accepted · 2026-10-29 · BRY-446
- **Decision:** a credit note made by a refund is an issued sales document and stays. If the refund later fails and the money comes back, or the refund booking is corrected, Brygge issues a new faktura for the amount instead of voiding the credit note.
- **Why:** under ACC-1 a sales document that has been issued is corrected by a new document, not withdrawn.

### ACC-10: Vipps income parked on 3900 is reclassified through the price-item rules

- **Status:** accepted · 2026-10-30 · BRY-557
- **Decision:** the Jan-Jun 2026 Vipps charges that the legacy model parked on 3900 Andre inntekter and that were then posted are reclassified through the club's price-item rules, the same rules automatic Vipps booking uses, by an explicit treasurer action.
- **Why:** automatic booking now classifies new charges, so the parked ones are the only remainder, and the price-item rules already say which amount is which item.
- **Consequences:** `ApplyVippsFallbackReclass`; see [invariants.md](../developer/reference/invariants.md) "A posted charge on 3900 may be reclassified" and [the accounting README](../../backend/internal/accounting/README.md). These follow from how it was built, not from a separate owner decision:
  - the entry is reversed and rebooked (`rebookVippsChargeTx`) rather than edited, so the charge's own entry stays its booking and the payout clearing is untouched; Angre treats the replacement like any charge booking (in a cleared settlement only after the deposit is undone, ACC-11);
  - a charge the rules cannot place unambiguously stays on 3900 with the reason shown;
  - a charge matching a member's open faktura is left to Tildel, because assigning it settles a faktura and a reclass only moves revenue;
  - a charge that may already have paid a faktura (`receivableChargeSettledFaktura`) stays, so a double count is not hidden behind a revenue account;
  - only open periods are touched; reopening a closed year is a separate, deliberate step.

### ACC-11: A charge in a cleared Vipps settlement is not unbooked on its own

- **Status:** proposed pending owner confirmation · 2026-10-30 · BRY-555
- **Decision:** while a Vipps settlement has a posted clearing (its fee and payout booked against the clearing account), a posted charge in it cannot be unassigned or voided on its own. The refusal offers **Angre innbetalingen**, which undoes the settlement's payout clearing (the payout bilag is voided and the deposit returns to Tildel; the fee stays booked); the charge is then corrected and the next sync clears the settlement again. Changing only what a charge books (its revenue account or its faktura), with the same clearing debit, is done atomically and does not undo the clearing. A charge counts as booked for a clearing only when its entry is posted, and the draft backlog posts a settlement's fee and payout only when all its charges are posted.
- **Why:** an unbook after the clearing leaves the fee and payout credits on 2900 with no charge debit against them until someone books the charge again, and nothing in the product says it must be.
- **Alternatives:** (1) allow the unbook and rely on the treasurer to rebook: 2900 is wrong in between and the books can be left that way; (2) undo the clearing automatically inside the unbook: it hides a bank-ledger action behind a charge button and returns a deposit to Tildel without the treasurer deciding; (3) offer only the atomic rebook and refuse every isolated unbook outright: a refunded or misbooked charge could then never be returned to Tildel, because nothing would undo the clearing it sits in; the chosen design adds that undo.
- **Consequences:** correcting a charge in a cleared settlement takes one extra confirmed step, the clearing undo, unless only the account or faktura changes. `rebookVippsChargeTx` is the one place that replaces a posted charge's booking in a cleared settlement.
