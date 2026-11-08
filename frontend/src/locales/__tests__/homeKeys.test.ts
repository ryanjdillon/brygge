import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The landing page is public, so its join card speaks every locale; the
// public-description setting is worded in every locale too (BRY-666).
const LOCALES: Record<string, Record<string, unknown>> = { de, en, fr, it: it_, nb, nl, nn, pl }

const KEYS = [
  'home.tagline',
  'home.joinCardBody',
  'home.joinCardCue',
  'admin.siteSettings.publicDescription',
  'admin.siteSettings.publicDescriptionHint',
  'admin.siteSettings.publicDescriptionInvalid',
]

function get(node: unknown, path: string): unknown {
  return path.split('.').reduce<unknown>((n, k) => (n as Record<string, unknown> | undefined)?.[k], node)
}

describe('landing page locale keys', () => {
  it.each(Object.keys(LOCALES))('%s has every landing page key, each non-empty', (loc) => {
    for (const key of KEYS) {
      const value = get(LOCALES[loc], key)
      expect(typeof value === 'string' && value.length > 0, `${loc}.${key}`).toBe(true)
    }
  })

  it.each(Object.keys(LOCALES))('%s no longer carries the FAQ teaser keys', (loc) => {
    expect(get(LOCALES[loc], 'faq.home')).toBeUndefined()
    expect(get(LOCALES[loc], 'faq.seeAll')).toBeUndefined()
  })

  it.each(Object.keys(LOCALES))('%s words the join cue apart from the heading and the sentence', (loc) => {
    const cue = String(get(LOCALES[loc], 'home.joinCardCue'))
    const title = String(get(LOCALES[loc], 'join.title'))
    const body = String(get(LOCALES[loc], 'home.joinCardBody'))
    expect(cue.toLocaleLowerCase()).not.toBe(title.toLocaleLowerCase())
    expect(body.toLocaleLowerCase().startsWith(cue.toLocaleLowerCase())).toBe(false)
  })

  it.each(['nn', 'nb'])('%s keeps "e-post" on one line in the join card', (loc) => {
    const body = String(get(LOCALES[loc], 'home.joinCardBody'))
    expect(body).toContain('e\u2011post')
    expect(body).not.toContain('e-post')
  })

  it.each(Object.keys(LOCALES))('%s names no payment provider in the join card', (loc) => {
    expect(String(get(LOCALES[loc], 'home.joinCardBody'))).not.toMatch(/vipps|stripe/i)
  })
})
