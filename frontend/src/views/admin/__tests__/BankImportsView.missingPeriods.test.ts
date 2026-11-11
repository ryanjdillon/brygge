import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankImportsView from '@/views/admin/BankImportsView.vue'
import FileInput from '@/components/ui/form/FileInput.vue'

const runBankSync = vi.fn()
const uploadBankCSV = vi.fn()

// Render the params next to the key, so the tests see the years passed.
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => (params ? `${key} ${JSON.stringify(params)}` : key),
    te: () => false,
    n: (value: number) => String(value),
    locale: { value: 'en' },
  }),
  createI18n: () => ({ global: { t: (key: string) => key }, install: vi.fn() }),
}))

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: () => Promise.resolve(true) }),
}))

vi.mock('@/composables/useCan', () => ({
  useCan: () => ({ can: () => true }),
}))

vi.mock('@/composables/useBankImports', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/composables/useBankImports')>()
  return {
    ...actual,
    runBankSync: () => runBankSync(),
    uploadBankCSV: (...args: unknown[]) => uploadBankCSV(...args),
    useStripeConfigured: () => ({ data: { value: false } }),
  }
})

vi.mock('@/components/admin/BankReconcileTab.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/admin/BankRefundTab.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/admin/StripeReviewList.vue', () => ({ default: { template: '<div />' } }))

const syncBase = {
  kid_matched: 0,
  vipps_reconciled: 0,
  vipps_unbalanced: 0,
  transfers_linked: 0,
  closed_periods: [],
}

const importBase = {
  id: 'imp1', filename: 'bank.csv', format: 'sparebank-norge-v1', rows_total: 1, imported: 1,
  skipped_dup: 0, matched: 0, transfers: 0, bank_account_code: '1920', auto_matched: false,
}

describe('BankImportsView missing fiscal years (BRY-578)', () => {
  beforeEach(() => {
    runBankSync.mockReset()
    uploadBankCSV.mockReset()
  })

  it('names the years a sync could not book into', async () => {
    runBankSync.mockResolvedValue({ ...syncBase, missing_periods: ['2027', '2028'] })
    const wrapper = mountWithPlugins(BankImportsView)
    await wrapper.find('[data-testid="bank-sync-btn"]').trigger('click')
    await flushPromises()
    const notice = wrapper.find('[data-testid="bank-sync-missing-periods"]')
    expect(notice.exists()).toBe(true)
    expect(notice.text()).toContain('admin.bankImports.missingPeriodsNotice')
    expect(notice.text()).toContain('"years":"2027, 2028"')
  })

  it('shows no notice when every year exists', async () => {
    runBankSync.mockResolvedValue({ ...syncBase, missing_periods: null })
    const wrapper = mountWithPlugins(BankImportsView)
    await wrapper.find('[data-testid="bank-sync-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="bank-sync-result"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="bank-sync-missing-periods"]').exists()).toBe(false)
  })

  it('names the years a bank upload could not book into', async () => {
    uploadBankCSV.mockResolvedValue({ ...importBase, missing_periods: ['2027'] })
    const wrapper = mountWithPlugins(BankImportsView)
    wrapper.findAllComponents(FileInput)[0]!.vm.$emit('change', [new File(['x'], 'x.csv', { type: 'text/csv' })])
    await flushPromises()
    await wrapper.find('[data-testid="bank-upload-submit"]').trigger('click')
    await flushPromises()
    const panel = wrapper.find('[data-testid="bank-result"]')
    const notice = panel.find('[data-testid="bank-import-missing-periods"]')
    expect(notice.text()).toContain('admin.bankImports.missingPeriodsNotice')
    expect(notice.text()).toContain('"years":"2027"')
  })
})
