import { ApiError } from '@/lib/errors'

// A Vipps charge whose settlement is already cleared against the bank deposit
// cannot be unbooked on its own: the server answers 409
// charge_in_cleared_settlement (BRY-555). The refusal names the settlement
// and the deposit that cleared it, so the SPA can offer to undo that clearing
// (ClearedSettlementRefusal) and then repeat what was refused.
export interface ClearedSettlementRefusal {
  settlementNumber: string
  msn: string
  /** The deposit whose unassign undoes the clearing; empty when none carries it. */
  bankRowId: string
  /** The server's Norwegian sentence, for callers that show no localized text. */
  message: string
}

export const CLEARED_SETTLEMENT_CODE = 'charge_in_cleared_settlement'

/** The refusal an error carries, or null for any other error. */
export function asClearedSettlementRefusal(e: unknown): ClearedSettlementRefusal | null {
  if (!(e instanceof ApiError) || e.status !== 409 || e.code !== CLEARED_SETTLEMENT_CODE) return null
  const d = e.details ?? {}
  const text = (k: string) => (typeof d[k] === 'string' ? (d[k] as string) : '')
  return {
    settlementNumber: text('settlement_number'),
    msn: text('msn'),
    bankRowId: text('bank_row_id'),
    message: e.message,
  }
}
