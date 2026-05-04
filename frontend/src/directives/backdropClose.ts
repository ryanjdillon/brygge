import type { Directive } from 'vue'

// v-backdrop-close="closeFn"
//
// Replaces `@click.self="closeFn"` on modal backdrops. The `.self`
// modifier only checks the click's target, which makes it fire when a
// drag-select inside an input releases the mouse over the backdrop —
// closing the modal mid-edit. This directive instead requires that
// mousedown *and* mouseup both occur on the backdrop element itself.
type Handler = () => void

interface BackdropEl extends HTMLElement {
  __backdropClose__?: {
    onMouseDown: (e: MouseEvent) => void
    onMouseUp: (e: MouseEvent) => void
  }
}

export const vBackdropClose: Directive<BackdropEl, Handler> = {
  mounted(el, binding) {
    let downOnSelf = false
    const onMouseDown = (e: MouseEvent) => {
      downOnSelf = e.target === el
    }
    const onMouseUp = (e: MouseEvent) => {
      if (downOnSelf && e.target === el) binding.value()
      downOnSelf = false
    }
    el.addEventListener('mousedown', onMouseDown)
    el.addEventListener('mouseup', onMouseUp)
    el.__backdropClose__ = { onMouseDown, onMouseUp }
  },
  unmounted(el) {
    const handlers = el.__backdropClose__
    if (!handlers) return
    el.removeEventListener('mousedown', handlers.onMouseDown)
    el.removeEventListener('mouseup', handlers.onMouseUp)
    delete el.__backdropClose__
  },
}
