// Layout helpers for the harbor QR print sheet (BRY-271): A4 landscape pages
// holding 2, 3, 4 or 6 cards. The page lays the cards out on a fixed grid for
// the cards-per-page choice, so a page with fewer cards (or a single card)
// prints them at the size they would have on a full page.

export type CardsPerPage = 2 | 3 | 4 | 6

export const CARDS_PER_PAGE: readonly CardsPerPage[] = [2, 3, 4, 6]
export const DEFAULT_CARDS_PER_PAGE: CardsPerPage = 4

export function parseCardsPerPage(v: unknown): CardsPerPage {
  const n = Number(Array.isArray(v) ? v[0] : v)
  return (CARDS_PER_PAGE as readonly number[]).includes(n) ? (n as CardsPerPage) : DEFAULT_CARDS_PER_PAGE
}

export function gridDims(per: CardsPerPage): { cols: number; rows: number } {
  switch (per) {
    case 2:
      return { cols: 2, rows: 1 }
    case 3:
      return { cols: 3, rows: 1 }
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

// Card text fitting (BRY-438). A card's title, translations and price shrink
// together, down to FIT_FLOOR of their design size, so long names and many
// languages still leave the QR at least its minimum printed size. Below
// that the card is flagged rather than clipped or squashed.
export const MIN_QR_MM = 35
export const MIN_QR_MM_LARGE = 60
export const FIT_FLOOR = 0.6
export const FIT_STEP = 0.05

// CSS pixels per millimetre: CSS fixes 1in = 96px = 25.4mm, on screen and in
// print alike.
export const PX_PER_MM = 96 / 25.4

export function mmToPx(mm: number): number {
  return mm * PX_PER_MM
}

export function minQrPx(per: CardsPerPage): number {
  // 2- and 3-up cards are a full page tall, so they hold the larger code.
  return mmToPx(per === 2 || per === 3 ? MIN_QR_MM_LARGE : MIN_QR_MM)
}

export type FitInput = {
  // Height of the scalable text block at --fit: 1.
  textHeightAt1: number
  // Height that does not scale: margins, the short address line.
  otherHeight: number
  // The card's content-box height.
  cardInnerHeight: number
  minQrPx: number
  floor?: number
  step?: number
}

export type FitResult = { scale: number; overflow: boolean }

// fitScale picks the largest scale, stepping down from 1, at which the text,
// the fixed parts and a minimum-size QR fit the card. It treats the text
// height as proportional to the scale; smaller type also wraps onto fewer
// lines, so the real height is at most that and the estimate errs towards
// leaving the QR more room.
export function fitScale({
  textHeightAt1,
  otherHeight,
  cardInnerHeight,
  minQrPx,
  floor = FIT_FLOOR,
  step = FIT_STEP,
}: FitInput): FitResult {
  const steps = Math.max(0, Math.round((1 - floor) / step))
  for (let i = 0; i <= steps; i++) {
    // Rounded so repeated subtraction can't drift to 0.6499… and skip a step.
    const scale = Math.max(floor, Math.round((1 - i * step) * 1000) / 1000)
    if (textHeightAt1 * scale + otherHeight + minQrPx <= cardInnerHeight) return { scale, overflow: false }
  }
  return { scale: floor, overflow: true }
}
