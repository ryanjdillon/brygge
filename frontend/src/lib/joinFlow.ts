// Shared bits of the self-service join pages (BRY-632): the public form, the
// emailed confirmation link and the applicant's "Fullfør innmeldinga" page.

import type { Router } from 'vue-router'

/** The legal documents a joiner consents to, in the order the form lists them. */
export const JOIN_LEGAL_DOCS = ['statutes', 'harbour_rules', 'privacy', 'terms_of_sale'] as const
export type JoinLegalDoc = (typeof JOIN_LEGAL_DOCS)[number]

/** The public page that shows one published legal document. */
export function legalDocPath(doc: JoinLegalDoc): string {
  return `/vilkar/${doc}`
}

// Vite sets import.meta.env; the cast keeps this file free of the vite/client
// types, which the app's tsconfig does not load.
function isDevBuild(): boolean {
  return (import.meta as { env?: { DEV?: boolean } }).env?.DEV === true
}

/** Where a pay link that cannot be trusted sends the user instead. */
export const PAY_LINK_FALLBACK = '/portal'

/**
 * Opens a pay link the server built. It is an absolute URL on the site's
 * public base, which is normally this SPA: then the router takes it, so the
 * refreshed session and the app state survive. Another origin is followed
 * only over https (http too in development); anything else, a
 * protocol-relative "//host" or a non-web scheme such as javascript:, goes to
 * PAY_LINK_FALLBACK, so a bad link can never run script or leave the site
 * unencrypted.
 */
export async function openPayLink(router: Router, url: string, dev = isDevBuild()): Promise<void> {
  if (url.startsWith('/')) {
    // "//host" and "/\host" are read by browsers as another host.
    await router.push(url.startsWith('//') || url.startsWith('/\\') ? PAY_LINK_FALLBACK : url)
    return
  }
  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    await router.push(PAY_LINK_FALLBACK)
    return
  }
  if (parsed.origin === window.location.origin) {
    await router.push(parsed.pathname + parsed.search + parsed.hash)
    return
  }
  if (parsed.protocol === 'https:' || (dev && parsed.protocol === 'http:')) {
    window.location.assign(parsed.href)
    return
  }
  await router.push(PAY_LINK_FALLBACK)
}
