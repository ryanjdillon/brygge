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
