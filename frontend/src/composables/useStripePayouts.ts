import type { Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi, ApiError } from '@/composables/useApi'

const BASE = '/api/v1/admin/accounting/stripe/payouts'

/** A paid Stripe payout not yet cleared (accounting.StripePayoutSummary). */
export interface UnclearedStripePayout {
  id: string
  payout_id: string
  amount: number
  amount_ore: number
  arrival_date: string
  automatic: boolean
  /** False for a manual payout and one Stripe has not listed yet. */
  members_listed: boolean
  members_count: number
  /** Members neither booked nor dismissed; the server refuses to clear while any are. */
  open_members: number
  members_net_ore: number
  /**
   * For a manual payout, the open balance rows that could have funded it
   * (linked to no payout, available by its arrival); the server refuses to
   * clear while any are. 0 for an automatic payout.
   */
  open_balance: number
}

/** What a manual clearing booked (accounting.StripePayoutClearResult). */
export interface StripePayoutClearResult {
  id: string
  payout_id: string
  bank_row_id: string
  journal_entry_id: string
  /** 0 for a manual payout: Stripe does not list what it paid out. */
  members_count: number
  members_net_ore: number
  /**
   * Stripe clearing account remainder over the members and the entry.
   * Absent for a payout without members, and when it could not be read.
   */
  clearing_balance_ore?: number | null
}

/** The 409 codes of POST /stripe/payouts/{id}/clear. */
export const STRIPE_CLEAR_CODES = [
  'not_stripe_deposit',
  'bank_row_not_eligible',
  'amount_mismatch',
  'payout_not_clearable',
  'members_open',
  'members_not_listed',
  'balance_open',
  'no_period',
  'period_closed',
  'busy',
] as const
export type StripeClearCode = (typeof STRIPE_CLEAR_CODES)[number]

/** The clearing's refusal code, or null for any other error. */
export function stripeClearErrorCode(e: unknown): StripeClearCode | null {
  if (!(e instanceof ApiError) || e.status !== 409) return null
  return (STRIPE_CLEAR_CODES as readonly string[]).includes(e.code ?? '') ? (e.code as StripeClearCode) : null
}

/** The count a members_open or balance_open refusal reported, else 0. */
export function refusalCountOf(e: unknown): number {
  const d = e instanceof ApiError ? e.details : undefined
  const n = d?.open_members ?? d?.open_balance
  return typeof n === 'number' ? n : 0
}

/**
 * The provider when a manual assign was refused because the row is a
 * provider's payout deposit (409 provider_payout), else null. Tildel never
 * offers such an assign, so this only follows a stale page.
 */
export function providerPayoutRefusal(e: unknown): 'vipps' | 'stripe' | null {
  if (!(e instanceof ApiError) || e.status !== 409 || e.code !== 'provider_payout') return null
  return e.details?.provider === 'vipps' ? 'vipps' : 'stripe'
}

/** Uncleared paid NOK payouts, newest first. */
export function useUnclearedStripePayouts(enabled: Ref<boolean>) {
  return useQuery({
    queryKey: ['accounting', 'stripe-payouts', 'uncleared'],
    enabled,
    queryFn: async () =>
      (await useApi().fetchApi<{ items: UnclearedStripePayout[] }>(`${BASE}?cleared=false`)).items,
  })
}

/** Clears a payout against a bank deposit; refreshes Tildel and the payout list. */
export function useClearStripePayout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ payoutId, bankRowId }: { payoutId: string; bankRowId: string }) =>
      useApi().fetchApi<StripePayoutClearResult>(`${BASE}/${payoutId}/clear`, {
        method: 'POST',
        body: JSON.stringify({ bank_row_id: bankRowId }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['bank-rows'] })
      qc.invalidateQueries({ queryKey: ['accounting', 'stripe-payouts'] })
    },
  })
}
