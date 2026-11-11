import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import DeclinedNotice from '@/components/admin/DeclinedNotice.vue'

describe('DeclinedNotice', () => {
  it('tells how many payments wait because a person undid them', () => {
    const wrapper = mountWithPlugins(DeclinedNotice, { props: { count: 2, testid: 'x-declined' } })
    expect(wrapper.find('[data-testid="x-declined"]').text()).toContain('admin.bankImports.declinedNotice')
  })

  it.each([0, undefined, null])('renders nothing for %s', (count) => {
    const wrapper = mountWithPlugins(DeclinedNotice, { props: { count } })
    expect(wrapper.find('[data-testid="declined-notice"]').exists()).toBe(false)
  })
})
