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
  /**
   * Optional checkbox shown above the buttons. Its state is exposed on the
   * store as `checkboxValue` for the caller to read after the promise
   * resolves (the promise itself still resolves to the OK/cancel boolean).
   */
  checkbox?: { label: string; default?: boolean }
  /**
   * Optional single-choice radio group shown above the buttons. The selected
   * value is exposed on the store as `choiceValue`, read the same way as
   * `checkboxValue`.
   */
  choice?: { label: string; options: { value: string; label: string }[]; default?: string }
  /**
   * Optional list of checkboxes, one per item, shown above the buttons. The
   * values of the ticked items are exposed on the store as `checklistValues`,
   * read the same way as `checkboxValue`. Items are ticked unless `default`
   * is false.
   */
  checklist?: { label: string; items: { value: string; label: string; default?: boolean }[] }
}

interface PendingConfirm extends ConfirmOptions {
  resolve: (ok: boolean) => void
}

// Pinia store backing the global ConfirmDialog. Components call
// `useConfirm()(opts)` and await a boolean instead of using the
// browser's `window.confirm`.
export const useConfirmStore = defineStore('confirm', () => {
  const current = ref<PendingConfirm | null>(null)
  // Bound to the optional checkbox in the dialog; the caller reads it after
  // the ask() promise resolves.
  const checkboxValue = ref(false)
  // Bound to the optional radio group; read the same way as checkboxValue.
  const choiceValue = ref('')
  // Bound to the optional checklist; read the same way as checkboxValue.
  const checklistValues = ref<string[]>([])

  function ask(opts: ConfirmOptions): Promise<boolean> {
    if (current.value) {
      // Resolve any in-flight prompt as cancelled before opening a new one.
      current.value.resolve(false)
    }
    checkboxValue.value = opts.checkbox?.default ?? false
    choiceValue.value = opts.choice?.default ?? opts.choice?.options[0]?.value ?? ''
    checklistValues.value = (opts.checklist?.items ?? []).filter((i) => i.default !== false).map((i) => i.value)
    return new Promise((resolve) => {
      current.value = { ...opts, resolve }
    })
  }

  function settle(ok: boolean) {
    current.value?.resolve(ok)
    current.value = null
  }

  return { current, checkboxValue, choiceValue, checklistValues, ask, settle }
})

/** Convenience: returns the `ask` function so callers don't deal with the store. */
export function useConfirm(): (opts: ConfirmOptions) => Promise<boolean> {
  const store = useConfirmStore()
  return (opts) => store.ask(opts)
}
