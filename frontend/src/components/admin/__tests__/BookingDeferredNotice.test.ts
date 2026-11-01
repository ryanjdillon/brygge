import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import BookingDeferredNotice from '@/components/admin/BookingDeferredNotice.vue'

describe('BookingDeferredNotice', () => {
  it('says the booking waits for the next sync', () => {
    const wrapper = mountWithPlugins(BookingDeferredNotice)
    expect(wrapper.find('[data-testid="booking-deferred"]').text()).toContain('admin.bankImports.bookingDeferred')
  })
})
