import { describe, it, expect } from 'vitest'
import { PAYMENT_FILTER_ICONS, PAYMENT_FILTER_ICON_IDS, groupLabel, iconMeta } from '@/lib/paymentFilterIcons'

// That the map covers every icon the server accepts is a compile-time check
// (`satisfies Record<PaymentFilterIcon, ...>` against the generated OpenAPI
// enum), so a new server icon fails vue-tsc rather than a test here.
describe('paymentFilterIcons', () => {
  it('gives every icon a component and a tint of literal Tailwind classes', () => {
    expect(PAYMENT_FILTER_ICON_IDS.length).toBeGreaterThan(0)
    for (const id of PAYMENT_FILTER_ICON_IDS) {
      const meta = PAYMENT_FILTER_ICONS[id]
      expect(meta.icon, id).toBeTruthy()
      expect(meta.classes, id).toMatch(/^bg-\w+-\d+ text-\w+-\d+ ring-\w+-\d+$/)
    }
  })

  it('offers the seeded groups their old icons', () => {
    for (const id of ['sailboat', 'van', 'forklift', 'file-text', 'circle-dollar-sign'] as const) {
      expect(PAYMENT_FILTER_ICON_IDS).toContain(id)
    }
  })

  it('draws an unknown icon, and a name that is a property of Object, as the fallback', () => {
    const fallback = PAYMENT_FILTER_ICONS['circle-dollar-sign']
    for (const id of ['from-a-newer-server', '', null, undefined, 'constructor', 'toString', '__proto__']) {
      expect(iconMeta(id)).toBe(fallback)
    }
    expect(iconMeta('anchor')).toBe(PAYMENT_FILTER_ICONS.anchor)
  })

  it('labels a group with the name as typed, or the translation of its key', () => {
    const t = (key: string) => `t:${key}`
    expect(groupLabel({ slug: 'a', name: 'Kran', label_key: 'guest' }, t)).toBe('Kran')
    expect(groupLabel({ slug: 'guest', name: null, label_key: 'guest' }, t)).toBe('t:recentPayments.kind.guest')
    expect(groupLabel({ slug: 'lonely', name: null, label_key: null }, t)).toBe('lonely')
  })
})
