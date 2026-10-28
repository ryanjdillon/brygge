import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import JournalEntriesView from '@/views/admin/JournalEntriesView.vue'
import { ApiError } from '@/lib/errors'

// A posted entry booked from a Stripe row or a bank row: the ledger
// refuses its void and the view points to where it is undone.
const { ensureFreshTotp, fetchApi, icon, refusalCode } = vi.hoisted(() => ({
  refusalCode: { value: 'stripe_entry' },
  ensureFreshTotp: vi.fn(),
  fetchApi: vi.fn(),
  icon: { template: '<span />' },
}))

vi.mock('lucide-vue-next', () => ({
  Plus: icon, ChevronDown: icon, ChevronUp: icon, ChevronRight: icon, Send: icon, Ban: icon,
  Trash2: icon, RefreshCw: icon, Lock: icon, Unlock: icon, Search: icon, Paperclip: icon,
}))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useCan', () => ({ useCan: () => ({ can: () => true }) }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => vi.fn(), useConfirmStore: () => ({ checklistValues: [] }) }))
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
  id: 'entry-1', entry_number: 12, entry_date: '2026-06-15', description: 'Kortbetaling ordre 0a1b2c3d',
  status: 'posted', source: 'stripe', source_table: 'stripe_balance_transactions', lines: [],
}

function respond(url: string) {
  if (url.endsWith('/periods')) return Promise.resolve([{ id: 'p1', year: 2026, status: 'open' }])
  if (url.includes('/journal?')) return Promise.resolve([ENTRY])
  if (url.endsWith('/journal/entry-1/void')) {
    return Promise.reject(new ApiError(409, 'refused', refusalCode.value, { reason: 'x' }))
  }
  return Promise.resolve([])
}

describe('JournalEntriesView void of a Stripe entry', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset().mockResolvedValue(true)
    fetchApi.mockReset().mockImplementation(respond)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })
  afterEach(() => vi.restoreAllMocks())

  it.each([
    ['stripe_entry', 'admin.accounting.journal.voidStripeEntry', '/admin/accounting/bank-imports?stripe=booked'],
    ['bank_entry', 'admin.accounting.journal.voidBankEntry', '/admin/accounting/bank-imports?tab=reconcile'],
  ])('points to where a %s refusal is undone', async (code, text, href) => {
    refusalCode.value = code
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    await wrapper.get('[data-testid="void-entry-entry-1"]').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    const note = wrapper.get(`[data-testid="void-refused-${code}"]`)
    expect(note.text()).toContain(text)
    expect(note.get('a').attributes('href')).toBe(href)
  })

  it('voids nothing without the step-up', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    await wrapper.get('[data-testid="void-entry-entry-1"]').trigger('click')
    await flushPromises()
    expect(fetchApi.mock.calls.some(([url]) => String(url).endsWith('/void'))).toBe(false)
  })
})
