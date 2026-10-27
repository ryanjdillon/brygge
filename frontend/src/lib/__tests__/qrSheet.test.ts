import { describe, it, expect } from 'vitest'
import {
  MIN_QR_MM,
  MIN_QR_MM_LARGE,
  cardTranslations,
  chunkPages,
  fitScale,
  minQrPx,
  mmToPx,
  normalisePrintLangs,
  parseLangList,
  gridDims,
  parseCardsPerPage,
  targetPriceLine,
} from '@/lib/qrSheet'

describe('parseCardsPerPage', () => {
  it('accepts 2, 3, 4 and 6 as numbers or strings', () => {
    expect(parseCardsPerPage(2)).toBe(2)
    expect(parseCardsPerPage('3')).toBe(3)
    expect(parseCardsPerPage('4')).toBe(4)
    expect(parseCardsPerPage(['6'])).toBe(6)
  })

  it('falls back to 4 for anything else', () => {
    for (const v of [undefined, null, '', '5', 'abc', 8, 0]) {
      expect(parseCardsPerPage(v)).toBe(4)
    }
  })
})

describe('gridDims', () => {
  it('maps 2/3/4/6 to landscape grids', () => {
    expect(gridDims(2)).toEqual({ cols: 2, rows: 1 })
    expect(gridDims(3)).toEqual({ cols: 3, rows: 1 })
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

describe('mmToPx', () => {
  it('uses the CSS 96 px per inch', () => {
    expect(mmToPx(25.4)).toBeCloseTo(96)
    expect(mmToPx(0)).toBe(0)
  })
})

describe('minQrPx', () => {
  it('needs a larger QR on the 2-up cards', () => {
    expect(minQrPx(2)).toBeCloseTo(mmToPx(MIN_QR_MM_LARGE))
    expect(minQrPx(4)).toBeCloseTo(mmToPx(MIN_QR_MM))
    expect(minQrPx(6)).toBeCloseTo(mmToPx(MIN_QR_MM))
  })
})

describe('fitScale', () => {
  const base = { otherHeight: 20, cardInnerHeight: 300, minQrPx: 130 }

  it('keeps full size when everything fits', () => {
    expect(fitScale({ ...base, textHeightAt1: 100 })).toEqual({ scale: 1, overflow: false })
  })

  it('keeps full size on an exact fit', () => {
    expect(fitScale({ ...base, textHeightAt1: 150 })).toEqual({ scale: 1, overflow: false })
  })

  it('steps down to the largest scale that fits', () => {
    // 150 px of room for text: 200 needs 0.75, 180 needs 0.8333 → 0.8.
    expect(fitScale({ ...base, textHeightAt1: 200 })).toEqual({ scale: 0.75, overflow: false })
    expect(fitScale({ ...base, textHeightAt1: 180 })).toEqual({ scale: 0.8, overflow: false })
  })

  it('reaches the floor without overflow when the floor just fits', () => {
    expect(fitScale({ ...base, textHeightAt1: 250 })).toEqual({ scale: 0.6, overflow: false })
  })

  it('flags overflow at the floor when even that is too much', () => {
    expect(fitScale({ ...base, textHeightAt1: 251 })).toEqual({ scale: 0.6, overflow: true })
  })

  it('flags overflow when the fixed parts and QR alone exceed the card', () => {
    expect(fitScale({ ...base, textHeightAt1: 0, otherHeight: 200 })).toEqual({ scale: 0.6, overflow: true })
  })

  it('returns only whole steps, free of float drift', () => {
    const seen = new Set<number>()
    for (let h = 150; h <= 250; h += 1) seen.add(fitScale({ ...base, textHeightAt1: h }).scale)
    expect([...seen].sort((a, b) => a - b)).toEqual([0.6, 0.65, 0.7, 0.75, 0.8, 0.85, 0.9, 0.95, 1])
  })

  it('honours a custom floor and step', () => {
    expect(fitScale({ ...base, textHeightAt1: 200, floor: 0.8, step: 0.1 })).toEqual({ scale: 0.8, overflow: true })
    expect(fitScale({ ...base, textHeightAt1: 160, floor: 0.5, step: 0.1 })).toEqual({ scale: 0.9, overflow: false })
  })

  it('demands more room for the large card QR', () => {
    const card = { textHeightAt1: 120, otherHeight: 30, cardInnerHeight: 330 }
    expect(fitScale({ ...card, minQrPx: minQrPx(4) })).toEqual({ scale: 1, overflow: false })
    // 330 - 30 - 226.8 leaves 73 px of text room: 120 needs 0.6.
    expect(fitScale({ ...card, minQrPx: minQrPx(2) })).toEqual({ scale: 0.6, overflow: false })
    expect(fitScale({ ...card, cardInnerHeight: 320, minQrPx: minQrPx(2) })).toEqual({ scale: 0.6, overflow: true })
  })
})
