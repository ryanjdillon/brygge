// HTML sanitiser for the shared-inbox reader (DIL-277/320). Thin
// wrapper over DOMPurify so call sites don't have to know about the
// underlying library; the {showImages, proxyBase} contract stays
// stable across future library swaps.
//
// Policy:
// - Drop tags: script, iframe, object, embed, form, base, meta, link
//   (FORBID_TAGS below).
// - Drop attributes starting with `on` (event handlers).
// - Rewrite `javascript:` / `data:` (non-image) URLs to about:blank.
// - <img>: src is replaced with a 1x1 transparent gif unless
//   `showImages` is true, in which case http(s) URLs are routed
//   through the (still-unimplemented) image proxy. See DIL-279.

import DOMPurify from 'dompurify'

const PIXEL =
  'data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw=='

const FORBID_TAGS = ['script', 'iframe', 'object', 'embed', 'form', 'base', 'meta', 'link']

export interface SanitizeOptions {
  showImages?: boolean
  proxyBase?: string // e.g. '/api/v1/admin/inbox/proxy-image'
}

// Module-scoped state for the uponSanitizeAttribute hook. DOMPurify's
// hooks don't get a per-call context, so we stash the active options
// in a closure-visible ref that sanitizeEmail sets before each call.
let activeOptions: SanitizeOptions = {}

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  // Force opener-isolation on links.
  if (node.tagName === 'A' && node.hasAttribute('href')) {
    node.setAttribute('rel', 'noopener nofollow noreferrer')
    node.setAttribute('target', '_blank')
  }

  // Image policy: block by default; opt-in routes through the proxy.
  if (node.tagName === 'IMG') {
    const src = node.getAttribute('src') || ''
    if (!activeOptions.showImages || !/^https?:/i.test(src)) {
      node.setAttribute('src', PIXEL)
      return
    }
    if (activeOptions.proxyBase) {
      node.setAttribute('src', `${activeOptions.proxyBase}?url=${encodeURIComponent(src)}`)
      node.setAttribute('referrerpolicy', 'no-referrer')
    }
  }
})

export function sanitizeEmail(html: string, opts: SanitizeOptions = {}): string {
  if (typeof window === 'undefined' || !html) return ''
  activeOptions = opts
  try {
    return DOMPurify.sanitize(html, {
      FORBID_TAGS,
      // Strip JS / inline-handler attributes; keep the standard
      // safe set otherwise (href, src, alt, title, table layout,
      // text styling are allowed by DOMPurify's defaults).
      FORBID_ATTR: ['style'],
    }) as unknown as string
  } finally {
    activeOptions = {}
  }
}
