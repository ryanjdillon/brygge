import { describe, it, expect } from 'vitest'
import { BOARD_ROLES, canViewAdmin, canWrite, canOpenAdminRoute } from '@/lib/adminAccess'

describe('adminAccess (BRY-372)', () => {
  it('lets every board role and admin view the admin area', () => {
    for (const r of [...BOARD_ROLES, 'admin']) {
      expect(canViewAdmin(['member', r])).toBe(true)
    }
    expect(canViewAdmin(['member', 'slip_holder', 'applicant'])).toBe(false)
    expect(canViewAdmin([])).toBe(false)
  })

  it('keeps money writes with treasurer and admin', () => {
    expect(canWrite('money', ['treasurer'])).toBe(true)
    expect(canWrite('money', ['admin'])).toBe(true)
    for (const r of ['board', 'chair', 'vice_chair', 'secretary', 'harbor_master', 'deputy']) {
      expect(canWrite('money', [r])).toBe(false)
    }
  })

  it('mirrors the server write roles for the other areas', () => {
    expect(canWrite('ledger', ['board'])).toBe(true)
    expect(canWrite('ledger', ['chair'])).toBe(false)
    expect(canWrite('members', ['board'])).toBe(true)
    expect(canWrite('members', ['secretary'])).toBe(false)
    expect(canWrite('harbor', ['harbor_master'])).toBe(true)
    expect(canWrite('harbor', ['deputy'])).toBe(false)
  })

  it('opens adminOnly routes to admin alone', () => {
    const meta = { requiresAdmin: true, adminOnly: true }
    expect(canOpenAdminRoute(meta, ['admin'])).toBe(true)
    expect(canOpenAdminRoute(meta, ['board', 'treasurer'])).toBe(false)
    expect(canOpenAdminRoute({ requiresAdmin: true }, ['deputy'])).toBe(true)
    expect(canOpenAdminRoute({ requiresAdmin: true }, ['member'])).toBe(false)
    expect(canOpenAdminRoute({}, [])).toBe(true)
  })
})
