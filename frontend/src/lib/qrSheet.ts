// Layout maths for the harbor QR print sheet (BRY-271): A4 landscape pages
// holding 2, 4 or 6 cards. The cell size depends only on the cards-per-page
// choice, so a page with fewer cards (or a single card) prints them at the
// same size they would have on a full page.

export type CardsPerPage = 2 | 4 | 6

export const CARDS_PER_PAGE: readonly CardsPerPage[] = [2, 4, 6]
export const DEFAULT_CARDS_PER_PAGE: CardsPerPage = 4

// Page geometry in mm. The printable box is the A4 landscape page minus the
// page padding; the header takes a fixed band so the grid area is the same
// on every page.
export const PAGE_WIDTH_MM = 297
export const PAGE_HEIGHT_MM = 210
export const PAGE_PADDING_MM = 10
export const HEADER_HEIGHT_MM = 24
export const GAP_MM = 6

export const GRID_WIDTH_MM = PAGE_WIDTH_MM - 2 * PAGE_PADDING_MM
export const GRID_HEIGHT_MM = PAGE_HEIGHT_MM - 2 * PAGE_PADDING_MM - HEADER_HEIGHT_MM

export function parseCardsPerPage(v: unknown): CardsPerPage {
  const n = Number(Array.isArray(v) ? v[0] : v)
  return (CARDS_PER_PAGE as readonly number[]).includes(n) ? (n as CardsPerPage) : DEFAULT_CARDS_PER_PAGE
}

export function gridDims(per: CardsPerPage): { cols: number; rows: number } {
  switch (per) {
    case 2:
      return { cols: 2, rows: 1 }
    case 6:
      return { cols: 3, rows: 2 }
    default:
      return { cols: 2, rows: 2 }
  }
}

// Rounded down to 0.1 mm so a full row never overflows the grid width and
// wraps early.
export function cellSizeMm(per: CardsPerPage): {
  width: number
  height: number
} {
  const { cols, rows } = gridDims(per)
  const floor = (v: number) => Math.floor(v * 10) / 10
  return {
    width: floor((GRID_WIDTH_MM - (cols - 1) * GAP_MM) / cols),
    height: floor((GRID_HEIGHT_MM - (rows - 1) * GAP_MM) / rows),
  }
}

// "300 kr / natt", or "300 kr" for a fixed-price target; matches the poster
// PDF's price line.
export function targetPriceLine(amount: number, unitLabel: string, fixedUnits: boolean): string {
  const kr = `${amount.toLocaleString('nb-NO', { minimumFractionDigits: 0, maximumFractionDigits: 2 })} kr`
  return fixedUnits || !unitLabel ? kr : `${kr} / ${unitLabel}`
}

export function chunkPages<T>(items: readonly T[], per: CardsPerPage): T[][] {
  const pages: T[][] = []
  for (let i = 0; i < items.length; i += per) pages.push(items.slice(i, i + per))
  return pages
}
