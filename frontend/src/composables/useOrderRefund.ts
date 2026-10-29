import type { Ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap, ApiError } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type OrderRefunds = components['schemas']['OrderRefundsResponse']
export type OrderRefundAttempt = components['schemas']['OrderRefundAttempt']
export type PaymentRefund = components['schemas']['PaymentRefund']
export type OrderRefundResult = components['schemas']['OrderRefundResponse']
export type RefundOutcome = 'credit' | 'recollect'

/** The longest reason the server accepts. */
export const MAX_REFUND_REASON = 500

/** The refusals the refund endpoint answers with a code (409, 502, 404). */
export const REFUND_ERROR_CODES = [
  'not_supported',
  'not_captured',
  'disputed',
  'amount_exceeds_refundable',
  'recollect_after_partial',
  'request_key_reused',
  'refund_failed',
] as const
export type RefundErrorCode = (typeof REFUND_ERROR_CODES)[number] | 'invalid' | 'not_found' | 'generic'

/** What the refund endpoint's error means for the person, as a locale key suffix. */
export function refundErrorCode(e: unknown): RefundErrorCode {
  if (!(e instanceof ApiError)) return 'generic'
  if (e.status === 404) return 'not_found'
  // The server judged the input, not the state: a retry as it is cannot succeed.
  if (e.status === 400) return 'invalid'
  return (REFUND_ERROR_CODES as readonly string[]).includes(e.code ?? '') ? (e.code as RefundErrorCode) : 'generic'
}

/**
 * A fresh key for one refund click. The dialog makes it when it opens and
 * sends the same one on every retry, so a repeated request is the same
 * request to the server.
 */
export function newRequestKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  // Not secret, only unique per click: the server treats it as an identifier.
  const hex = (n: number) => Array.from({ length: n }, () => Math.floor(Math.random() * 16).toString(16)).join('')
  return `${hex(8)}-${hex(4)}-4${hex(3)}-a${hex(3)}-${hex(12)}`
}

/** An amount the person typed in kroner ("450", "450,5", "450.50"), or null when it is not one. */
export function parseKr(text: string): number | null {
  const t = text.trim().replace(/\s/g, '').replace(',', '.')
  if (!/^\d+(\.\d{1,2})?$/.test(t)) return null
  const n = Number(t)
  return Number.isFinite(n) && n > 0 ? n : null
}

export type RefundFormError =
  | { key: 'errors.amountInvalid' }
  | { key: 'errors.amountTooHigh'; max: number }
  | { key: 'errors.reasonRequired' }
  | { key: 'errors.reasonTooLong'; max: number }

/** The first thing wrong with the form, or null when it can be sent. */
export function validateRefund(amountText: string, reason: string, refundable: number): RefundFormError | null {
  const amount = parseKr(amountText)
  if (amount === null) return { key: 'errors.amountInvalid' }
  if (Math.round(amount * 100) > Math.round(refundable * 100)) return { key: 'errors.amountTooHigh', max: refundable }
  const trimmed = reason.trim()
  if (!trimmed) return { key: 'errors.reasonRequired' }
  if ([...trimmed].length > MAX_REFUND_REASON) return { key: 'errors.reasonTooLong', max: MAX_REFUND_REASON }
  return null
}

/** Whether the amount is the whole of what is left, to the øre. */
export function isFullRefund(amountText: string, refundable: number): boolean {
  const amount = parseKr(amountText)
  return amount !== null && Math.round(amount * 100) === Math.round(refundable * 100)
}

/**
 * The payment to offer first: a duplicate capture (the usual reason for a
 * refund) when one has money left, else the first payment that can be
 * refunded.
 */
export function preselectAttempt(attempts: OrderRefundAttempt[]): OrderRefundAttempt | undefined {
  const open = attempts.filter((a) => a.can_refund)
  return open.find((a) => a.duplicate) ?? open[0]
}

/**
 * Whether the credit / collect-again choice applies: a full refund of the
 * payment that paid a faktura. A duplicate capture paid nothing.
 */
export function offersOutcome(hasFakturas: boolean, attempt: OrderRefundAttempt | undefined, full: boolean): boolean {
  return hasFakturas && full && !!attempt && !attempt.duplicate
}

const refundsKey = (orderId: string | null) => ['order-refunds', orderId]

/** An order's payments, refunds and disputes; read while the dialog is open. */
export function useOrderRefunds(orderId: Ref<string | null>, enabled: Ref<boolean>) {
  const client = useApiClient()
  return useQuery({
    queryKey: ['order-refunds', orderId],
    enabled,
    // The refund list looks up refunds Stripe has not confirmed, so each open reads fresh.
    gcTime: 0,
    queryFn: async () =>
      unwrap(
        await client.GET('/api/v1/admin/orders/{orderID}/refunds', {
          params: { path: { orderID: orderId.value as string } },
        }),
      ),
  })
}

export interface RefundOrderInput {
  orderId: string
  attemptId: string
  /** Kr; omitted refunds everything refundable. */
  amount?: number
  reason: string
  outcome?: RefundOutcome
  requestKey: string
}

/** Starts the refund. The step-up is asked for before the dialog opens; the client retries once if it lapses. */
export function useRefundOrder() {
  const client = useApiClient()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (input: RefundOrderInput) =>
      unwrap(
        await client.POST('/api/v1/admin/orders/{orderID}/refund', {
          params: { path: { orderID: input.orderId } },
          body: {
            attempt_id: input.attemptId,
            amount: input.amount,
            reason: input.reason.trim(),
            outcome: input.outcome,
            request_key: input.requestKey,
          },
        }),
      ),
    onSettled: (_data, _err, input) => queryClient.invalidateQueries({ queryKey: refundsKey(input.orderId) }),
  })
}
