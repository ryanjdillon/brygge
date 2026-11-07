# `membership/` — granting membership when a join payment settles

Settling a self-service join payment (BRY-630, spec BRY-476) makes the applicant a member. This package is the grant and its integrity check, called from inside the transactions that settle money, so it imports neither `handlers` nor `accounting`.

| File | What |
|---|---|
| `grant.go` | `GrantForOrderInSavepoint` (a captured join order, and every join faktura the order pays; called from `handlers.orderSettlement.settleCapture`) and `GrantForInvoiceInSavepoint` (a join faktura real money paid; called from `accounting.linkInvoicePaymentIn`) run `GrantForOrderTx` / `GrantForInvoiceTx` in a nested savepoint: a failed grant is rolled back alone and logged, never failing the settlement. Also the integrity check (`joinFacts.check`, `joinLineSQL`), the withheld-grant board notice (`queueWithheldTx`) and `grantJoinMembershipTx`, which swaps `applicant` for `member`, sets `member_since`, and, only when it added the role, writes `membership.granted` and queues the welcome and board emails on the `deliveries` queue. `SetLogger` is called once at startup. |
| `refund.go` | `QueueJoinRefundNoticesTx`: one `membership_join_refunded` board email and `membership.join_refunded` audit row per refunded or credited join source. Never revokes. |
| `expiry.go` | `QueueJoinExpiredReviewTx`: the board's review notice (a `membership_grant_withheld` delivery with ref `join_expired:<source>`) and `membership.join_expired_review` audit row for a join that ran out after 60 days with part of the money in (BRY-631). The expiry itself is `handlers.InvoiceHandler.ExpireUnpaidJoins`. |
| `sweep.go` | `Sweep`, the hourly safety net run from `cmd/api/main.go` (grants a paid, ungranted applicant, which heals a failed grant; reports and notifies withheld ones once; queues missed refund notices), and `QueueJoinRefundNotices` for callers that have committed. |

The rule and its failure modes: [invariants.md](../../../docs/developer/reference/invariants.md#membership-is-granted-only-on-real-payment-of-an-intact-join-idempotently). Where the grant sits in the member's life, from join to purge: [member-lifecycle.md](../../../docs/developer/security/member-lifecycle.md); the decision: BRD-17 in [board-workflows.md](../../../docs/adr/board-workflows.md). The emails are sent by `handlers.MembershipNoticeSender`; the board's address is resolved as [configuration.md](../../../docs/developer/configuration.md#board-notification-address) describes.

## Common misses

- **Never `finance.SettledForMemberSQL`.** It is true for a voided or credited faktura; a grant needs real money (`payment_id`, `online_paid_at`, or a captured order) on a faktura that is neither.
- **The emails are queued only by the call that added the role.** A second settlement path must go through `grantJoinMembershipTx`, not write `user_roles` itself, or a redelivery mails twice.
- **A new join source needs a join line.** BRY-631's bank faktura must carry an `invoice_lines` row whose `price_item_id` is the club's `join_price_item_id` (or a `membership` item), with `line_total` = quantity × unit price and an invoice total at least that line, or the grant is withheld.
- **The source is judged against itself, not today's price.** Never compare with `price_items.amount`: a price raised after the applicant confirmed must not withhold what they paid.
- **Never let a grant error fail settlement.** Call the `…InSavepoint` forms from a settlement; the `…Tx` forms return their error and are for callers that already isolate it.
