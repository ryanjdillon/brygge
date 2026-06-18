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
// identically. `signed` forces a leading +/- (used by the bank
// reconciliation views to distinguish incoming from outgoing rows).
export function formatNOK(
  amount: number,
  opts: { signed?: boolean; locale?: string } = {},
): string {
  return new Intl.NumberFormat(opts.locale ?? 'nb-NO', {
    style: 'currency',
    currency: 'NOK',
    ...(opts.signed ? { signDisplay: 'always' as const } : {}),
  }).format(amount)
}

// Locale-aware date/time formatting from an ISO string (YYYY-MM-DD or
// RFC3339). Empty input → empty string; an unparseable value is returned
// unchanged. `locale` accepts the active i18n locale and falls back to
// nb-NO when empty, so callers can pass `locale.value` directly.
//
// formatDate     — date only            (e.g. 17.06.2026)
// formatDateMedium — date with short month (e.g. 17. jun. 2026)
// formatDateTime — date + time          (e.g. 17.06.2026, 14:30)

function parseDate(iso: string | null | undefined): Date | null {
  if (!iso) return null
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? null : d
}

export function formatDate(iso: string | null | undefined, locale?: string): string {
  const d = parseDate(iso)
  if (!d) return iso ?? ''
  return d.toLocaleDateString(locale || 'nb-NO')
}

export function formatDateMedium(iso: string | null | undefined, locale?: string): string {
  const d = parseDate(iso)
  if (!d) return iso ?? ''
  return d.toLocaleDateString(locale || 'nb-NO', { year: 'numeric', month: 'short', day: 'numeric' })
}

export function formatDateTime(iso: string | null | undefined, locale?: string): string {
  const d = parseDate(iso)
  if (!d) return iso ?? ''
  return d.toLocaleString(locale || 'nb-NO')
}

// Medium date + time, e.g. "17. juni 2026, 16:30" — used by the events
// and communication admin lists.
export function formatDateTimeMedium(iso: string | null | undefined, locale?: string): string {
  const d = parseDate(iso)
  if (!d) return iso ?? ''
  return d.toLocaleString(locale || 'nb-NO', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}
