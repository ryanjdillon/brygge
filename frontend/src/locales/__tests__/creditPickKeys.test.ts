import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The manual faktura search for credit-note links (BRY-393) exists in every
// locale. Older parts of the credit section still fall back to English in
// some locales, so parity is pinned for this feature's keys only.
const KEYS = [
  'noCandidates',
  'searchOption',
  'searchTitle',
  'searchBody',
  'searchPlaceholder',
  'searchEmpty',
  'searchTruncated',
  'pick',
  'otherAmount',
  'otherPayer',
  'stateOpen',
  'statePaid',
  'differentPayerTitle',
  'differentPayerBody',
  'differentPayerAction',
].map((k) => `admin.faktura.credit.${k}`)

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('credit-note faktura search locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has every search string`, () => {
      const missing = KEYS.filter((k) => typeof lookup(messages, k) !== 'string' || lookup(messages, k) === '')
      expect(missing).toEqual([])
    })
  }
})
