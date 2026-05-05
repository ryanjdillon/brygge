import { ref } from 'vue'
import { defineStore } from 'pinia'

export type ConfirmTone = 'danger' | 'warning' | 'info'

export interface ConfirmOptions {
  title: string
  body: string
  /** Optional list rendered between body and buttons (e.g. selected items). */
  details?: string[]
  confirmLabel?: string
  cancelLabel?: string
  tone?: ConfirmTone
}

interface PendingConfirm extends ConfirmOptions {
  resolve: (ok: boolean) => void
}

// Pinia store backing the global ConfirmDialog. Components call
// `useConfirm()(opts)` and await a boolean instead of using the
// browser's `window.confirm`.
export const useConfirmStore = defineStore('confirm', () => {
  const current = ref<PendingConfirm | null>(null)

  function ask(opts: ConfirmOptions): Promise<boolean> {
    if (current.value) {
      // Resolve any in-flight prompt as cancelled before opening a new one.
      current.value.resolve(false)
    }
    return new Promise((resolve) => {
      current.value = { ...opts, resolve }
    })
  }

  function settle(ok: boolean) {
    current.value?.resolve(ok)
    current.value = null
  }

  return { current, ask, settle }
})

/** Convenience: returns the `ask` function so callers don't deal with the store. */
export function useConfirm(): (opts: ConfirmOptions) => Promise<boolean> {
  const store = useConfirmStore()
  return (opts) => store.ask(opts)
}
