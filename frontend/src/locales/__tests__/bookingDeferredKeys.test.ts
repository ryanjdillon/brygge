import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// The messages the Vipps booking slot (BRY-564) adds: a deferred booking in an
// upload or sync result, and Synk Vipps refused while another booking runs.
const KEYS = ['bookingDeferred', 'vippsBookingBusy'].map((k) => `admin.bankImports.${k}`)

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('Vipps booking slot locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    for (const key of KEYS) {
      it(`${locale} has ${key}`, () => {
        const value = lookup(messages, key)
        expect(typeof value).toBe('string')
        expect((value as string).length).toBeGreaterThan(0)
      })
    }
  }
})
