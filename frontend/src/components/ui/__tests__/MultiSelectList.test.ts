import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import MultiSelectList from '@/components/ui/MultiSelectList.vue'

const options = [
  { value: 'a', label: 'Alpha', hint: 'first' },
  { value: 'b', label: 'Beta', meta: 'In group: X', bulkSkip: true },
  { value: 'c', label: 'Gamma', disabled: true },
  { value: 'd', label: 'Delta' },
]

function mountList(modelValue: string[]) {
  return mount(MultiSelectList, { props: { modelValue, options } })
}

const toggleAll = (w: ReturnType<typeof mountList>) => w.find('[data-testid="multi-select-toggle-all"]')
const lastEmit = (w: ReturnType<typeof mountList>) => {
  const all = w.emitted('update:modelValue') as string[][][]
  return all[all.length - 1][0]
}

describe('MultiSelectList', () => {
  it('names its controls with translations, not English literals', () => {
    const w = mountList([])
    expect(toggleAll(w).text()).toBe('common.selectAll')
    expect(w.find('input[type="search"]').attributes('placeholder')).toBe('common.search')
    expect(mountList(['a', 'd']).find('[data-testid="multi-select-toggle-all"]').text()).toBe('common.deselectAll')
  })

  it('makes the toggle, the search clear and every row at least 44px', async () => {
    const w = mountList([])
    expect(toggleAll(w).classes()).toEqual(expect.arrayContaining(['min-h-11', 'min-w-11']))
    expect(w.find('input[type="search"]').classes()).toContain('min-h-11')
    for (const label of w.findAll('li label')) expect(label.classes()).toContain('min-h-11')
    await w.find('input[type="search"]').setValue('al')
    expect(w.find('button[aria-label="common.clearSearch"]').classes()).toEqual(expect.arrayContaining(['min-h-11', 'min-w-11']))
  })

  it('selects all adds neither disabled options nor options marked bulkSkip', async () => {
    const w = mountList([])
    await toggleAll(w).trigger('click')
    expect(lastEmit(w).sort()).toEqual(['a', 'd'])
  })

  it('still lets a bulkSkip option be ticked on its own', async () => {
    const w = mountList([])
    const beta = w.findAll('input[type="checkbox"]')[1]
    await beta.trigger('change')
    expect(lastEmit(w)).toEqual(['b'])
  })

  it('is "all selected" when every option it would add is, and then clears the shown ones', async () => {
    const w = mountList(['a', 'd', 'b'])
    expect(toggleAll(w).text()).toBe('common.deselectAll')
    await toggleAll(w).trigger('click')
    expect(lastEmit(w)).toEqual([])
  })

  it('has no toggle when nothing could be added in bulk', () => {
    const w = mount(MultiSelectList, { props: { modelValue: [], options: [{ value: 'b', label: 'Beta', bulkSkip: true }] } })
    expect(w.find('[data-testid="multi-select-toggle-all"]').exists()).toBe(false)
  })

  it('shows the hint and the meta of an option', () => {
    const w = mountList([])
    expect(w.text()).toContain('first')
    expect(w.text()).toContain('In group: X')
  })

  it('falls back to a translated empty text', () => {
    const w = mount(MultiSelectList, { props: { modelValue: [], options: [] } })
    expect(w.text()).toContain('common.noResults')
  })
})
