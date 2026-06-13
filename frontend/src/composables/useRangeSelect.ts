import { ref, type Ref } from 'vue'

// Drives a multi-select checkbox column on a table. Supports the
// standard click-first / shift-click-last range-select gesture.
//
// Caller owns the underlying `Set<string>` of selected IDs (passed in
// as `selected`), so this composable composes with any existing
// selection state — typical use is `const selected = ref(new Set())`
// outside the composable.
//
// `visible` is a getter for the currently-rendered row list at click
// time (after filter + sort). The composable uses it to map a shift+
// click index range back to row IDs.

export interface RangeSelectable {
  id: string
}

export function useRangeSelect<T extends RangeSelectable>(
  selected: Ref<Set<string>>,
  visible: () => readonly T[],
) {
  const anchorIndex = ref<number | null>(null)

  // Use on `@click` (not `@change`) so MouseEvent's shiftKey is
  // available. Call `.preventDefault()` because we drive the
  // checkbox's checked state manually via `:checked`.
  function onCheckboxClick(index: number, event: MouseEvent) {
    event.preventDefault()
    const rows = visible()
    const clicked = rows[index]
    if (!clicked) return
    const wantSelected = !selected.value.has(clicked.id)
    const next = new Set(selected.value)
    if (event.shiftKey && anchorIndex.value !== null && anchorIndex.value !== index) {
      const from = Math.min(anchorIndex.value, index)
      const to = Math.max(anchorIndex.value, index)
      for (let i = from; i <= to; i++) {
        const row = rows[i]
        if (!row) continue
        if (wantSelected) next.add(row.id)
        else next.delete(row.id)
      }
    } else {
      if (wantSelected) next.add(clicked.id)
      else next.delete(clicked.id)
    }
    selected.value = next
    anchorIndex.value = index
  }

  function resetAnchor() {
    anchorIndex.value = null
  }

  return { onCheckboxClick, resetAnchor, anchorIndex }
}
