import { describe, it, expect } from 'vitest'
import { normalizePhone } from '@/lib/phone'
// TestNormalizePhone in backend/internal/handlers reads these vectors too, so
// the form and the API agree on what is a phone number.
import vectors from './phone_vectors.json'

const valid: Record<string, string> = vectors.valid
const invalid: string[] = vectors.invalid

describe('normalizePhone', () => {
  it.each(Object.entries(valid))('%s → %s', (raw, want) => {
    expect(normalizePhone(raw)).toBe(want)
  })

  it.each(invalid)('refuses %s', (raw: string) => {
    expect(normalizePhone(raw)).toBeNull()
  })
})
