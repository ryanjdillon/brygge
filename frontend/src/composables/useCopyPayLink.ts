// Shared logic for the admin "copy pay link" actions (BRY-274 per-invoice,
// BRY-275 per-member): fetch a durable pay link from an admin endpoint that
// returns { pay_url } and copy it to the clipboard. Both the faktura list and
// the members admin view use this so the fetch/clipboard/error handling stays in
// one place. The caller maps the result to its own toast/flash and i18n.

export type CopyPayLinkResult =
  | { ok: true; url: string }
  | { ok: false; reason: 'vipps_unavailable' | 'failed' }

/**
 * Fetches a pay link from the given admin endpoint (cookie-authed) and copies it
 * to the clipboard. Returns a discriminated result so callers can show their own
 * localized message. A 409 with `{ error: 'vipps_unavailable' }` is reported
 * distinctly so the UI can explain Vipps isn't configured.
 */
export async function copyPayLink(url: string): Promise<CopyPayLinkResult> {
  try {
    const res = await fetch(url, { credentials: 'include' })
    if (!res.ok) {
      const data = await res.json().catch(() => null)
      return {
        ok: false,
        reason:
          res.status === 409 && data?.error === 'vipps_unavailable'
            ? 'vipps_unavailable'
            : 'failed',
      }
    }
    const { pay_url } = (await res.json()) as { pay_url: string }
    await navigator.clipboard.writeText(pay_url)
    return { ok: true, url: pay_url }
  } catch {
    return { ok: false, reason: 'failed' }
  }
}

export function useCopyPayLink() {
  return { copyPayLink }
}
