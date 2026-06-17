// Single source of truth for assembling display names from a user-shaped
// record. Until DIL-230 drops `full_name` from API responses, we accept
// either shape and prefer the explicit fields when present.
export function formatName(u: {
  first_name?: string | null
  last_name?: string | null
  full_name?: string | null
}): string {
  const first = (u.first_name ?? '').trim()
  const last = (u.last_name ?? '').trim()
  if (first || last) return `${first} ${last}`.trim()
  return (u.full_name ?? '').trim()
}

// Norwegian krone money formatting, shared so every view renders amounts
// identically. Mirrors the inline Intl.NumberFormat used across the admin
// faktura/bank screens.
export function formatNOK(amount: number, locale = 'nb-NO'): string {
  return new Intl.NumberFormat(locale, { style: 'currency', currency: 'NOK' }).format(amount)
}

// Locale-aware short date from an ISO string (YYYY-MM-DD or RFC3339).
// Returns the raw input unchanged if it can't be parsed.
export function formatDate(iso: string | null | undefined, locale = 'nb-NO'): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleDateString(locale)
}
