import { describe, it, expect } from 'vitest'
import {
  isFullRefund,
  newRequestKey,
  offersOutcome,
  parseKr,
  preselectAttempt,
  refundErrorCode,
  validateRefund,
  MAX_REFUND_REASON,
  type OrderRefundAttempt,
} from '@/composables/useOrderRefund'
import { ApiError } from '@/lib/errors'

const attempt = (extra: Partial<OrderRefundAttempt> = {}): OrderRefundAttempt => ({
  id: 'a1',
  provider: 'stripe',
  currency: 'NOK',
  amount: 450,
  refunded: 0,
  refundable: 450,
  duplicate: false,
  disputed: false,
  can_refund: true,
  recollect_allowed: true,
  created_at: '2026-06-15T10:00:00Z',
  ...extra,
})

describe('parseKr', () => {
  it('reads kroner with a comma or a point and at most two decimals', () => {
    expect(parseKr('450')).toBe(450)
    expect(parseKr('450,5')).toBe(450.5)
    expect(parseKr(' 1 234.56 ')).toBe(1234.56)
  })
  it('refuses what is not a positive amount', () => {
    for (const bad of ['', 'abc', '0', '0,00', '-5', '12,345', '1e3', '12,', ',5']) expect(parseKr(bad), bad).toBeNull()
  })
})

describe('validateRefund', () => {
  it('accepts an amount up to what is refundable with a reason', () => {
    expect(validateRefund('450,00', 'Dobbeltbetaling', 450)).toBeNull()
    expect(validateRefund('0,01', ' x ', 450)).toBeNull()
  })
  it('names the first problem', () => {
    expect(validateRefund('', 'x', 450)).toEqual({ key: 'errors.amountInvalid' })
    expect(validateRefund('450,01', 'x', 450)).toEqual({ key: 'errors.amountTooHigh', max: 450 })
    expect(validateRefund('100', '   ', 450)).toEqual({ key: 'errors.reasonRequired' })
    expect(validateRefund('100', 'å'.repeat(MAX_REFUND_REASON + 1), 450)).toEqual({
      key: 'errors.reasonTooLong',
      max: MAX_REFUND_REASON,
    })
    expect(validateRefund('100', 'å'.repeat(MAX_REFUND_REASON), 450)).toBeNull()
  })
  it('compares to the øre, not as floats', () => {
    expect(validateRefund('0,30', 'x', 0.3)).toBeNull()
    expect(validateRefund('19,99', 'x', 19.99)).toBeNull()
  })
})

describe('isFullRefund', () => {
  it('is true only for the whole of what is refundable', () => {
    expect(isFullRefund('450', 450)).toBe(true)
    expect(isFullRefund('450,00', 450)).toBe(true)
    expect(isFullRefund('449,99', 450)).toBe(false)
    expect(isFullRefund('', 450)).toBe(false)
  })
})

describe('preselectAttempt', () => {
  it('prefers a duplicate capture with money left, else the first refundable payment', () => {
    const first = attempt({ id: 'first' })
    const dup = attempt({ id: 'dup', duplicate: true })
    expect(preselectAttempt([first, dup])?.id).toBe('dup')
    expect(preselectAttempt([first, attempt({ id: 'dup', duplicate: true, can_refund: false })])?.id).toBe('first')
    expect(preselectAttempt([attempt({ can_refund: false })])).toBeUndefined()
    expect(preselectAttempt([])).toBeUndefined()
  })
})

describe('offersOutcome', () => {
  it('applies to a full refund of the payment that paid a faktura', () => {
    expect(offersOutcome(true, attempt(), true)).toBe(true)
    expect(offersOutcome(false, attempt(), true)).toBe(false)
    expect(offersOutcome(true, attempt(), false)).toBe(false)
    expect(offersOutcome(true, attempt({ duplicate: true }), true)).toBe(false)
    expect(offersOutcome(true, undefined, true)).toBe(false)
  })
})

describe('refundErrorCode', () => {
  it('maps the server refusals and anything else to generic', () => {
    expect(refundErrorCode(new ApiError(409, 'x', 'disputed'))).toBe('disputed')
    expect(refundErrorCode(new ApiError(409, 'x', 'recollect_after_partial'))).toBe('recollect_after_partial')
    expect(refundErrorCode(new ApiError(502, 'x', 'refund_failed'))).toBe('refund_failed')
    expect(refundErrorCode(new ApiError(404, 'x'))).toBe('not_found')
    expect(refundErrorCode(new ApiError(400, 'x'))).toBe('invalid')
    expect(refundErrorCode(new ApiError(409, 'x', 'something_new'))).toBe('generic')
    expect(refundErrorCode(new ApiError(500, 'x'))).toBe('generic')
    expect(refundErrorCode(new TypeError('network'))).toBe('generic')
  })
})

describe('newRequestKey', () => {
  it('is a UUID, different each time', () => {
    const a = newRequestKey()
    expect(a).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/)
    expect(newRequestKey()).not.toBe(a)
  })
})
