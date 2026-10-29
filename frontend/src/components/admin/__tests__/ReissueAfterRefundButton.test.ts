import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import ReissueAfterRefundButton from '@/components/admin/ReissueAfterRefundButton.vue'
import { ApiError } from '@/lib/errors'

const { ensureFreshTotp, askConfirm, mutateAsync, order } = vi.hoisted(() => ({
  ensureFreshTotp: vi.fn(),
  askConfirm: vi.fn(),
  mutateAsync: vi.fn(),
  order: [] as string[],
}))

vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => askConfirm }))
vi.mock('@/composables/useReissueAfterRefund', async (orig) => ({
  ...(await orig<typeof import('@/composables/useReissueAfterRefund')>()),
  useReissueAfterRefund: () => ({ mutateAsync }),
}))

const props = { invoiceId: 'inv-1', invoiceNumber: 70, amount: 450 }
const click = async (w: ReturnType<typeof mountWithPlugins>) => {
  await w.find('[data-testid="reissue-after-refund-button"]').trigger('click')
  await flushPromises()
}

describe('ReissueAfterRefundButton', () => {
  beforeEach(() => {
    order.length = 0
    ensureFreshTotp.mockReset().mockImplementation(async () => {
      order.push('step-up')
      return true
    })
    askConfirm.mockReset().mockImplementation(async () => {
      order.push('confirm')
      return true
    })
    mutateAsync.mockReset().mockImplementation(async () => {
      order.push('request')
      return { id: 'new', invoice_number: 71, sent_at: '2026-10-29T10:00:00Z' }
    })
  })

  it('asks for the step-up at click time, then confirms, then issues', async () => {
    const w = mountWithPlugins(ReissueAfterRefundButton, { props })
    expect(ensureFreshTotp).not.toHaveBeenCalled()

    await click(w)

    expect(order).toEqual(['step-up', 'confirm', 'request'])
    expect(mutateAsync).toHaveBeenCalledWith('inv-1')
    expect(askConfirm.mock.calls[0][0]).toMatchObject({
      title: 'admin.refund.reissue.confirmTitle',
      confirmLabel: 'admin.refund.reissue.confirmAction',
    })
    expect(w.emitted('reissued')?.[0][0]).toMatchObject({ invoice_number: 71 })
  })

  it('does nothing when the step-up or the confirm is declined', async () => {
    ensureFreshTotp.mockResolvedValueOnce(false)
    const w = mountWithPlugins(ReissueAfterRefundButton, { props })
    await click(w)
    expect(askConfirm).not.toHaveBeenCalled()

    askConfirm.mockResolvedValueOnce(false)
    await click(w)
    expect(mutateAsync).not.toHaveBeenCalled()
    expect(w.emitted('reissued')).toBeUndefined()
  })

  it.each([
    [new ApiError(409, 'x', 'already_reissued'), 'admin.refund.reissue.errors.already_reissued'],
    [new ApiError(409, 'x', 'not_refunded'), 'admin.refund.reissue.errors.not_refunded'],
    [new ApiError(404, 'x'), 'admin.refund.reissue.errors.not_found'],
    [new ApiError(500, 'x'), 'admin.refund.reissue.errors.generic'],
    [new TypeError('network'), 'admin.refund.reissue.errors.generic'],
  ])('reports a refusal in words', async (err, message) => {
    mutateAsync.mockRejectedValueOnce(err)
    const w = mountWithPlugins(ReissueAfterRefundButton, { props })
    await click(w)
    expect(w.emitted('failed')?.[0]).toEqual([message])
    expect(w.emitted('reissued')).toBeUndefined()
  })

  it('is a 44px target that names the faktura', () => {
    const w = mountWithPlugins(ReissueAfterRefundButton, { props })
    const button = w.find('[data-testid="reissue-after-refund-button"]')
    expect(button.classes()).toContain('min-h-11')
    expect(button.attributes('aria-label')).toBe('admin.refund.reissue.buttonAria')
    expect(button.text()).toBe('admin.refund.reissue.button')
  })
})
