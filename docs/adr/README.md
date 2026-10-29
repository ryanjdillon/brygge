# Decision records

The decisions the owner has made about how Brygge behaves, one file per area of concern. A decision record says **what was chosen and why**. How the code implements it lives in the developer docs and the subsystem READMEs; rules the code must obey live in [invariants.md](../developer/reference/invariants.md).

Read the area's file before proposing a design in that area, and don't re-open a recorded decision without saying which one and why.

| Area | File | Covers |
|------|------|--------|
| Accounting | [accounting.md](accounting.md) | Bookkeeping standard, credit notes, refunds and disputes in the ledger, imported history, exemptions |
| Payments | [payments.md](payments.md) | Online payment providers, Stripe card payments, refund button, provider fees |
| Board workflows | [board-workflows.md](board-workflows.md) | Board task inbox, responsibility allocation, automation of manual steps, notifications |
| Platform | [platform.md](platform.md) | Cross-cutting product rules: multi-club configuration, mobile support |

## Format

Each decision is a numbered heading (`ACC-3`, `PAY-7`, …) with:

- **Status:** `accepted`, or `superseded by <id>`; **date**; **issue** (the Linear `BRY-` issue where it was decided).
- **Decision:** what was chosen, in one or a few sentences.
- **Why:** the reason, including the alternatives that were turned down when that matters.
- **Consequences:** what this commits the code to, and links to where it's implemented.

Records are append-only. When a decision changes, add a new record and mark the old one `superseded by <id>`; don't rewrite history. A decision that applies to one club's data (an operator step) doesn't belong here: Brygge's decisions must hold for every club that deploys it.

## Adding a decision

Add it in the same commit as the code or spec it shapes, or straight away when the owner decides something during planning. Reference the record from the Linear issue.
