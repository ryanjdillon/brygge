import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankImportsView from '@/views/admin/BankImportsView.vue'

const ensureFreshTotp = vi.fn()
const runStripeSync = vi.fn()
const stripeConfigured = ref(true)

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: () => ensureFreshTotp() }),
}))

vi.mock('@/composables/useCan', () => ({
  useCan: () => ({ can: () => true }),
}))

vi.mock('@/composables/useBankImports', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/composables/useBankImports')>()
  return {
    ...actual,
    runStripeSync: () => runStripeSync(),
    useStripeConfigured: () => ({ data: stripeConfigured }),
    useStripeReview: () => ({ data: ref([]) }),
  }
})

vi.mock('@/components/admin/BankReconcileTab.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/admin/BankRefundTab.vue', () => ({ default: { template: '<div />' } }))

const booked = {
  fetched: 3,
  inserted: 2,
  needs_review: {},
  payouts_seen: 0,
  payouts_linked: 0,
  members_linked: 0,
  booking: { charges_booked: 1, fees_booked: 1, needs_review: {}, deferred: [] },
}

describe('BankImportsView Stripe sync', () => {
  beforeEach(() => {
    stripeConfigured.value = true
    ensureFreshTotp.mockReset().mockResolvedValue(true)
    runStripeSync.mockReset().mockResolvedValue(booked)
  })

  it('steps up at click time, then syncs and shows the booking', async () => {
    const wrapper = mountWithPlugins(BankImportsView)
    await wrapper.find('[data-testid="stripe-sync-btn"]').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(runStripeSync).toHaveBeenCalledTimes(1)
    const panel = wrapper.find('[data-testid="stripe-sync-result"]')
    expect(panel.text()).toContain('admin.bankImports.stripeSyncResult')
    expect(panel.find('[data-testid="stripe-book-summary"]').exists()).toBe(true)
  })

  it('does not sync when the step-up is cancelled', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(BankImportsView)
    await wrapper.find('[data-testid="stripe-sync-btn"]').trigger('click')
    await flushPromises()
    expect(runStripeSync).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="stripe-sync-result"]').exists()).toBe(false)
  })

  it('offers no Stripe sync without Stripe', () => {
    stripeConfigured.value = false
    const wrapper = mountWithPlugins(BankImportsView)
    expect(wrapper.find('[data-testid="stripe-sync-btn"]').exists()).toBe(false)
  })
})
