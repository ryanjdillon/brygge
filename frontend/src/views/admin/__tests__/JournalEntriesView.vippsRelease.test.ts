import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import JournalEntriesView from '@/views/admin/JournalEntriesView.vue'

// One synthetic 1500-parked draft the backlog preview refused but offers for
// release.
const { ensureFreshTotp, fetchApi, confirm, icon } = vi.hoisted(() => ({
  ensureFreshTotp: vi.fn(),
  fetchApi: vi.fn(),
  confirm: vi.fn(),
  icon: { template: '<span />' },
}))

vi.mock('lucide-vue-next', () => ({
  Plus: icon, ChevronDown: icon, ChevronUp: icon, ChevronRight: icon, Send: icon, Ban: icon,
  Trash2: icon, RefreshCw: icon, Lock: icon, Unlock: icon, Search: icon, Paperclip: icon,
}))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useCan', () => ({ useCan: () => ({ can: () => true }) }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => confirm }))
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

const ROW = 'row-1'

function respond(url: string) {
  if (url.endsWith('/periods')) return Promise.resolve([{ id: 'p1', year: 2026, status: 'open' }])
  if (url.endsWith('/sync/vipps-drafts/preview')) {
    return Promise.resolve({
      year: 2026, applied: false, auto_assign_enabled: true, drafts: 1, posted: 0,
      posted_totals: [], reclassified: [],
      refused: [{ ref: 'bilag 7', reason: 'receivable credit with no recorded faktura' }],
      releasable: [{ ref: 'bilag 7', row_id: ROW, entry_number: 7, date: '2026-06-01', amount: 800 }],
    })
  }
  if (url.endsWith(`/vipps-charges/${ROW}/unassign`)) return Promise.resolve({ status: 'unassigned' })
  return Promise.resolve([])
}

const releases = () => fetchApi.mock.calls.filter(([url]) => String(url).endsWith('/unassign'))

describe('JournalEntriesView backlog release', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset()
    fetchApi.mockReset()
    confirm.mockReset()
    fetchApi.mockImplementation(respond)
    confirm.mockResolvedValue(true)
  })

  it('asks for a fresh TOTP at click time and releases through the flagged unassign', async () => {
    ensureFreshTotp.mockResolvedValue(true)
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    await wrapper.get('[data-testid="post-vipps-drafts"]').trigger('click')
    await flushPromises()

    const list = wrapper.get('[data-testid="vipps-releasable"]')
    expect(list.text()).toContain('bilag 7')
    ensureFreshTotp.mockClear()

    ensureFreshTotp.mockResolvedValue(false)
    await list.get('button').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    expect(confirm).not.toHaveBeenCalled()
    expect(releases()).toEqual([])

    ensureFreshTotp.mockResolvedValue(true)
    await list.get('button').trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledOnce()
    expect(releases()).toEqual([
      [`/api/v1/admin/accounting/vipps-charges/${ROW}/unassign`, expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ released_from_backlog: true }),
      })],
    ])
    expect(wrapper.find('[data-testid="vipps-releasable"]').exists()).toBe(false)
  })
})
