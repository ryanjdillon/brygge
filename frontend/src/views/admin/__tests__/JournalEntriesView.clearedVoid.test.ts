import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import JournalEntriesView from '@/views/admin/JournalEntriesView.vue'
import { ApiError } from '@/lib/errors'

// A Vipps charge entry whose settlement is already cleared: the ledger
// refuses its void, the view offers to undo the clearing, and voids again.
const { ensureFreshTotp, fetchApi, icon, askConfirm } = vi.hoisted(() => ({
  ensureFreshTotp: vi.fn(),
  fetchApi: vi.fn(),
  askConfirm: vi.fn(),
  icon: { template: '<span />' },
}))

vi.mock('lucide-vue-next', () => ({
  Plus: icon, ChevronDown: icon, ChevronUp: icon, ChevronRight: icon, Send: icon, Ban: icon,
  Trash2: icon, RefreshCw: icon, Lock: icon, Unlock: icon, Search: icon, Paperclip: icon,
}))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useCan', () => ({ useCan: () => ({ can: () => true }) }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => askConfirm, useConfirmStore: () => ({ checklistValues: [] }) }))
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

const ENTRY = {
  id: 'entry-1', entry_number: 12, entry_date: '2026-06-15', description: 'Vipps innbetaling',
  status: 'posted', source: 'vipps', source_table: 'vipps_import_rows', lines: [],
}

let voidAttempts = 0
let clearingUndone = false

function respond(url: string) {
  if (url.endsWith('/periods')) return Promise.resolve([{ id: 'p1', year: 2026, status: 'open' }])
  if (url.includes('/journal?')) return Promise.resolve([ENTRY])
  if (url.endsWith('/bank-rows/bank-1/unassign')) {
    clearingUndone = true
    return Promise.resolve({ status: 'unassigned' })
  }
  if (url.endsWith('/journal/entry-1/void')) {
    voidAttempts++
    if (!clearingUndone) {
      return Promise.reject(new ApiError(409, 'refused', 'charge_in_cleared_settlement', {
        settlement_number: '3000101', msn: '000001', bank_row_id: 'bank-1',
      }))
    }
    return Promise.resolve({ id: 'reversal-1' })
  }
  return Promise.resolve([])
}

describe('JournalEntriesView void of a charge in a cleared settlement', () => {
  beforeEach(() => {
    voidAttempts = 0
    clearingUndone = false
    ensureFreshTotp.mockReset().mockResolvedValue(true)
    askConfirm.mockReset().mockResolvedValue(true)
    fetchApi.mockReset().mockImplementation(respond)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })
  afterEach(() => vi.restoreAllMocks())

  it('offers to undo the clearing, then voids the entry again', async () => {
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    await wrapper.get('[data-testid="void-entry-entry-1"]').trigger('click')
    await flushPromises()
    expect(voidAttempts).toBe(1)
    expect(wrapper.get('[data-testid="cleared-settlement-refusal"]').text()).toContain('admin.accounting.clearedSettlement.refused')

    await wrapper.get('[data-testid="cleared-settlement-undo"]').trigger('click')
    await flushPromises()
    const undo = fetchApi.mock.calls.find(([url]) => String(url).endsWith('/bank-rows/bank-1/unassign'))
    expect(undo?.[1]).toMatchObject({ method: 'POST', body: JSON.stringify({ confirm: true }) })
    expect(voidAttempts).toBe(2)
    expect(wrapper.find('[data-testid="cleared-settlement-refusal"]').exists()).toBe(false)
  })
})
