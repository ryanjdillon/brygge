import { ref, computed, watch, nextTick, onMounted, onBeforeUnmount, type Ref, type WatchSource } from 'vue'
import { measureHeadline, measureQrCard } from '@/lib/qrCardMeasure'
import { cardSize, minQrPx, type CardsPerPage } from '@/lib/qrSheet'

const CARD_SELECTOR = '[data-testid="qr-card"]'
const HEADLINE_SELECTOR = '[data-fit-headline]'

// useFitCards shrinks each QR card's text to fit (see fitScale) by setting
// --fit on the card, and reports which cards still don't fit, keyed by the
// card's data-target-id. The page headlines are fitted first: their size
// sets the header height, and with it the cards'. It measures after every render the inputs cause,
// whenever an image inside root loads (the logo changes the header height,
// and with it the cards'), once web fonts are ready, and just before
// printing.
export function useFitCards(root: Ref<HTMLElement | null>, per: Ref<CardsPerPage>, inputs: WatchSource) {
  const overflow = ref<Record<string, boolean>>({})
  const overflowCount = computed(() => Object.values(overflow.value).filter(Boolean).length)

  function measureAll() {
    const el = root.value
    if (!el) return
    for (const headline of el.querySelectorAll<HTMLElement>(HEADLINE_SELECTOR)) measureHeadline(headline)
    const cards = Array.from(el.querySelectorAll<HTMLElement>(CARD_SELECTOR))
    // Reset every card before reading any, so the reads share one layout
    // instead of forcing one per card.
    for (const card of cards) card.style.setProperty('--fit', '1')
    const min = minQrPx(cardSize(per.value))
    const results = cards.map((card) => measureQrCard(card, min))
    const next: Record<string, boolean> = {}
    cards.forEach((card, i) => {
      card.style.setProperty('--fit', String(results[i].scale))
      const id = card.dataset.targetId
      if (id) next[id] = results[i].overflow
    })
    overflow.value = next
  }

  let pending = false
  function schedule() {
    if (pending) return
    pending = true
    void nextTick(() => {
      pending = false
      measureAll()
    })
  }

  watch(inputs, schedule, { flush: 'post', deep: true })

  onMounted(() => {
    root.value?.addEventListener('load', schedule, true)
    window.addEventListener('beforeprint', measureAll)
    void document.fonts?.ready.then(schedule)
    schedule()
  })

  onBeforeUnmount(() => {
    root.value?.removeEventListener('load', schedule, true)
    window.removeEventListener('beforeprint', measureAll)
  })

  return { overflow, overflowCount }
}
