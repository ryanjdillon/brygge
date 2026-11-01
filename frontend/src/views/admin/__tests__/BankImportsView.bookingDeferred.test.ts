import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import { ApiError } from '@/lib/errors'
import BankImportsView from '@/views/admin/BankImportsView.vue'
import FileInput from '@/components/ui/form/FileInput.vue'

const ensureFreshTotp = vi.fn()
const runBankSync = vi.fn()
const runVippsResync = vi.fn()
const uploadBankCSV = vi.fn()
const uploadVippsCSV = vi.fn()

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
    runBankSync: () => runBankSync(),
    runVippsResync: () => runVippsResync(),
    uploadBankCSV: (...args: unknown[]) => uploadBankCSV(...args),
    uploadVippsCSV: (...args: unknown[]) => uploadVippsCSV(...args),
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

const sweep = {
  cleared: [{ settlement_number: '2000003', msn: '1113315', bank_row_id: 'b1' }],
  waiting: [],
  skipped: [],
  failed: [],
}

function file() {
  return [new File(['x'], 'x.csv', { type: 'text/csv' })]
}

describe('BankImportsView Vipps booking slot', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset().mockResolvedValue(true)
    runBankSync.mockReset()
    runVippsResync.mockReset()
    uploadBankCSV.mockReset()
    uploadVippsCSV.mockReset()
  })

  it('tells the person a bank sync deferred its Vipps booking, and keeps the rest of the result', async () => {
    runBankSync.mockResolvedValue({ ...syncBase, kid_matched: 2, booking_deferred: true })
    const wrapper = mountWithPlugins(BankImportsView)
    await wrapper.find('[data-testid="bank-sync-btn"]').trigger('click')
    await flushPromises()
    const panel = wrapper.find('[data-testid="bank-sync-result"]')
    expect(panel.find('[data-testid="booking-deferred"]').exists()).toBe(true)
    expect(panel.text()).toContain('admin.bankImports.bookingDeferred')
  })

  it('shows no deferred note when the booking ran', async () => {
    runBankSync.mockResolvedValue({ ...syncBase, vipps_payouts: sweep })
    const wrapper = mountWithPlugins(BankImportsView)
    await wrapper.find('[data-testid="bank-sync-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="booking-deferred"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="vipps-payout-sweep-summary"]').exists()).toBe(true)
  })

  it('shows the Vipps payouts a bank upload cleared', async () => {
    uploadBankCSV.mockResolvedValue({
      id: 'imp1', filename: 'bank.csv', format: 'sparebank-norge-v1', rows_total: 1, imported: 1,
      skipped_dup: 0, matched: 0, transfers: 0, bank_account_code: '1920', auto_matched: false,
      vipps_payouts: sweep,
    })
    const wrapper = mountWithPlugins(BankImportsView)
    wrapper.findAllComponents(FileInput)[0]!.vm.$emit('change', file())
    await flushPromises()
    await wrapper.find('[data-testid="bank-upload-submit"]').trigger('click')
    await flushPromises()
    const panel = wrapper.find('[data-testid="bank-result"]')
    expect(panel.find('[data-testid="vipps-payout-sweep-summary"]').exists()).toBe(true)
    expect(panel.find('[data-testid="booking-deferred"]').exists()).toBe(false)
  })

  it('keeps a bank upload that deferred its Vipps sweep and says so', async () => {
    uploadBankCSV.mockResolvedValue({
      id: 'imp1', filename: 'bank.csv', format: 'sparebank-norge-v1', rows_total: 1, imported: 1,
      skipped_dup: 0, matched: 0, transfers: 0, bank_account_code: '1920', auto_matched: false,
      booking_deferred: true,
    })
    const wrapper = mountWithPlugins(BankImportsView)
    wrapper.findAllComponents(FileInput)[0]!.vm.$emit('change', file())
    await flushPromises()
    await wrapper.find('[data-testid="bank-upload-submit"]').trigger('click')
    await flushPromises()
    const panel = wrapper.find('[data-testid="bank-result"]')
    expect(panel.text()).toContain('bank.csv')
    expect(panel.find('[data-testid="booking-deferred"]').exists()).toBe(true)
  })

  it('keeps a Vipps upload that deferred its booking and says so', async () => {
    uploadVippsCSV.mockResolvedValue({
      id: 'v1', filename: 'vipps.csv', msn: '000001', rows_total: 3, imported: 3, skipped_dup: 0,
      booking_deferred: true,
    })
    const wrapper = mountWithPlugins(BankImportsView)
    wrapper.findAllComponents(FileInput)[1]!.vm.$emit('change', file())
    await flushPromises()
    await wrapper.find('[data-testid="vipps-upload-submit"]').trigger('click')
    await flushPromises()
    const panel = wrapper.find('[data-testid="vipps-result"]')
    expect(panel.text()).toContain('vipps.csv')
    expect(panel.find('[data-testid="booking-deferred"]').exists()).toBe(true)
  })

  it('answers Synk Vipps with the busy message when the booking slot is held', async () => {
    runVippsResync.mockRejectedValue(new ApiError(409, 'Vipps-bokføring kjører allerede', 'vipps_booking_busy'))
    const wrapper = mountWithPlugins(BankImportsView)
    await wrapper.find('[data-testid="vipps-resync-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.bankImports.vippsBookingBusy')
    expect(wrapper.find('[data-testid="vipps-resync-result"]').exists()).toBe(false)
  })

  it('still shows the server text for other Synk Vipps failures', async () => {
    runVippsResync.mockRejectedValue(new ApiError(500, 'internal error'))
    const wrapper = mountWithPlugins(BankImportsView)
    await wrapper.find('[data-testid="vipps-resync-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('internal error')
    expect(wrapper.text()).not.toContain('admin.bankImports.vippsBookingBusy')
  })
})
