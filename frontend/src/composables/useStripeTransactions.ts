import type { Ref } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi, ApiError } from '@/composables/useApi'
import type { InvoiceSuggestion } from '@/composables/useBankReconcile'
import { formatNOK } from '@/lib/format'

const BASE = '/api/v1/admin/accounting/stripe'

/** A manual action on a Stripe balance transaction (accounting.StripeAction*). */
export type StripeTxnAction = 'assign_invoice' | 'assign_account' | 'dismiss' | 'undismiss' | 'unassign' | 'rebook'

/** The list's states. */
export type StripeTxnListStatus = 'needs_review' | 'dismissed' | 'booked'

/** One Stripe balance transaction (accounting.StripeTxnItem). */
export interface StripeTxnItem {
  id: string
  balance_transaction_id: string
  reporting_category: string
  stripe_type: string
  /** Kroner, signed as Stripe signs them. */
  amount: number
  fee: number
  net: number
  currency: string
  /** The booking date (yyyy-mm-dd). */
  date: string
  created_at: string
  status: string
  review_reason?: string
  order_id: string | null
  attempt_id: string | null
  /** Stripe's id of the payout that paid the row out. */
  payout_id: string | null
  payout_cleared: boolean
  dismissed_at?: string
  dismissed_reason?: string
  dismissed_note?: string
  /** A booked row's journal entry. */
  journal_entry_id?: string
  journal_entry_number?: number
  journal_entry_description?: string
  journal_entry_status?: string
  /** The fakturas a booked row settles, by number. */
  invoice_numbers?: number[]
  /** What the server accepts for this row now. */
  allowed_actions: StripeTxnAction[]
}

export interface StripeTxnPage {
  items: StripeTxnItem[]
  limit: number
  offset: number
  has_more: boolean
}

/** An account a Stripe row may be booked against by hand. */
export interface StripeAssignableAccount {
  code: string
  name: string
  account_type: string
}

/** The Stripe dismissal reasons (accounting.IsValidStripeDismissReason). */
export const STRIPE_DISMISS_REASONS = [
  'booked_manually',
  'test_transaction',
  'unidentifiable',
  'refund_or_credit',
  'duplicate',
] as const
export type StripeDismissReason = (typeof STRIPE_DISMISS_REASONS)[number]

/** The 409 codes of the manual actions. */
export const STRIPE_TXN_CODES = [
  'not_allowed',
  'amount_mismatch',
  'invoice_settled',
  'invoice_credited',
  'invoice_not_debt',
  'period_closed',
  'already_booked',
  'payout_cleared',
  'invalid_reason',
  'invalid_account',
  'use_bank_unassign',
  'busy',
] as const
export type StripeTxnCode = (typeof STRIPE_TXN_CODES)[number]

/** The action's refusal code, or null for any other error. */
export function stripeTxnErrorCode(e: unknown): StripeTxnCode | null {
  if (!(e instanceof ApiError) || e.status !== 409) return null
  return (STRIPE_TXN_CODES as readonly string[]).includes(e.code ?? '') ? (e.code as StripeTxnCode) : null
}

/** The server's Norwegian reason for a refusal, if it gave one. */
export function stripeTxnRefusalReason(e: unknown): string {
  const r = e instanceof ApiError ? e.details?.reason : undefined
  return typeof r === 'string' ? r : ''
}

/** A Stripe amount in its own currency; most rows are NOK. */
export function formatStripeAmount(amount: number, currency: string): string {
  if (currency === 'nok') return formatNOK(amount)
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: currency.toUpperCase() }).format(amount)
}

/** What the booking pass did with a rebooked row (accounting.StripeTxnRebookResult). */
export interface StripeTxnRebookResult {
  id: string
  balance_transaction_id: string
  outcome: 'booked' | 'needs_review' | 'deferred' | 'failed'
  reason?: string
  error?: string
  status: string
  journal_entry_id?: string
}

export const STRIPE_TXN_PAGE_SIZE = 20

/** One page of the club's Stripe rows in a state, newest first. */
export function useStripeTransactions(status: Ref<StripeTxnListStatus>, offset: Ref<number>, enabled: Ref<boolean>) {
  return useQuery({
    queryKey: ['accounting', 'stripe-transactions', status, offset],
    enabled,
    queryFn: () =>
      useApi().fetchApi<StripeTxnPage>(
        `${BASE}/transactions?status=${status.value}&limit=${STRIPE_TXN_PAGE_SIZE}&offset=${offset.value}`,
      ),
  })
}

/** Open fakturas whose total equals the charge; q narrows by number or member. */
export function useStripeTxnPotentialInvoices(txnId: Ref<string | null>, q: Ref<string>, enabled: Ref<boolean>) {
  return useQuery({
    queryKey: ['accounting', 'stripe-transactions', 'potential-invoices', txnId, q],
    enabled,
    queryFn: async () => {
      const params = new URLSearchParams({ q: q.value })
      return (
        await useApi().fetchApi<{ items: InvoiceSuggestion[] }>(
          `${BASE}/transactions/${txnId.value}/potential-invoices?${params}`,
        )
      ).items
    },
  })
}

/** The accounts a Stripe row may be booked against by hand. */
export function useStripeAssignableAccounts(enabled: Ref<boolean>) {
  return useQuery({
    queryKey: ['accounting', 'stripe-assignable-accounts'],
    enabled,
    queryFn: async () =>
      (await useApi().fetchApi<{ items: StripeAssignableAccount[] }>(`${BASE}/assignable-accounts`)).items,
    staleTime: 60_000,
  })
}

/**
 * The manual actions. Each refreshes the Stripe lists, the payout picker
 * and Tildel, whose payout deposits wait for these rows.
 */
export function useStripeTxnMutations() {
  const qc = useQueryClient()
  const post = (id: string, action: string, body?: unknown) =>
    useApi().fetchApi<unknown>(`${BASE}/transactions/${id}/${action}`, {
      method: 'POST',
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  const onSuccess = () => {
    qc.invalidateQueries({ queryKey: ['accounting', 'stripe-transactions'] })
    qc.invalidateQueries({ queryKey: ['accounting', 'stripe-payouts'] })
    qc.invalidateQueries({ queryKey: ['bank-rows'] })
  }
  return {
    assignInvoice: useMutation({
      mutationFn: ({ id, invoiceId }: { id: string; invoiceId: string }) =>
        post(id, 'assign-invoice', { invoice_id: invoiceId }),
      onSuccess,
    }),
    assignAccount: useMutation({
      mutationFn: ({ id, accountCode, note }: { id: string; accountCode: string; note: string }) =>
        post(id, 'assign-account', { account_code: accountCode, note }),
      onSuccess,
    }),
    dismiss: useMutation({
      mutationFn: ({ id, reason, note }: { id: string; reason: StripeDismissReason; note: string }) =>
        post(id, 'dismiss', { reason, note }),
      onSuccess,
    }),
    undismiss: useMutation({
      mutationFn: ({ id }: { id: string }) => post(id, 'undismiss'),
      onSuccess,
    }),
    unassign: useMutation({
      mutationFn: ({ id }: { id: string }) => post(id, 'unassign'),
      onSuccess: () => {
        onSuccess()
        // The reversal lands in the ledger.
        qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
      },
    }),
    rebook: useMutation({
      mutationFn: ({ id }: { id: string }) =>
        useApi().fetchApi<StripeTxnRebookResult>(`${BASE}/transactions/${id}/rebook`, { method: 'POST' }),
      onSuccess: () => {
        onSuccess()
        qc.invalidateQueries({ queryKey: ['accounting', 'journal'] })
      },
    }),
  }
}
