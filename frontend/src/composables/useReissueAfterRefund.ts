import { useMutation } from '@tanstack/vue-query'
import { useApiClient, unwrap, ApiError } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type ReissuedInvoice = components['schemas']['ReissuedInvoice']

/** The refusals the endpoint answers with a code (409), plus the rest. */
export const REISSUE_ERROR_CODES = ['not_refunded', 'already_reissued'] as const
export type ReissueErrorCode = (typeof REISSUE_ERROR_CODES)[number] | 'not_found' | 'generic'

/** What the endpoint's error means for the person, as a locale key suffix. */
export function reissueErrorCode(e: unknown): ReissueErrorCode {
  if (!(e instanceof ApiError)) return 'generic'
  if (e.status === 404) return 'not_found'
  return (REISSUE_ERROR_CODES as readonly string[]).includes(e.code ?? '') ? (e.code as ReissueErrorCode) : 'generic'
}

/**
 * Issues a new faktura for one that card refunds credited in full. The
 * step-up is asked for at click time, before the confirm; the client retries
 * once if it lapses meanwhile.
 */
export function useReissueAfterRefund() {
  const client = useApiClient()
  return useMutation({
    mutationFn: async (invoiceId: string) =>
      unwrap(
        await client.POST('/api/v1/admin/financials/invoices/{invoiceID}/reissue-after-refund', {
          params: { path: { invoiceID: invoiceId } },
        }),
      ),
  })
}
