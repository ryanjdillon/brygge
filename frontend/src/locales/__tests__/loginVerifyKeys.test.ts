import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The login verify page is reached from an email by members who may not
// have chosen a language yet, so every locale carries every string, and the
// account and warning lines keep their placeholders (BRY-660).
const LOCALES: Record<string, Record<string, unknown>> = { de, en, fr, it: it_, nb, nl, nn, pl }

function leaves(node: unknown, prefix = ''): string[] {
  if (typeof node !== 'object' || node === null) return [prefix]
  return Object.entries(node).flatMap(([k, v]) => leaves(v, prefix ? `${prefix}.${k}` : k))
}

function verifyOf(loc: string): Record<string, unknown> {
  return (LOCALES[loc].login as Record<string, unknown>).verify as Record<string, unknown>
}

function at(node: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((n, k) => (n as Record<string, unknown>)[k], node)
}

const reference = leaves(verifyOf('en')).sort()

describe('login verify locale keys', () => {
  it.each(Object.keys(LOCALES))('%s has every login.verify key, each non-empty', (loc) => {
    const verify = verifyOf(loc)
    expect(leaves(verify).sort()).toEqual(reference)
    for (const key of reference) {
      const value = at(verify, key)
      expect(typeof value === 'string' && value.length > 0, `${loc}.login.verify.${key}`).toBe(true)
    }
  })

  it.each(['rate_limited', 'unavailable', 'failed'])('has a peek message for %s', (kind) => {
    expect(reference).toContain(`peekFailed.${kind}.title`)
    expect(reference).toContain(`peekFailed.${kind}.body`)
  })

  it.each(Object.keys(LOCALES))('%s names both accounts in the other-user warning', (loc) => {
    const warning = String(verifyOf(loc).otherUser)
    expect(warning).toContain('{current}')
    expect(warning).toContain('{account}')
  })

  it.each(Object.keys(LOCALES))('%s labels the address on the join confirm page', (loc) => {
    const label = at(LOCALES[loc], 'join.confirm.accountLabel')
    expect(typeof label === 'string' && label.length > 0).toBe(true)
    const warning = String(at(LOCALES[loc], 'join.confirm.otherUser'))
    expect(warning).toContain('{current}')
    expect(warning).toContain('{account}')
  })
})
