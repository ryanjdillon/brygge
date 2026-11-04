import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { formatDate, parseDate } from '../format'

// Node re-reads process.env.TZ on assignment, so pinning a zone west of UTC
// here reproduces the off-by-one a bare `new Date('YYYY-MM-DD')` causes.
const originalTZ = process.env.TZ

beforeAll(() => {
  process.env.TZ = 'America/New_York'
})

afterAll(() => {
  process.env.TZ = originalTZ
})

describe('parseDate', () => {
  it('reads a bare YYYY-MM-DD as a local calendar date', () => {
    const d = parseDate('2026-03-01')
    expect(d).not.toBeNull()
    expect([d!.getFullYear(), d!.getMonth() + 1, d!.getDate()]).toEqual([2026, 3, 1])
    expect([d!.getHours(), d!.getMinutes()]).toEqual([0, 0])
  })

  it('keeps full timestamps on the native parser', () => {
    const iso = '2026-03-01T00:30:00Z'
    expect(parseDate(iso)!.getTime()).toBe(new Date(iso).getTime())
  })

  it('returns null for empty or unparseable input', () => {
    expect(parseDate('')).toBeNull()
    expect(parseDate(null)).toBeNull()
    expect(parseDate('2026-13-45x')).toBeNull()
  })
})

describe('formatDate', () => {
  it('does not shift a date-only value back a day', () => {
    expect(formatDate('2026-03-01', 'nb-NO')).toBe('1.3.2026')
  })

  it('returns unparseable input unchanged', () => {
    expect(formatDate('not a date')).toBe('not a date')
  })
})
