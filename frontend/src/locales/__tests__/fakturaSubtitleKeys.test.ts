import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// Each faktura list tab renders `admin.faktura.<status>.subtitle`; a missing
// key shows the raw key path on the page (BRY-394).
const KEYS = [
  'admin.faktura.draft.subtitle',
  'admin.faktura.sent.subtitle',
  'admin.faktura.voided.subtitle',
  'admin.faktura.imported.subtitle',
]

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('faktura tab subtitle locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has every faktura tab subtitle`, () => {
      const missing = KEYS.filter((k) => typeof lookup(messages, k) !== 'string' || lookup(messages, k) === '')
      expect(missing).toEqual([])
    })
  }
})
