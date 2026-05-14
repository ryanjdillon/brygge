// Minimal allowlist HTML sanitiser for the shared-inbox reader
// (DIL-277). v1 strips structural injection vectors and removes JS
// event handlers + javascript: URLs. Remote `<img>` src is rewritten
// to a data-URI 1x1 transparent gif unless `showImages` is true, in
// which case the original URL is wrapped via the (not-yet-implemented)
// `/api/v1/admin/inbox/proxy-image` endpoint.
//
// TODO(DIL-279): swap this for DOMPurify (`npm i dompurify`) once the
// frontend dep bump goes through. The Browser DOMPurify config is
// the same shape: forbid script/iframe/object/embed/form/base, strip
// on* attrs, normalise URLs, and re-target relative URLs through the
// image proxy. This util keeps the call sites stable so the swap is
// one-file-change.

const VOID_TAGS = new Set(['br', 'hr', 'img'])
const ALLOWED_TAGS = new Set([
  'a', 'b', 'blockquote', 'br', 'code', 'div', 'em', 'h1', 'h2', 'h3',
  'h4', 'h5', 'h6', 'hr', 'i', 'img', 'li', 'ol', 'p', 'pre', 's',
  'span', 'strong', 'table', 'tbody', 'td', 'th', 'thead', 'tr', 'u',
  'ul',
])
const ALLOWED_ATTRS = new Set([
  'href', 'src', 'alt', 'title', 'colspan', 'rowspan', 'align',
])

const PIXEL =
  'data:image/gif;base64,R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw=='

export interface SanitizeOptions {
  showImages?: boolean
  proxyBase?: string // e.g. '/api/v1/admin/inbox/proxy-image'
}

export function sanitizeEmail(html: string, opts: SanitizeOptions = {}): string {
  if (typeof window === 'undefined' || !html) return ''
  const doc = new DOMParser().parseFromString(html, 'text/html')
  walk(doc.body, opts)
  return doc.body.innerHTML
}

function walk(node: Element, opts: SanitizeOptions): void {
  // Iterate over a snapshot — we mutate children during the walk.
  const children = Array.from(node.children)
  for (const el of children) {
    const tag = el.tagName.toLowerCase()
    if (!ALLOWED_TAGS.has(tag)) {
      el.remove()
      continue
    }
    // Drop unsafe attributes.
    for (const a of Array.from(el.attributes)) {
      const name = a.name.toLowerCase()
      const value = a.value
      if (name.startsWith('on') || !ALLOWED_ATTRS.has(name)) {
        el.removeAttribute(a.name)
        continue
      }
      if ((name === 'href' || name === 'src') && /^\s*javascript:/i.test(value)) {
        el.removeAttribute(a.name)
        continue
      }
    }
    if (tag === 'a') {
      el.setAttribute('rel', 'noopener nofollow noreferrer')
      el.setAttribute('target', '_blank')
    }
    if (tag === 'img') {
      const src = el.getAttribute('src') || ''
      if (!opts.showImages || !/^https?:/i.test(src)) {
        el.setAttribute('src', PIXEL)
      } else if (opts.proxyBase) {
        el.setAttribute('src', `${opts.proxyBase}?url=${encodeURIComponent(src)}`)
        el.setAttribute('referrerpolicy', 'no-referrer')
      }
    }
    if (!VOID_TAGS.has(tag)) walk(el, opts)
  }
}
