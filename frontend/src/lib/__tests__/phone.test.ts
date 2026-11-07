import { describe, it, expect } from 'vitest'
import { normalizePhone } from '@/lib/phone'
// The vectors TestNormalizePhone in backend/internal/handlers reads too, so
// the form and the API agree on what is a phone number.
import vectors from '../../../../backend/internal/handlers/testdata/phone_vectors.json'

describe('normalizePhone', () => {
  it.each(Object.entries(vectors.valid))('%s → %s', (raw, want) => {
    expect(normalizePhone(raw)).toBe(want)
  })

  it.each(vectors.invalid)('refuses %s', (raw) => {
    expect(normalizePhone(raw)).toBeNull()
  })
})
