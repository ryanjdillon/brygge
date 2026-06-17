// Natural-numeric collation so that A2 sorts before A10. Used wherever
// slips appear in a list (admin slips table, slip picker in the user
// edit modal, dock-filter dropdowns, etc.) so sort order stays
// consistent across the SPA.

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

export interface SlipLike {
  section?: string | null
  number?: string | null
}

/**
 * Compare two slip-like records by dock (section), then by slip number.
 * `dir` defaults to ascending; pass 'desc' to reverse both keys at once
 * (so `B-2` -> `B-1` -> `A-3` reads naturally as a single inversion).
 */
export function compareSlip(a: SlipLike, b: SlipLike, dir: 'asc' | 'desc' = 'asc'): number {
  const sec = collator.compare(a.section ?? '', b.section ?? '')
  const cmp = sec !== 0 ? sec : collator.compare(a.number ?? '', b.number ?? '')
  return dir === 'desc' ? -cmp : cmp
}

/**
 * Render a slip as a compact dock-prefixed label, e.g. section "C" +
 * number "18" -> "C18". Used wherever a member's berth is shown so the
 * format stays consistent (never "#18 — C"). Falls back gracefully when
 * either part is missing.
 */
export function formatSlip(slip: SlipLike | null | undefined): string {
  if (!slip) return ''
  const section = (slip.section ?? '').trim()
  const number = (slip.number ?? '').trim()
  return `${section}${number}`
}

/**
 * In-place-style sort returning a new array, ascending or descending.
 */
export function sortBySlip<T extends SlipLike>(items: readonly T[], dir: 'asc' | 'desc' = 'asc'): T[] {
  return [...items].sort((a, b) => compareSlip(a, b, dir))
}
