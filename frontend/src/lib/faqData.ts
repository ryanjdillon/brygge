// Public FAQ shapes and grouping. Content itself is per-club data served by
// `GET /api/v1/faqs` (see useFaqs) — never hardcoded here, since Brygge is
// multi-tenant. This module only fixes the section order and the model.

export interface FaqItem {
  id: string
  group_key: string
  position: number
  question: string
  answer: string
  link_url?: string | null
  link_label?: string | null
  published: boolean
}

export interface FaqGroup {
  key: string
  items: FaqItem[]
}

// Fixed section order + i18n label keys (chrome). The backend orders items by
// this same sequence; keep the two in sync.
export const FAQ_GROUP_ORDER = ['newcomers', 'members', 'deposit', 'general'] as const

export const faqGroupLabelKey = (key: string): string => `faq.groups.${key}`

// Group an ordered item list into non-empty groups, preserving FAQ_GROUP_ORDER.
export function groupFaqItems(items: FaqItem[]): FaqGroup[] {
  return FAQ_GROUP_ORDER.map((key) => ({
    key,
    items: items.filter((it) => it.group_key === key),
  })).filter((g) => g.items.length > 0)
}
