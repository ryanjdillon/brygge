// DOM side of the QR card text fit (BRY-438): reads one rendered card and
// hands the numbers to fitScale. Kept apart from the composable so tests,
// where jsdom lays nothing out, can replace the measurement alone.
import { fitHeadlinePt, fitScale, type FitResult } from '@/lib/qrSheet'

function px(v: string): number {
  const n = parseFloat(v)
  return Number.isFinite(n) ? n : 0
}

function verticalMargins(el: Element): number {
  const s = getComputedStyle(el)
  return px(s.marginTop) + px(s.marginBottom)
}

// measureQrCard expects the card to be laid out at --fit: 1. The text block
// is a flex column, so its height already includes its children's margins;
// those are in mm and don't scale, so they move to the fixed part.
export function measureQrCard(card: HTMLElement, minQrPx: number): FitResult {
  const cardStyle = getComputedStyle(card)
  const cardInnerHeight = card.clientHeight - px(cardStyle.paddingTop) - px(cardStyle.paddingBottom)
  // Not laid out yet (hidden or detached): nothing to fit, and flagging it
  // would flash a false overflow.
  if (cardInnerHeight <= 0) return { scale: 1, overflow: false }

  const text = card.querySelector<HTMLElement>('[data-fit="text"]')
  const textChildren = text ? Array.from(text.children) : []
  const textMargins = textChildren.reduce((sum, el) => sum + verticalMargins(el), 0)
  const textHeight = text ? text.getBoundingClientRect().height : 0

  const fixed = Array.from(card.querySelectorAll<HTMLElement>('[data-fit="fixed"]'))
  const fixedHeight = fixed.reduce((sum, el) => sum + el.getBoundingClientRect().height + verticalMargins(el), 0)

  return fitScale({
    textHeightAt1: Math.max(0, textHeight - textMargins),
    otherHeight: textMargins + fixedHeight,
    cardInnerHeight,
    minQrPx,
  })
}

// measureHeadline fits a page headline to one line across its box. The
// element carries its design size in data-headline-pt and is sized by the
// --headline-fit custom property, which this sets.
export function measureHeadline(el: HTMLElement): number {
  const target = Number(el.dataset.headlinePt) || 0
  el.style.setProperty('--headline-fit', `${target}pt`)
  const pt = fitHeadlinePt(el.scrollWidth, el.clientWidth, target)
  el.style.setProperty('--headline-fit', `${pt}pt`)
  return pt
}
