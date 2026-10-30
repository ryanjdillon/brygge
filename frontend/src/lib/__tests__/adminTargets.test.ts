import { describe, it, expect } from 'vitest'
import { canManageMember, assignableRoles } from '../adminTargets'

describe('canManageMember', () => {
  it('lets an admin manage anyone', () => {
    expect(canManageMember(true, ['admin'])).toBe(true)
    expect(canManageMember(true, ['member'])).toBe(true)
  })

  it('keeps the board away from an admin but not from a member', () => {
    expect(canManageMember(false, ['member', 'admin'])).toBe(false)
    expect(canManageMember(false, ['member', 'board'])).toBe(true)
    expect(canManageMember(false, undefined)).toBe(true)
  })
})

describe('assignableRoles', () => {
  const roles = ['member', 'board', 'admin']

  it('offers admin only to an admin', () => {
    expect(assignableRoles(true, roles)).toEqual(roles)
    expect(assignableRoles(false, roles)).toEqual(['member', 'board'])
  })
})
