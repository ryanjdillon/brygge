import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import DeliveryStatusBadge from '@/components/admin/DeliveryStatusBadge.vue'

describe('DeliveryStatusBadge', () => {
  it('shows a status that needs attention', () => {
    const wrapper = mountWithPlugins(DeliveryStatusBadge, { props: { status: 'suppressed' } })
    const badge = wrapper.find('[data-testid="delivery-status-badge"]')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('admin.faktura.sent.deliveryLog.status.suppressed')
    expect(badge.classes()).toContain('bg-red-50')
  })

  it.each(['accepted', 'queued', 'submitted', null, undefined] as const)('stays quiet for %s', (status) => {
    const wrapper = mountWithPlugins(DeliveryStatusBadge, { props: { status } })
    expect(wrapper.find('[data-testid="delivery-status-badge"]').exists()).toBe(false)
  })
})
