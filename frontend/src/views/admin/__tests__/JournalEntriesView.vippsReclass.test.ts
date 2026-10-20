import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import JournalEntriesView from '@/views/admin/JournalEntriesView.vue'

// Two synthetic parked drafts the backlog preview would rebook; the
// treasurer unticks one in the confirm dialog's checklist.
const { ensureFreshTotp, fetchApi, confirm, confirmStore, icon } = vi.hoisted(() => ({
  ensureFreshTotp: vi.fn(),
  fetchApi: vi.fn(),
  confirm: vi.fn(),
  confirmStore: { checklistValues: [] as string[] },
  icon: { template: '<span />' },
}))

vi.mock('lucide-vue-next', () => ({
  Plus: icon, ChevronDown: icon, ChevronUp: icon, ChevronRight: icon, Send: icon, Ban: icon,
  Trash2: icon, RefreshCw: icon, Lock: icon, Unlock: icon, Search: icon, Paperclip: icon,
}))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useCan', () => ({ useCan: () => ({ can: () => true }) }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => confirm, useConfirmStore: () => confirmStore }))
// The global setup stubs Vue Query to return nothing; this view needs its
// periods loaded and its mutations to reach fetchApi.
vi.mock('@tanstack/vue-query', async () => {
  const { ref } = await import('vue')
  type Handlers = { onSuccess?: (r: unknown) => void; onError?: (e: unknown) => void; onSettled?: () => void }
  return {
    useQuery: (opts: { queryFn: () => Promise<unknown> }) => {
      const data = ref<unknown>(undefined)
      void opts.queryFn().then((r) => { data.value = r })
      return { data, isLoading: ref(false), isError: ref(false), error: ref(null), refetch: vi.fn() }
    },
    useMutation: (opts: { mutationFn: (v: unknown) => Promise<unknown>; onSuccess?: (r: unknown) => void }) => ({
      mutate: (v: unknown, h: Handlers = {}) =>
        opts.mutationFn(v).then(
          (r) => { opts.onSuccess?.(r); h.onSuccess?.(r); h.onSettled?.() },
          (e) => { h.onError?.(e); h.onSettled?.() },
        ),
      isPending: ref(false),
    }),
    useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  }
})

const reclass = (n: number, amount: number, to: string) => ({
  entry_id: `entry-${n}`, row_id: `row-${n}`, entry_number: n, date: '2026-06-01',
  amount, from: '1500', to, rule: 'price_item',
})

const backlog = (over: Record<string, unknown> = {}) => ({
  year: 2026, applied: false, auto_assign_enabled: true, drafts: 2, posted: 0,
  posted_totals: [], reclassified: [reclass(7, 300, '3210'), reclass(8, 1600, '3300')],
  refused: [], releasable: [], unchecked: 0, stale_ids: [],
  ...over,
})

function respond(url: string) {
  if (url.endsWith('/periods')) return Promise.resolve([{ id: 'p1', year: 2026, status: 'open' }])
  if (url.endsWith('/sync/vipps-drafts/preview')) return Promise.resolve(backlog())
  if (url.endsWith('/sync/vipps-drafts')) {
    return Promise.resolve(backlog({ applied: true, reclassified: [reclass(8, 1600, '3300')], unchecked: 1 }))
  }
  return Promise.resolve([])
}

const applies = () => fetchApi.mock.calls.filter(([url]) => String(url).endsWith('/sync/vipps-drafts'))

describe('JournalEntriesView backlog per-row choice', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset()
    fetchApi.mockReset()
    confirm.mockReset()
    fetchApi.mockImplementation(respond)
    ensureFreshTotp.mockResolvedValue(true)
    // The treasurer unticks bilag 7 before confirming.
    confirm.mockImplementation(async () => {
      confirmStore.checklistValues = ['entry-8']
      return true
    })
  })

  it('offers one tick per rebooking and sends only the ticked entry ids', async () => {
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    await wrapper.get('[data-testid="post-vipps-drafts"]').trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    const opts = confirm.mock.calls[0]![0]
    expect(opts.checklist.items).toEqual([
      { value: 'entry-7', label: expect.stringContaining('1500 → 3210') },
      { value: 'entry-8', label: expect.stringContaining('1500 → 3300') },
    ])
    expect(applies()).toEqual([
      ['/api/v1/admin/accounting/sync/vipps-drafts', expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ period_id: 'p1', reclassify_entry_ids: ['entry-8'] }),
      })],
    ])
  })

  it('cancelling posts nothing', async () => {
    confirm.mockResolvedValue(false)
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    await wrapper.get('[data-testid="post-vipps-drafts"]').trigger('click')
    await flushPromises()
    expect(applies()).toEqual([])
  })
})
