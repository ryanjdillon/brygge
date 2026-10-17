import { describe, it, expect, vi, afterEach } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import BankImportProvisionalNotices from '@/components/admin/BankImportProvisionalNotices.vue'
import type { BankImportResult } from '@/composables/useBankImports'

function result(extra: Partial<BankImportResult>): BankImportResult {
  return {
    id: 'imp-1',
    filename: 'synthetic.csv',
    format: 'sparebank-norge-v1',
    rows_total: 3,
    imported: 3,
    skipped_dup: 0,
    matched: 0,
    transfers: 0,
    bank_account_code: '1920',
    auto_matched: false,
    ...extra,
  }
}

describe('BankImportProvisionalNotices', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('warns when the statement ends on the export day', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 17, 12, 0, 0))
    const wrapper = mountWithPlugins(BankImportProvisionalNotices, {
      props: { result: result({ last_row_date: '2026-10-17', provisional_rows: 0 }) },
    })
    expect(wrapper.find('[data-testid="bank-export-day-notice"]').exists()).toBe(true)
  })

  it('warns about an older statement that still holds provisional rows', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 17, 12, 0, 0))
    const wrapper = mountWithPlugins(BankImportProvisionalNotices, {
      props: { result: result({ last_row_date: '2026-09-02', provisional_rows: 1 }) },
    })
    expect(wrapper.find('[data-testid="bank-export-day-notice"]').exists()).toBe(true)
  })

  it('stays quiet for a statement that ended before today with nothing provisional', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 17, 12, 0, 0))
    const wrapper = mountWithPlugins(BankImportProvisionalNotices, {
      props: { result: result({ last_row_date: '2026-10-16', provisional_rows: 0 }) },
    })
    expect(wrapper.find('[data-testid="bank-export-day-notice"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="bank-provisional-settled"]').exists()).toBe(false)
  })

  it('reports settled and ambiguous provisional rows', () => {
    const wrapper = mountWithPlugins(BankImportProvisionalNotices, {
      props: {
        result: result({
          refreshed: 2,
          superseded: 1,
          provisional_ambiguous: [{ row_id: 'r1', date: '2026-08-03', amount: 294.75, candidates: 2 }],
        }),
      },
    })
    expect(wrapper.find('[data-testid="bank-provisional-settled"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="bank-provisional-ambiguous"]').exists()).toBe(true)
  })

  it('lists provisional rows whose settled row did not fit', () => {
    const wrapper = mountWithPlugins(BankImportProvisionalNotices, {
      props: {
        result: result({
          reference_mismatches: [
            { row_id: 'r1', reference: 'SYN1', date: '2026-06-05', amount: 200, new_date: '2026-06-08', new_amount: 200 },
          ],
        }),
      },
    })
    expect(wrapper.get('[data-testid="bank-reference-mismatch"]').text()).toContain('SYN1')
  })
})
