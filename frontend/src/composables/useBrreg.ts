// Tiny wrapper around the Brønnøysund Enhetsregisteret public API.
// CORS is open on data.brreg.no, so we hit it directly from the
// browser — no backend proxy. Two operations are exposed:
//
//   * lookupByOrgNumber — exact match by 9-digit organisasjonsnummer.
//     Validates the mod-11 checksum first to avoid burning a request
//     on an obvious typo. Returns the address fields the faktura
//     form needs, plus konkurs / slettedato so the caller can warn
//     when the entity isn't really billable any more.
//
//   * searchByName — incremental name search (top 10 hits) for an
//     autocomplete dropdown. Falls back gracefully on network errors.
//
// All requests are abortable so successive keystrokes can cancel
// in-flight calls without leaking to the network.

const BASE = 'https://data.brreg.no/enhetsregisteret/api'

export interface BrregEntity {
  organisasjonsnummer: string
  navn: string
  organisasjonsform?: { kode?: string; beskrivelse?: string }
  forretningsadresse?: BrregAddress
  postadresse?: BrregAddress
  konkurs?: boolean
  underAvvikling?: boolean
  slettedato?: string | null
}

export interface BrregAddress {
  adresse?: string[]
  postnummer?: string
  poststed?: string
  land?: string
}

// Mod-11 check on the 9-digit organisasjonsnummer. Weights run
// 3,2,7,6,5,4,3,2 across the first 8 digits; the 9th is the check
// digit. Remainder 0 → checksum 0; remainder 1 → invalid number
// (no valid mod-11 representation); else 11-remainder.
export function isValidOrgNumber(input: string): boolean {
  const s = (input ?? '').replace(/\s/g, '')
  if (!/^\d{9}$/.test(s)) return false
  const w = [3, 2, 7, 6, 5, 4, 3, 2]
  let sum = 0
  for (let i = 0; i < 8; i++) sum += parseInt(s[i], 10) * w[i]
  const rem = sum % 11
  if (rem === 1) return false
  const check = rem === 0 ? 0 : 11 - rem
  return check === parseInt(s[8], 10)
}

// Format the address into the multi-line text we put on the
// `recipient_org_address` field. Joins street lines, then "<postnr>
// <poststed>" on a final line. Returns empty string if nothing useful.
export function formatBrregAddress(a?: BrregAddress | null): string {
  if (!a) return ''
  const lines: string[] = []
  for (const ln of a.adresse ?? []) {
    if (ln && ln.trim()) lines.push(ln.trim())
  }
  const post = [a.postnummer, a.poststed].filter(Boolean).join(' ')
  if (post.trim()) lines.push(post.trim())
  return lines.join('\n')
}

export async function lookupByOrgNumber(
  orgnr: string,
  signal?: AbortSignal,
): Promise<BrregEntity | null> {
  const cleaned = (orgnr ?? '').replace(/\s/g, '')
  if (!isValidOrgNumber(cleaned)) return null
  try {
    const res = await fetch(`${BASE}/enheter/${cleaned}`, { signal })
    if (res.status === 404) return null
    if (!res.ok) return null
    return (await res.json()) as BrregEntity
  } catch {
    // Network errors / aborts are non-fatal; the form keeps working
    // with whatever the admin types manually.
    return null
  }
}

export async function searchByName(
  query: string,
  signal?: AbortSignal,
): Promise<BrregEntity[]> {
  const q = (query ?? '').trim()
  if (q.length < 2) return []
  try {
    const url = `${BASE}/enheter?navn=${encodeURIComponent(q)}&size=10`
    const res = await fetch(url, { signal })
    if (!res.ok) return []
    const body = await res.json()
    const list = body?._embedded?.enheter
    return Array.isArray(list) ? (list as BrregEntity[]) : []
  } catch {
    return []
  }
}
