import { describe, it, expect, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import de from '@/locales/de.json'
import en from '@/locales/en.json'
import fr from '@/locales/fr.json'
import it_ from '@/locales/it.json'
import nb from '@/locales/nb.json'
import nl from '@/locales/nl.json'
import nn from '@/locales/nn.json'
import pl from '@/locales/pl.json'
import { PAYMENT_FILTER_ICON_IDS } from '@/lib/paymentFilterIcons'

// The payment filter settings (BRY-522) are used by the board in their own
// language. English is the reference; the rest must have every key and the
// same {placeholders}.
const LOCALES: Record<string, unknown> = { de, fr, it: it_, nb, nl, nn, pl }

function flatten(node: unknown, prefix = ''): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries((node ?? {}) as Record<string, unknown>)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'string') out[key] = v
    else Object.assign(out, flatten(v, key))
  }
  return out
}

// Unique: a plural message names a placeholder once per form.
const placeholders = (s: string) => [...new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort()

const reference = flatten((en as { paymentFilters: unknown }).paymentFilters, 'paymentFilters')
const referenceExtra: Record<string, string> = {
  ...flatten((en as { recentPayments: { filters: unknown } }).recentPayments.filters, 'recentPayments.filters'),
  'common.selectAll': (en as { common: { selectAll: string } }).common.selectAll,
  'common.deselectAll': (en as { common: { deselectAll: string } }).common.deselectAll,
}

describe('payment filter locale keys', () => {
  it('has a name for every icon the editor offers', () => {
    for (const id of PAYMENT_FILTER_ICON_IDS) {
      expect(reference[`paymentFilters.icons.${id}`], id).toBeTruthy()
    }
  })

  for (const [locale, messages] of Object.entries(LOCALES)) {
    it(`${locale} has every key, with the placeholders of English`, () => {
      const got: Record<string, string> = {
        ...flatten((messages as { paymentFilters: unknown }).paymentFilters, 'paymentFilters'),
        ...flatten((messages as { recentPayments: { filters: unknown } }).recentPayments.filters, 'recentPayments.filters'),
        'common.selectAll': (messages as { common: { selectAll: string } }).common.selectAll,
        'common.deselectAll': (messages as { common: { deselectAll: string } }).common.deselectAll,
      }
      const want: Record<string, string> = { ...reference, ...referenceExtra }
      expect(Object.keys(got).sort()).toEqual(Object.keys(want).sort())
      for (const [key, text] of Object.entries(want)) {
        expect(got[key], key).toBeTruthy()
        expect(placeholders(got[key]), key).toEqual(placeholders(text))
      }
    })
  }
})

// The global test setup stubs vue-i18n; plural selection is the library's own.
vi.unmock('vue-i18n')

describe('payment filter plural messages', () => {
  const messages = { en, nb, nn, de, fr, nl, it: it_, pl }
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages })
  const t = i18n.global.t
  const at = (locale: string, key: string, count: number, extra: Record<string, unknown> = {}) => {
    i18n.global.locale.value = locale as 'en'
    return t(key, { count, ...extra }, count)
  }

  it.each([
    ['en', 1, '1 price'],
    ['en', 2, '2 prices'],
    ['en', 0, '0 prices'],
    ['nb', 1, '1 pris'],
    ['nb', 3, '3 priser'],
    ['nn', 1, '1 pris'],
    ['nn', 3, '3 prisar'],
    ['de', 1, '1 Preis'],
    ['de', 4, '4 Preise'],
    ['pl', 1, 'Ceny: 1'],
    ['pl', 5, 'Ceny: 5'],
  ])('itemCount in %s with %i', (locale, n, expected) => {
    expect(at(locale, 'paymentFilters.itemCount', n)).toBe(expected)
  })

  it.each(['paymentFilters.moveConfirm.body', 'paymentFilters.delete.body'])('%s has a one and a many form in every locale', (key) => {
    for (const locale of Object.keys(messages)) {
      const one = at(locale, key, 1, { group: 'G' })
      const many = at(locale, key, 3, { group: 'G' })
      expect(one, locale).toContain('1')
      expect(many, locale).toContain('3')
      expect(one, locale).not.toContain('{')
      if (locale !== 'pl') expect(one, locale).not.toBe(many.replace('3', '1'))
    }
  })
})
