import { describe, it, expect } from 'vitest'
import { defaultPeriodId, type FiscalPeriod } from '@/composables/useAccounting'
import { bankRowKindParam, emptyBankRowFilter, isReferencePeriodError } from '@/composables/useBankReconcile'
import { ApiError } from '@/lib/errors'

const period = (over: Partial<FiscalPeriod>): FiscalPeriod => ({
  id: 'p',
  club_id: 'c',
  year: 2025,
  start_date: '2025-01-01',
  end_date: '2025-12-31',
  status: 'open',
  closed_by: null,
  closed_at: null,
  created_at: '2025-01-01T00:00:00Z',
  reference_only: false,
  reference_blocked: null,
  open_invoice_bookings: 0,
  ...over,
})

describe('defaultPeriodId', () => {
  it('prefers the first open period that is not a reference year', () => {
    expect(defaultPeriodId([
      period({ id: 'a', year: 2027, reference_only: true }),
      period({ id: 'b', year: 2026, status: 'closed' }),
      period({ id: 'c', year: 2025 }),
    ])).toBe('c')
  })

  it('falls back to a closed non-reference year, then to the first period', () => {
    expect(defaultPeriodId([
      period({ id: 'a', reference_only: true }),
      period({ id: 'b', status: 'closed' }),
    ])).toBe('b')
    expect(defaultPeriodId([period({ id: 'a', reference_only: true })])).toBe('a')
    expect(defaultPeriodId([])).toBe('')
  })
})

describe('reference mode of the Tildel filter', () => {
  it('is a mode of its own, not a group that combines with the others', () => {
    expect(bankRowKindParam({ ...emptyBankRowFilter(), reference: true })).toBe('reference')
    expect(bankRowKindParam({ direction: 'incoming', source: 'bank', flags: ['duplicate'], reference: true })).toBe('reference')
    expect(bankRowKindParam({ ...emptyBankRowFilter(), reference: true, referenceDismissed: true })).toBe('reference,dismissed')
    expect(bankRowKindParam(emptyBankRowFilter())).toBe('')
  })
})

describe('isReferencePeriodError', () => {
  it('matches only the typed 409', () => {
    expect(isReferencePeriodError(new ApiError(409, 'x', 'reference_period'))).toBe(true)
    expect(isReferencePeriodError(new ApiError(409, 'x', 'period_closed'))).toBe(false)
    expect(isReferencePeriodError(new ApiError(400, 'x', 'reference_period'))).toBe(false)
    expect(isReferencePeriodError(new Error('x'))).toBe(false)
  })
})
