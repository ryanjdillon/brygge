// Layout helpers for the harbor QR print sheet (BRY-271): A4 landscape pages
// holding 2, 4 or 6 cards. The page lays the cards out on a fixed grid for
// the cards-per-page choice, so a page with fewer cards (or a single card)
// prints them at the size they would have on a full page.

export type CardsPerPage = 2 | 4 | 6

export const CARDS_PER_PAGE: readonly CardsPerPage[] = [2, 4, 6]
export const DEFAULT_CARDS_PER_PAGE: CardsPerPage = 4

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

// Languages for the print sheet's instruction lines and card translations
// (BRY-437). Codes are UI locale codes; the order of `order` (the locale
// picker's) decides the printed order after the club language.
export const DEFAULT_PRINT_LANGS: readonly string[] = ['nb', 'en', 'de', 'nl']
export const PRINT_LANGS_STORAGE_KEY = 'brygge-qr-print-langs'

// parseLangList reads a comma-separated list from a query value or stored
// string. Absent or empty is null, so the caller can fall through to the
// next source.
export function parseLangList(v: unknown): string[] | null {
  const raw = Array.isArray(v) ? v[0] : v
  if (typeof raw !== 'string') return null
  const list = raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
  return list.length ? list : null
}

// normalisePrintLangs keeps the known codes from requested, in `order`, with
// the club language always first whether it was requested or not.
export function normalisePrintLangs(
  requested: readonly string[],
  clubLang: string,
  order: readonly string[],
): string[] {
  const want = new Set(requested)
  const rest = order.filter((code) => code !== clubLang && want.has(code))
  return order.includes(clubLang) ? [clubLang, ...rest] : rest
}

// cardTranslations is the card's second line: the target's names in the
// selected languages other than the club's, skipping ones it has none for.
export function cardTranslations(
  translations: Record<string, string> | undefined,
  langs: readonly string[],
  clubLang: string,
): string[] {
  return langs
    .filter((code) => code !== clubLang)
    .map((code) => translations?.[code]?.trim() ?? '')
    .filter(Boolean)
}
