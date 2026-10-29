import { describe, it, expect, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { SUPPORTED_LOCALES } from '@/i18n'
import nb from '@/locales/nb.json'
import nn from '@/locales/nn.json'
import en from '@/locales/en.json'
import de from '@/locales/de.json'
import fr from '@/locales/fr.json'
import nl from '@/locales/nl.json'
import it_ from '@/locales/it.json'
import pl from '@/locales/pl.json'

// The global test setup replaces vue-i18n with a stub; plural selection is
// the library's own behaviour, so these specs need the real one.
vi.unmock('vue-i18n')

const messages = { nb, nn, en, de, fr, nl, it: it_, pl }

const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages })
const t = i18n.global.t
const setLocale = (code: string) => {
  i18n.global.locale.value = code as (typeof SUPPORTED_LOCALES)[number]
}

// Real plural keys in the call shapes the views use: t(key, params, n)
// for the two-form message and t(key, n) for the three-form message.
const TWO_FORM = 'portal.dashboard.bookingsCount'
const THREE_FORM = 'admin.bankReconcile.partners.daysApart'

function raw(code: string, key: string): string {
  return key.split('.').reduce<unknown>(
    (o, k) => (o as Record<string, unknown>)[k],
    messages[code as keyof typeof messages],
  ) as string
}

describe('plural selection', () => {
  it.each([
    ['en', 1, '1 upcoming booking'],
    ['en', 0, '0 upcoming bookings'],
    ['en', 2, '2 upcoming bookings'],
    ['en', 5, '5 upcoming bookings'],
    ['pl', 1, '1 nadchodząca rezerwacja'],
    ['pl', 2, '2 nadchodzących rezerwacji'],
    ['pl', 5, '5 nadchodzących rezerwacji'],
  ])('two-form message in %s with n=%i', (code, n, expected) => {
    setLocale(code)
    expect(t(TWO_FORM, { count: n }, n)).toBe(expected)
  })

  it.each([
    ['en', 0, 'same day'],
    ['en', 1, '1 day apart'],
    ['en', 2, '2 days apart'],
    ['en', 7, '7 days apart'],
    ['pl', 0, 'ten sam dzień'],
    ['pl', 1, '1 dzień różnicy'],
    ['pl', 2, '2 dni różnicy'],
    ['pl', 12, '12 dni różnicy'],
  ])('three-form message in %s with n=%i (implicit {n})', (code, n, expected) => {
    setLocale(code)
    expect(t(THREE_FORM, n)).toBe(expected)
  })

  it('picks the same form index in every locale', () => {
    const fill = (s: string, n: number) => s.replace('{n}', String(n)).replace('{count}', String(n))
    for (const code of SUPPORTED_LOCALES) {
      setLocale(code)
      const three = raw(code, THREE_FORM).split('|').map((s) => s.trim())
      const two = raw(code, TWO_FORM).split('|').map((s) => s.trim())
      expect(three, code).toHaveLength(3)
      expect(two, code).toHaveLength(2)
      expect(t(THREE_FORM, 0), code).toBe(fill(three[0]!, 0))
      expect(t(THREE_FORM, 1), code).toBe(fill(three[1]!, 1))
      expect(t(THREE_FORM, 3), code).toBe(fill(three[2]!, 3))
      expect(t(TWO_FORM, { count: 1 }, 1), code).toBe(fill(two[0]!, 1))
      expect(t(TWO_FORM, { count: 4 }, 4), code).toBe(fill(two[1]!, 4))
    }
  })
})
