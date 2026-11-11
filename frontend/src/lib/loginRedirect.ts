// Where to go after logging in, when the visitor was sent to /login from a
// page that needs a session (the router guard puts it in ?redirect=). A
// link in an e-mail (BRY-575: Bilag with ?openPeriod=…) then lands on its
// page instead of the portal.
//
// Only a same-origin relative path is ever followed: it must start with one
// "/" (not "//", which a browser reads as another host), hold no backslash
// or control character, resolve to this origin, and still not start with
// "//" once normalised ("/.//host" resolves to "//host"). Anything else is
// dropped, so ?redirect= cannot send a visitor off-site.
//
// The magic link is opened from the e-mail, often in a new tab, so a
// successful login-link request keeps the path in localStorage together with
// the address the link went to, for the length of a login link's life. The
// page the link lands on takes it (read and cleared) only for that account;
// logging out clears it.

const STORAGE_KEY = 'brygge.loginRedirect'
// A login link works for 15 minutes; allow for a slow inbox.
const MAX_AGE_MS = 60 * 60 * 1000
const BASE = 'https://origin.invalid'

/** safeRedirect returns the path when it is a same-origin relative path, else null. */
export function safeRedirect(value: unknown): string | null {
  if (typeof value !== 'string' || value === '') return null
  if (!value.startsWith('/') || value.startsWith('//')) return null
  // eslint-disable-next-line no-control-regex
  if (/[\\\u0000-\u001f\u007f]/.test(value)) return null
  let url: URL
  try {
    url = new URL(value, BASE)
  } catch {
    return null
  }
  if (url.origin !== BASE) return null
  const path = url.pathname + url.search + url.hash
  if (path.startsWith('//')) return null
  // The login pages themselves are never a destination.
  const lower = url.pathname.toLowerCase()
  if (lower === '/login' || lower.startsWith('/login/')) return null
  return path
}

function sameAccount(a: string, b: string): boolean {
  return a.trim().toLowerCase() === b.trim().toLowerCase() && a.trim() !== ''
}

/**
 * rememberLoginRedirect keeps a safe path for the page the login link opens,
 * for the account the link was sent to; an unsafe or missing path clears it.
 */
export function rememberLoginRedirect(value: unknown, email: string): void {
  try {
    const path = safeRedirect(value)
    if (path && email.trim() !== '') {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ path, email: email.trim(), at: Date.now() }))
    } else {
      localStorage.removeItem(STORAGE_KEY)
    }
  } catch {
    // Storage unavailable (private mode): the login still lands on the portal.
  }
}

/** clearLoginRedirect forgets a kept path (logout, a login by another route). */
export function clearLoginRedirect(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
  } catch {
    // nothing kept
  }
}

/**
 * takeLoginRedirect returns the kept path once (it is cleared either way),
 * when it is still fresh and safe and was kept for the account that logged in.
 */
export function takeLoginRedirect(account: string | null | undefined, now = Date.now()): string | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    localStorage.removeItem(STORAGE_KEY)
    if (!raw || !account) return null
    const { path, email, at } = JSON.parse(raw) as { path?: unknown; email?: unknown; at?: unknown }
    if (typeof at !== 'number' || now - at > MAX_AGE_MS || now < at) return null
    if (typeof email !== 'string' || !sameAccount(email, account)) return null
    return safeRedirect(path)
  } catch {
    return null
  }
}
