import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The harbor share (moloandel) column, filter and edit fields (BRD-14) are
// translated in every locale rather than falling back to English.
const KEYS = [
  'admin.columns.harborShare',
  'admin.users.harborShareFilterLabel',
  'admin.users.harborShareFilterAll',
  'admin.users.harborShareFilterOwner',
  'admin.users.harborShareFilterNotOwner',
  'admin.users.harborShareYesPaid',
  'admin.users.harborShareYesUnknownDate',
  'admin.users.harborShareOwner',
  'admin.users.harborSharePaidOn',
  'admin.users.harborSharePaidOnHint',
  'admin.users.harborShareClearDate',
]

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('harbor share locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has every harbor share string`, () => {
      const missing = KEYS.filter((k) => typeof lookup(messages, k) !== 'string' || lookup(messages, k) === '')
      expect(missing).toEqual([])
      expect(lookup(messages, 'admin.users.harborShareYesPaid')).toContain('{date}')
    })
  }
})
