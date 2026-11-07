// Phone numbers on the join form, normalised to E.164 by the same simple
// rules as the server (backend/internal/handlers/phone.go), so the form
// refuses exactly what the API would: separators are dropped (brackets too,
// also around the country code as in "(+47)"), a leading 00 reads as +,
// eight digits without a country code are Norwegian (+47), a +47 number has
// exactly eight digits after the code, and any other country code takes 8 to
// 15 digits in all. Both sides test against the same vectors,
// frontend/src/lib/__tests__/phone_vectors.json.

function isNorwegianNational(d: string): boolean {
  return /^[2-9]\d{7}$/.test(d)
}

/** The number in E.164, or null when it is not a plausible one. */
export function normalizePhone(raw: string): string | null {
  const s = raw.trim()
  let n = ''
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (c >= '0' && c <= '9') n += c
    else if (c === '+' && n === '') n += c
    else if (c === ' ' || c === ' ' || c === '-' || c === '.' || c === '(' || c === ')') continue
    else return null
  }
  if (n.startsWith('00')) n = '+' + n.slice(2)
  if (!n.startsWith('+')) return isNorwegianNational(n) ? '+47' + n : null
  const digits = n.slice(1)
  if (digits.startsWith('47')) return isNorwegianNational(digits.slice(2)) ? n : null
  if (digits.length < 8 || digits.length > 15 || digits[0] === '0') return null
  return n
}
