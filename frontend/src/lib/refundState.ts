// How the lists name what card refunds and disputes did to a payment or a
// faktura (BRY-498). The server sends the refund state and Stripe's own word
// for a dispute; these helpers say which chip each one is.

export type RefundState = 'none' | 'partial' | 'full'

export type RefundChipKind = 'refunded' | 'partial' | 'disputed' | 'dispute_lost' | 'dispute_won'

/** The chip for a refund state, or null when nothing was refunded. */
export function refundChipKind(state: string | null | undefined): 'refunded' | 'partial' | null {
  if (state === 'full') return 'refunded'
  if (state === 'partial') return 'partial'
  return null
}

// Stripe's dispute statuses. An inquiry (warning_*) is a bank asking
// questions before it becomes a chargeback; both wait on the club.
const OPEN_DISPUTE = new Set(['warning_needs_response', 'warning_under_review', 'needs_response', 'under_review'])
// A dispute that waits for evidence from the club: its deadline is the one to watch.
const AWAITING_ANSWER = new Set(['warning_needs_response', 'needs_response'])

/**
 * The chip for a dispute status. A dispute that ended in a refund, was
 * prevented or was an inquiry that closed took no money, so it has none.
 */
export function disputeChipKind(status: string | null | undefined): 'disputed' | 'dispute_lost' | 'dispute_won' | null {
  if (!status) return null
  if (OPEN_DISPUTE.has(status)) return 'disputed'
  if (status === 'lost') return 'dispute_lost'
  if (status === 'won') return 'dispute_won'
  return null
}

/** Whether the club still has to answer the dispute (so its deadline matters). */
export function disputeAwaitsAnswer(status: string | null | undefined): boolean {
  return !!status && AWAITING_ANSWER.has(status)
}
