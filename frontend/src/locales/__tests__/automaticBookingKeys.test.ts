import { describe, it, expect } from 'vitest'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'

// BRY-568: the master switch for the automatic booking run (ACC-13) in
// Økonomiinnstillinger. The label is fixed by ACC-13.
const KEYS = ['automaticBookingLabel', 'automaticBooking', 'automaticBookingHint'].map(
  (k) => `admin.economySettings.${k}`,
)

const LOCALES: Record<string, unknown> = { de, en, fr, it: it_, nb, nl, nn, pl }

function lookup(messages: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], messages)
}

describe('automatic booking locale keys', () => {
  for (const [locale, messages] of Object.entries(LOCALES)) {
    for (const key of KEYS) {
      it(`${locale} has ${key}`, () => {
        const value = lookup(messages, key)
        expect(typeof value).toBe('string')
        expect((value as string).trim()).not.toBe('')
        // vue-i18n reads these as message syntax; none of them takes a placeholder.
        expect(value as string).not.toMatch(/[{}|@$]/)
      })
    }
    it(`${locale} names the system actor as it appears on bilag`, () => {
      expect(lookup(messages, 'admin.economySettings.automaticBookingHint') as string).toContain('Automatisk bokføring')
    })
  }

  it('nb uses the ACC-13 wording', () => {
    expect(lookup(nb, 'admin.economySettings.automaticBooking')).toBe('Bokfør og avstem automatisk')
    expect(lookup(nb, 'admin.economySettings.automaticBookingLabel')).toBe('Automatisk bokføring')
  })
})
