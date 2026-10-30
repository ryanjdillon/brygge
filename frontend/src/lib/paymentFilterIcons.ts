import type { Component } from 'vue'
import {
  Sailboat,
  Van,
  Forklift,
  FileText,
  Store,
  CircleDollarSign,
  Anchor,
  Zap,
  Wrench,
  Tent,
  KeyRound,
  ShoppingBag,
  Coffee,
  Ticket,
} from 'lucide-vue-next'
import type { components } from '@/types/api'

/**
 * The icon ids a payment filter group may carry, from the generated OpenAPI
 * enum (the server's paymentfilters.IconAllowList). A new icon on the server
 * makes PAYMENT_FILTER_ICONS below a type error until it is mapped.
 */
export type PaymentFilterIcon = components['schemas']['PaymentFilterGroup']['icon']

export interface PaymentFilterIconMeta {
  icon: Component
  /** Tint of the round badge. Literal strings: Tailwind finds classes in the source text. */
  classes: string
}

// Each icon has a fixed tint, so groups need no colour setting.
export const PAYMENT_FILTER_ICONS = {
  sailboat: { icon: Sailboat, classes: 'bg-blue-50 text-blue-700 ring-blue-200' },
  van: { icon: Van, classes: 'bg-teal-50 text-teal-700 ring-teal-200' },
  forklift: { icon: Forklift, classes: 'bg-indigo-50 text-indigo-700 ring-indigo-200' },
  'file-text': { icon: FileText, classes: 'bg-slate-100 text-slate-700 ring-slate-200' },
  store: { icon: Store, classes: 'bg-amber-50 text-amber-700 ring-amber-200' },
  'circle-dollar-sign': { icon: CircleDollarSign, classes: 'bg-gray-50 text-gray-600 ring-gray-200' },
  anchor: { icon: Anchor, classes: 'bg-sky-50 text-sky-700 ring-sky-200' },
  zap: { icon: Zap, classes: 'bg-yellow-50 text-yellow-700 ring-yellow-200' },
  wrench: { icon: Wrench, classes: 'bg-violet-50 text-violet-700 ring-violet-200' },
  tent: { icon: Tent, classes: 'bg-green-50 text-green-700 ring-green-200' },
  'key-round': { icon: KeyRound, classes: 'bg-emerald-50 text-emerald-700 ring-emerald-200' },
  'shopping-bag': { icon: ShoppingBag, classes: 'bg-orange-50 text-orange-700 ring-orange-200' },
  coffee: { icon: Coffee, classes: 'bg-rose-50 text-rose-700 ring-rose-200' },
  ticket: { icon: Ticket, classes: 'bg-pink-50 text-pink-700 ring-pink-200' },
} as const satisfies Record<PaymentFilterIcon, PaymentFilterIconMeta>

/** The icons in the order the editor offers them. */
export const PAYMENT_FILTER_ICON_IDS = Object.keys(PAYMENT_FILTER_ICONS) as PaymentFilterIcon[]

const FALLBACK: PaymentFilterIcon = 'circle-dollar-sign'

/**
 * The badge for an icon id. An id this build does not know (a group made by a
 * newer server) renders the fallback instead of nothing.
 */
export function iconMeta(id: string | null | undefined): PaymentFilterIconMeta {
  if (id && Object.prototype.hasOwnProperty.call(PAYMENT_FILTER_ICONS, id)) return PAYMENT_FILTER_ICONS[id as PaymentFilterIcon]
  return PAYMENT_FILTER_ICONS[FALLBACK]
}

/** What a group needs to be labelled. */
export interface FilterGroupRef {
  slug: string
  name: string | null
  label_key: string | null
  icon: string
}

/**
 * The label of a group: the name the board typed, shown as typed in every
 * locale, or for a seeded row the translation of its label key. The same
 * strings the Payments page chips use, so a duplicate-looking name can be
 * caught against them.
 */
export function groupLabel(
  g: Pick<FilterGroupRef, 'slug' | 'name' | 'label_key'>,
  t: (key: string) => string,
): string {
  if (g.name) return g.name
  return g.label_key ? t(`recentPayments.kind.${g.label_key}`) : g.slug
}
