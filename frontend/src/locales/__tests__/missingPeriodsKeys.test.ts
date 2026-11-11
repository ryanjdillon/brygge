import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// BRY-578: an upload or sync that left payments for a year with no fiscal
// period names the years to create, and the settlement preview words a
// locked period apart from a closed one (a locked one cannot be reopened).
const KEY = 'admin.bankImports.missingPeriodsNotice'
const PERIOD_KEYS = ['periodClosedNotice', 'periodLockedNotice'].map((k) => `admin.bankImports.${k}`)

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('missing fiscal year locale key', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has ${KEY} with the years placeholder`, () => {
      const value = lookup(messages, KEY)
      expect(typeof value).toBe('string')
      expect(value as string).toContain('{years}')
    })
    for (const key of PERIOD_KEYS) {
      it(`${locale} has ${key} with the year placeholder`, () => {
        const value = lookup(messages, key)
        expect(typeof value).toBe('string')
        expect(value as string).toContain('{year}')
      })
    }
  }
})
