import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The join pages are public and reached from an email, so every locale
// carries every string, and the confirm page has a message per error code.
const LOCALES: Record<string, Record<string, unknown>> = { de, en, fr, it: it_, nb, nl, nn, pl }

function leaves(node: unknown, prefix = ''): string[] {
  if (typeof node !== 'object' || node === null) return [prefix]
  return Object.entries(node).flatMap(([k, v]) => leaves(v, prefix ? `${prefix}.${k}` : k))
}

const reference = leaves(en.join).sort()

describe('join locale keys', () => {
  it.each(Object.keys(LOCALES))('%s has every join key, each non-empty', (loc) => {
    const join = LOCALES[loc].join
    expect(leaves(join).sort()).toEqual(reference)
    for (const key of reference) {
      const value = key.split('.').reduce<unknown>((n, k) => (n as Record<string, unknown>)[k], join)
      expect(typeof value === 'string' && value.length > 0, `${loc}.join.${key}`).toBe(true)
    }
  })

  it.each([
    'join_token_invalid',
    'join_token_used',
    'join_token_expired',
    'join_closed',
    'already_member',
    'rejoin_blocked',
    'failed',
  ])('has a confirm message for %s', (code) => {
    expect(reference).toContain(`confirm.problems.${code}.title`)
    expect(reference).toContain(`confirm.problems.${code}.body`)
  })

  it.each(Object.keys(LOCALES))('%s names no payment provider in the join texts', (loc) => {
    const join = LOCALES[loc].join
    const named = reference.filter((key) =>
      /vipps|stripe/i.test(String(key.split('.').reduce<unknown>((n, k) => (n as Record<string, unknown>)[k], join))),
    )
    expect(named).toEqual([])
  })

  it('nn is Nynorsk, not a copy of nb', () => {
    expect(nn.join.confirm.title).not.toBe(nb.join.confirm.title)
    expect(nn.join.complete.payNow).toBe('Betal no')
  })
})
