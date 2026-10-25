import { describe, it, expect } from 'vitest'
import {
  cellSizeMm,
  chunkPages,
  gridDims,
  parseCardsPerPage,
  targetPriceLine,
  GAP_MM,
  GRID_HEIGHT_MM,
  GRID_WIDTH_MM,
  type CardsPerPage,
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

describe('cellSizeMm', () => {
  it.each([2, 4, 6] as CardsPerPage[])('a full %i-up page fits the grid area', (per) => {
    const { cols, rows } = gridDims(per)
    const { width, height } = cellSizeMm(per)
    expect(cols * width + (cols - 1) * GAP_MM).toBeLessThanOrEqual(GRID_WIDTH_MM)
    expect(rows * height + (rows - 1) * GAP_MM).toBeLessThanOrEqual(GRID_HEIGHT_MM)
  })

  it('keeps 4- and 6-up cards tall enough for a ~50 mm QR', () => {
    expect(cellSizeMm(4).height).toBeGreaterThanOrEqual(75)
    expect(cellSizeMm(6).width).toBeGreaterThanOrEqual(85)
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
