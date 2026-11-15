import { describe, it, expect } from 'vitest'
import { ApiError } from '@/lib/errors'
import { testPushFailure, testPushOutcome } from '@/lib/testPushResult'

// Echo the key and the plural count, so the assertions see what was asked for.
const t = (key: string, _params?: unknown, n?: unknown) => (n === undefined ? key : `${key}#${n}`)

describe('testPushOutcome', () => {
  it('is a success when every device got it, pluralised by total', () => {
    expect(testPushOutcome(t, { sent: 2, removed: 0, failed: 0, total: 2 })).toEqual({
      kind: 'success',
      lines: ['notifications.test.sent#2', 'notifications.test.hint'],
    })
  })

  it('is a warning when a device was removed or failed', () => {
    const m = testPushOutcome(t, { sent: 1, removed: 1, failed: 1, total: 3 })
    expect(m.kind).toBe('warning')
    expect(m.lines).toContain('notifications.test.removed')
    expect(m.lines).toContain('notifications.test.failed')
  })

  it('is an error when nothing was sent, even on a 200', () => {
    const m = testPushOutcome(t, { sent: 0, removed: 0, failed: 1, total: 1 })
    expect(m.kind).toBe('error')
    expect(m.lines).not.toContain('notifications.test.hint')
  })
})

describe('testPushFailure', () => {
  it.each([
    [new ApiError(409, 'x', 'no_push_subscription'), 'notifications.test.noSubscription'],
    [new ApiError(503, 'x', 'push_not_configured'), 'notifications.test.notConfigured'],
    [new ApiError(429, 'x'), 'notifications.test.rateLimited'],
    [new ApiError(500, 'x'), 'notifications.test.error'],
    [new Error('network'), 'notifications.test.error'],
  ])('maps %o', (err, key) => {
    expect(testPushFailure(t, err)).toEqual({ kind: 'error', lines: [key] })
  })
})
