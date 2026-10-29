import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import RefundOrderButton from '@/components/admin/RefundOrderButton.vue'

const { ensureFreshTotp, order } = vi.hoisted(() => ({ ensureFreshTotp: vi.fn(), order: [] as string[] }))

vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('lucide-vue-next', () => ({ Undo2: { template: '<span />' } }))
vi.mock('@/components/admin/RefundOrderDialog.vue', () => ({
  default: {
    props: ['open', 'orderId'],
    template: '<div v-if="open" data-testid="refund-dialog-stub" :data-order="orderId" />',
    setup() {
      order.push('dialog built')
      return {}
    },
  },
}))

describe('RefundOrderButton', () => {
  beforeEach(() => {
    order.length = 0
    ensureFreshTotp.mockReset().mockImplementation(async () => {
      order.push('step-up')
      return true
    })
  })

  it('asks for the step-up at click time, before the dialog exists', async () => {
    const w = mountWithPlugins(RefundOrderButton, { props: { orderId: 'order-1' } })
    expect(ensureFreshTotp).not.toHaveBeenCalled()
    expect(w.find('[data-testid="refund-dialog-stub"]').exists()).toBe(false)

    await w.find('[data-testid="refund-order-button"]').trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(order).toEqual(['step-up', 'dialog built'])
    expect(w.find('[data-testid="refund-dialog-stub"]').attributes('data-order')).toBe('order-1')
  })

  it('opens nothing when the step-up is cancelled', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const w = mountWithPlugins(RefundOrderButton, { props: { orderId: 'order-1' } })
    await w.find('[data-testid="refund-order-button"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="refund-dialog-stub"]').exists()).toBe(false)
    expect(order).toEqual([])
  })

  it('has a 44px touch target also as an icon-only button, with its label for screen readers', () => {
    const w = mountWithPlugins(RefundOrderButton, { props: { orderId: 'order-1', compact: true } })
    const button = w.find('[data-testid="refund-order-button"]')
    expect(button.classes()).toEqual(expect.arrayContaining(['min-h-11', 'min-w-11']))
    expect(button.attributes('aria-label')).toBe('admin.refund.buttonAria')
    expect(w.find('span.sr-only').text()).toBe('admin.refund.button')
  })
})
