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

  // Residency follows from the address (BRY-639): the form no longer asks it.
  it('has no residency question and credits Kartverket for the address data', () => {
    expect(reference).not.toContain('form.isLocal')
    expect(reference).not.toContain('form.chooseOne')
    for (const loc of Object.keys(LOCALES)) {
      const form = (LOCALES[loc].join as { form: { address: { attribution: string } } }).form
      expect(form.address.attribution, loc).toMatch(/© Kartverket$/)
    }
  })

  it('nn is Nynorsk, not a copy of nb', () => {
    expect(nn.join.confirm.title).not.toBe(nb.join.confirm.title)
    expect(nn.join.complete.payNow).toBe('Betal no')
  })

  // Every entry point to joining (BRY-656): the join texts and the labels that
  // lead to them never promise joining for later; closed, they point at contact.
  const ENTRY_LABELS = ['nav.join', 'home.ctaJoin', 'join.title']
  function at(locale: Record<string, unknown>, key: string): unknown {
    return key.split('.').reduce<unknown>((n, k) => (n as Record<string, unknown> | undefined)?.[k], locale)
  }

  it.each(Object.keys(LOCALES))('%s never says joining is coming soon', (loc) => {
    const texts = [
      ...reference.map((key) => String(at(LOCALES[loc].join as Record<string, unknown>, key))),
      ...ENTRY_LABELS.map((key) => String(at(LOCALES[loc], key))),
      ...leaves((LOCALES[loc].home as Record<string, unknown>).join ?? {}).map(String),
    ]
    const soon = texts.filter((s) => /kjem snart|kommer snart|coming soon|bientôt|demnächst|presto|binnenkort|wkrótce/i.test(s))
    expect(soon).toEqual([])
  })

  it.each(Object.keys(LOCALES))('%s labels every entry point alike', (loc) => {
    const labels = new Set(ENTRY_LABELS.map((key) => at(LOCALES[loc], key)))
    expect(labels.size, loc).toBe(1)
    expect([...labels][0], loc).toBeTruthy()
  })

  it('says Bli medlem in nb and nn, with a neutral closed message', () => {
    for (const loc of [nb, nn]) {
      for (const key of ENTRY_LABELS) expect(at(loc, key)).toBe('Bli medlem')
    }
    expect(nn.join.closed.body).toBe('Vil du bli medlem? Ta kontakt med styret, så hjelper vi deg.')
  })

  it('drops the obsolete coming-soon and mailbox keys', () => {
    for (const loc of Object.keys(LOCALES)) {
      const l = LOCALES[loc] as { home: Record<string, unknown>; login: Record<string, unknown> }
      expect(l.home.join, loc).toBeUndefined()
      expect(l.login.joinLink, loc).toBeUndefined()
    }
    expect(reference).not.toContain('closed.title')
  })
})
