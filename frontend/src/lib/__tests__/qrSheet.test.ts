import { describe, it, expect } from 'vitest'
import {
  cardTranslations,
  chunkPages,
  normalisePrintLangs,
  parseLangList,
  gridDims,
  parseCardsPerPage,
  targetPriceLine,
} from '@/lib/qrSheet'

describe('parseCardsPerPage', () => {
  it('accepts 2, 4 and 6 as numbers or strings', () => {
    expect(parseCardsPerPage(2)).toBe(2)
    expect(parseCardsPerPage('4')).toBe(4)
    expect(parseCardsPerPage(['6'])).toBe(6)
  })

  it('falls back to 4 for anything else', () => {
    for (const v of [undefined, null, '', '3', 'abc', 8, 0]) {
      expect(parseCardsPerPage(v)).toBe(4)
    }
  })
})

describe('gridDims', () => {
  it('maps 2/4/6 to landscape grids', () => {
    expect(gridDims(2)).toEqual({ cols: 2, rows: 1 })
    expect(gridDims(4)).toEqual({ cols: 2, rows: 2 })
    expect(gridDims(6)).toEqual({ cols: 3, rows: 2 })
  })
})

describe('targetPriceLine', () => {
  it('adds the unit for per-unit targets', () => {
    expect(targetPriceLine(300, 'natt', false)).toBe('300 kr / natt')
  })

  it('omits the unit for fixed-price targets', () => {
    expect(targetPriceLine(1600, 'natt', true)).toMatch(/^1\s600 kr$/)
  })

  it('keeps øre when present', () => {
    expect(targetPriceLine(12.5, 'stk', false)).toBe('12,5 kr / stk')
  })
})

describe('chunkPages', () => {
  const items = (n: number) => Array.from({ length: n }, (_, i) => i)

  it('puts one item on one page', () => {
    expect(chunkPages(items(1), 4)).toEqual([[0]])
  })

  it('fills exactly one page with per items', () => {
    expect(chunkPages(items(6), 6)).toHaveLength(1)
  })

  it('spills per+1 items onto a second page', () => {
    expect(chunkPages(items(5), 4).map((p) => p.length)).toEqual([4, 1])
  })

  it('paginates 13 items', () => {
    expect(chunkPages(items(13), 6).map((p) => p.length)).toEqual([6, 6, 1])
    expect(chunkPages(items(13), 2)).toHaveLength(7)
  })

  it('returns no pages for no items', () => {
    expect(chunkPages([], 4)).toEqual([])
  })
})

describe('print languages', () => {
  const order = ['nb', 'nn', 'en', 'de', 'fr', 'nl', 'it', 'pl']

  it('parses a comma list and treats empty as absent', () => {
    expect(parseLangList(' en, de ,')).toEqual(['en', 'de'])
    expect(parseLangList(['nl,pl'])).toEqual(['nl', 'pl'])
    expect(parseLangList('')).toBeNull()
    expect(parseLangList(undefined)).toBeNull()
  })

  it('orders by the picker, drops unknown codes and forces the club language first', () => {
    expect(normalisePrintLangs(['nl', 'xx', 'en'], 'de', order)).toEqual(['de', 'en', 'nl'])
    expect(normalisePrintLangs(['nb', 'de'], 'nb', order)).toEqual(['nb', 'de'])
  })

  it('takes card translations for the non-club languages that have one', () => {
    const tr = { nb: 'Gjest', en: 'Guest', de: ' ', fr: 'Invité' }
    expect(cardTranslations(tr, ['nb', 'en', 'de', 'fr'], 'nb')).toEqual(['Guest', 'Invité'])
    expect(cardTranslations(undefined, ['nb', 'en'], 'nb')).toEqual([])
  })
})
