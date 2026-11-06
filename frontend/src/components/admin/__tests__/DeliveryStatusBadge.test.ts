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

  it('takes a caller label and title', () => {
    const wrapper = mountWithPlugins(DeliveryStatusBadge, {
      props: { status: 'rejected', label: 'Email rejected', title: 'Open the member' },
    })
    const badge = wrapper.find('[data-testid="delivery-status-badge"]')
    expect(badge.text()).toBe('Email rejected')
    expect(badge.attributes('title')).toBe('Open the member')
  })

  it.each(['accepted', 'queued', 'submitted', null, undefined] as const)('stays quiet for %s', (status) => {
    const wrapper = mountWithPlugins(DeliveryStatusBadge, { props: { status } })
    expect(wrapper.find('[data-testid="delivery-status-badge"]').exists()).toBe(false)
  })
})
