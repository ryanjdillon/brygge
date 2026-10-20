import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises, DOMWrapper } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import { useConfirmStore } from '@/stores/confirm'
import { vBackdropClose } from '@/directives/backdropClose'

vi.mock('lucide-vue-next', () => {
  const icon = { template: '<span />' }
  return { AlertTriangle: icon, AlertCircle: icon, Info: icon, X: icon }
})

function mountDialog() {
  const wrapper = mountWithPlugins(ConfirmDialog, {
    attachTo: document.body,
    // Registered globally in main.ts; Modal uses it.
    global: { directives: { 'backdrop-close': vBackdropClose } },
    // The real ask/settle actions: the default spy factory drops the action.
    piniaOptions: { stubActions: false, createSpy: (fn?: (...args: unknown[]) => unknown) => vi.fn(fn) },
  })
  return { wrapper, store: useConfirmStore() }
}

// The dialog teleports to <body>.
const body = () => new DOMWrapper(document.body)

describe('ConfirmDialog checklist', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('renders one checkbox per item, ticked unless the item says otherwise', async () => {
    const { wrapper, store } = mountDialog()
    const answer = store.ask({
      title: 'Synthetic title',
      body: 'Synthetic body',
      checklist: {
        label: 'Pick rows',
        items: [
          { value: 'a', label: 'Row A' },
          { value: 'b', label: 'Row B' },
          { value: 'c', label: 'Row C', default: false },
        ],
      },
    })
    await flushPromises()

    const list = body().get('[data-testid="confirm-checklist"]')
    expect(list.text()).toContain('Pick rows')
    const boxes = list.findAll('input[type="checkbox"]')
    expect(boxes.map((b) => (b.element as HTMLInputElement).checked)).toEqual([true, true, false])
    expect(store.checklistValues).toEqual(['a', 'b'])

    await boxes[0]!.setValue(false)
    await boxes[2]!.setValue(true)
    expect([...store.checklistValues].sort()).toEqual(['b', 'c'])

    store.settle(true)
    expect(await answer).toBe(true)
    wrapper.unmount()
  })

  it('shows no checklist when none is asked for, and resets the values', async () => {
    const { wrapper, store } = mountDialog()
    store.checklistValues = ['stale']
    void store.ask({ title: 'Plain', body: 'No list' })
    await flushPromises()
    expect(body().find('[data-testid="confirm-checklist"]').exists()).toBe(false)
    expect(store.checklistValues).toEqual([])
    store.settle(false)
    wrapper.unmount()
  })
})
