import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The harbor payment overview is read on a phone by whoever is on duty, in
// their own language, and covers every provider, so its subtitle names none.
const KEYS = [
  'title',
  'subtitle',
  'refresh',
  'searchPlaceholder',
  'since',
  'until',
  'empty',
  'anonymous',
  'kind.all',
  'kind.guest',
  'kind.motorhome',
  'kind.slipping',
  'kind.faktura',
  'kind.other',
  'filters.link',
  'filters.unassignedHint',
  'method.vipps',
  'method.card',
  'method.apple_pay',
  'method.google_pay',
].map((k) => `recentPayments.${k}`)

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('recent payments locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    // The chips are the club's filter groups (BRY-522): the legacy harbor chip
    // and the merch kind (no category ever matched it) are gone.
    it(`${locale} has no string for the removed harbor and merch kinds`, () => {
      expect(lookup(messages, 'recentPayments.kind.harbor')).toBeUndefined()
      expect(lookup(messages, 'recentPayments.kind.merch')).toBeUndefined()
    })

    it(`${locale} has every overview string`, () => {
      const missing = KEYS.filter((k) => typeof lookup(messages, k) !== 'string' || lookup(messages, k) === '')
      expect(missing).toEqual([])
    })

    it(`${locale} names no provider in the subtitle`, () => {
      expect(String(lookup(messages, 'recentPayments.subtitle'))).not.toMatch(/vipps|stripe/i)
    })
  }
})
