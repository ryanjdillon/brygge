import { describe, it, expect } from 'vitest'
import { cn } from '@/lib/utils'

describe('cn utility', () => {
  it('merges class strings', () => {
    expect(cn('px-2', 'py-3')).toBe('px-2 py-3')
  })

  it('handles conflicting Tailwind classes (last wins)', () => {
    const result = cn('px-2', 'px-4')
    expect(result).toBe('px-4')
  })

  it('handles conflicting color classes', () => {
    const result = cn('text-red-500', 'text-blue-500')
    expect(result).toBe('text-blue-500')
  })

  it('handles undefined/null/false values', () => {
    const result = cn('px-2', undefined, null, false, 'py-3')
    expect(result).toBe('px-2 py-3')
  })

  it('returns empty string for no arguments', () => {
    expect(cn()).toBe('')
  })

  it('handles empty string arguments', () => {
    expect(cn('', 'px-2', '')).toBe('px-2')
  })

  it('handles conditional classes', () => {
    const isActive = true
    const result = cn('base', isActive && 'active')
    expect(result).toBe('base active')
  })
})
