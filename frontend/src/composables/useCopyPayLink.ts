// Shared logic for the admin "copy pay link" actions (BRY-274 per-invoice,
// BRY-275 per-member): fetch a durable pay link from an admin endpoint that
// returns { pay_url } and copy it to the clipboard. Both the faktura list and
// the members admin view use this so the fetch/clipboard/error handling stays in
// one place. The caller maps the result to its own toast/flash and i18n.

export type PayLinkFailure =
  | 'online_payment_unavailable'
  | 'online_payment_over_limit'
  | 'amount_below_minimum'
  | 'imported_no_opening_balance'
  | 'failed'

export type CopyPayLinkResult = { ok: true; url: string } | { ok: false; reason: PayLinkFailure }

/**
 * Fetches a pay link from the given admin endpoint (cookie-authed) and copies it
 * to the clipboard. Returns a discriminated result so callers can show their own
 * localized message. A 409 with `{ error: 'online_payment_unavailable' }` is
 * reported distinctly so the UI can explain no payment provider is configured.
 */
export async function copyPayLink(url: string): Promise<CopyPayLinkResult> {
  try {
    const res = await fetch(url, { credentials: 'include' })
    if (!res.ok) {
      const data = await res.json().catch(() => null)
      let reason: PayLinkFailure = 'failed'
      if (res.status === 409 && data?.error === 'online_payment_unavailable') reason = 'online_payment_unavailable'
      else if (res.status === 409 && data?.error === 'online_payment_over_limit') reason = 'online_payment_over_limit'
      else if (res.status === 409 && data?.error === 'amount_below_minimum') reason = 'amount_below_minimum'
      else if (res.status === 409 && data?.code === 'imported_no_opening_balance') reason = 'imported_no_opening_balance'
      return { ok: false, reason }
    }
    const { pay_url } = (await res.json()) as { pay_url: string }
    await navigator.clipboard.writeText(pay_url)
    return { ok: true, url: pay_url }
  } catch {
    return { ok: false, reason: 'failed' }
  }
}

// Maps a failure reason to its i18n key so both copy-link callers show the same
// localized message (admin.payLink.*).
export function payLinkErrorKey(reason: PayLinkFailure): string {
  switch (reason) {
    case 'online_payment_unavailable':
      return 'admin.payLink.unavailable'
    case 'online_payment_over_limit':
      return 'admin.payLink.overLimit'
    case 'amount_below_minimum':
      return 'admin.payLink.belowMinimum'
    case 'imported_no_opening_balance':
      return 'admin.accounting.importedNoOpeningBalance'
    default:
      return 'admin.payLink.failed'
  }
}

export function useCopyPayLink() {
  return { copyPayLink }
}
