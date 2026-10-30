import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import AccountingReportsView from '@/views/admin/AccountingReportsView.vue'

const { periods } = vi.hoisted(() => ({ periods: { value: [] as Record<string, unknown>[] } }))

vi.mock('@/composables/useAccounting', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useAccounting')>('@/composables/useAccounting')
  return { ...actual, useFiscalPeriods: () => ({ data: ref(periods.value) }) }
})

const period = (over: Record<string, unknown>) => ({
  id: 'p', year: 2025, status: 'open', reference_only: false, reference_blocked: null, open_invoice_bookings: 0, ...over,
})

describe('AccountingReportsView reference years', () => {
  beforeEach(() => {
    periods.value = []
  })

  it('opens on the first open year that is not a reference year, with no banner', async () => {
    periods.value = [period({ id: 'ref', year: 2026, reference_only: true }), period({ id: 'live', year: 2025 })]
    const wrapper = mountWithPlugins(AccountingReportsView)
    await flushPromises()
    expect((wrapper.get('#rpt-period').element as HTMLSelectElement).value).toBe('live')
    expect(wrapper.find('[data-testid="reference-banner"]').exists()).toBe(false)
  })

  it('labels the reference year in the picker and banners its reports', async () => {
    periods.value = [period({ id: 'ref', year: 2025, reference_only: true })]
    const wrapper = mountWithPlugins(AccountingReportsView)
    await flushPromises()
    expect(wrapper.get('#rpt-period').text()).toContain('admin.accounting.periods.referenceLabel')
    const banner = wrapper.get('[data-testid="reference-banner"]')
    expect(banner.text()).toContain('admin.accounting.reports.referenceBanner')
    expect(banner.text()).not.toContain('admin.accounting.reports.referenceMomskomp')

    const momskomp = wrapper.findAll('nav button').find((b) => b.text() === 'admin.accounting.reports.momskomp')!
    await momskomp.trigger('click')
    expect(wrapper.get('[data-testid="reference-banner"]').text()).toContain('admin.accounting.reports.referenceMomskomp')
  })
})
