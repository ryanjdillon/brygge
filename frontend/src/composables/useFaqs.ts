import { ref } from 'vue'
import type { FaqItem } from '@/lib/faqData'

// Published FAQ items for the deploy's club, fetched once and shared across
// the home teaser, the /faq page, and the footer link. Mirrors the club
// store's cached single-flight fetch. Failures leave the list empty, which
// simply hides the FAQ entry points.
const items = ref<FaqItem[]>([])
const loaded = ref(false)
let inflight: Promise<void> | null = null

export function useFaqs() {
  async function ensureLoaded() {
    if (loaded.value) return
    if (inflight) return inflight
    inflight = (async () => {
      try {
        const res = await fetch('/api/v1/faqs', { credentials: 'include' })
        if (!res.ok) return
        const data = await res.json()
        items.value = Array.isArray(data.items) ? (data.items as FaqItem[]) : []
      } catch {
        // Network/parse error — leave the list empty.
      } finally {
        loaded.value = true
        inflight = null
      }
    })()
    return inflight
  }

  return { items, loaded, ensureLoaded }
}
