import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import JournalEntriesView from '@/views/admin/JournalEntriesView.vue'
import { ApiError } from '@/lib/errors'

// Synthetic periods: which one the view opens on depends on the flags.
const { ensureFreshTotp, fetchApi, confirm, icon, state } = vi.hoisted(() => ({
  ensureFreshTotp: vi.fn(),
  fetchApi: vi.fn(),
  confirm: vi.fn(),
  icon: { template: '<span />' },
  state: { periods: [] as Record<string, unknown>[], can: true },
}))

vi.mock('lucide-vue-next', () => ({
  Plus: icon, ChevronDown: icon, ChevronUp: icon, ChevronRight: icon, Send: icon, Ban: icon,
  Trash2: icon, RefreshCw: icon, Lock: icon, Unlock: icon, Search: icon, Paperclip: icon,
}))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useCan', () => ({ useCan: () => ({ can: () => state.can }) }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => confirm, useConfirmStore: () => ({ checklistValues: [] }) }))
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

const period = (over: Record<string, unknown>) => ({
  id: 'p', year: 2025, status: 'open', reference_only: false, reference_blocked: null, open_invoice_bookings: 0, ...over,
})

function respond(url: string, opts?: { method?: string }) {
  if (url.endsWith('/periods')) return Promise.resolve(state.periods)
  if (url.endsWith('/reference') && opts?.method === 'POST') return Promise.resolve({})
  return Promise.resolve([])
}

const marks = () => fetchApi.mock.calls.filter(([url]) => String(url).endsWith('/reference'))

describe('JournalEntriesView reference years', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset()
    fetchApi.mockReset()
    confirm.mockReset()
    fetchApi.mockImplementation(respond)
    confirm.mockResolvedValue(true)
    ensureFreshTotp.mockResolvedValue(true)
    state.can = true
    state.periods = [period({ id: 'p26', year: 2026 })]
  })

  it('opens on the first open year that is not a reference year', async () => {
    state.periods = [
      period({ id: 'ref', year: 2026, reference_only: true }),
      period({ id: 'live', year: 2025 }),
    ]
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    expect(wrapper.find('[data-testid="reference-chip"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="toggle-reference"]').text()).toBe('admin.accounting.periods.markReference')
  })

  it('marks a reference year with a chip and drops the buttons that would be refused', async () => {
    state.periods = [period({ id: 'ref', year: 2025, reference_only: true })]
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()

    expect(wrapper.get('[data-testid="reference-chip"]').text()).toBe('admin.accounting.periods.reference')
    expect(wrapper.get('[data-testid="reference-note"]').text()).toBe('admin.accounting.referencePeriod')
    expect(wrapper.get('[data-testid="toggle-reference"]').text()).toBe('admin.accounting.periods.clearReference')
    expect(wrapper.find('[data-testid="resplit-invoice-bilags"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="post-vipps-drafts"]').exists()).toBe(false)
    // Avstem periode is the third booking button; only Lukk and the toggle remain.
    expect(wrapper.text()).not.toContain('admin.accounting.journal.updatePeriod')
  })

  it('asks for a fresh TOTP at click time, then confirms, then posts', async () => {
    state.periods = [period({ id: 'p25', year: 2025, open_invoice_bookings: 2 })]
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()

    ensureFreshTotp.mockResolvedValue(false)
    await wrapper.get('[data-testid="toggle-reference"]').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    expect(confirm).not.toHaveBeenCalled()
    expect(marks()).toEqual([])

    ensureFreshTotp.mockResolvedValue(true)
    await wrapper.get('[data-testid="toggle-reference"]').trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledOnce()
    const asked = confirm.mock.calls[0][0] as { title: string; body: string }
    expect(asked.title).toBe('admin.accounting.periods.markReferenceTitle')
    expect(asked.body).toContain('admin.accounting.periods.markReferenceBody')
    expect(asked.body).toContain('admin.accounting.periods.markReferenceInvoices')
    expect(ensureFreshTotp.mock.invocationCallOrder[1]).toBeLessThan(confirm.mock.invocationCallOrder[0])
    expect(marks()).toEqual([
      ['/api/v1/admin/accounting/periods/p25/reference', expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ reference_only: true }),
      })],
    ])
  })

  it('posts nothing when the confirm is declined', async () => {
    state.periods = [period({ id: 'p25', year: 2025 })]
    confirm.mockResolvedValue(false)
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    await wrapper.get('[data-testid="toggle-reference"]').trigger('click')
    await flushPromises()
    expect(marks()).toEqual([])
  })

  it('disables the toggle and says why when the server blocks it', async () => {
    state.periods = [period({ id: 'p26', year: 2026, reference_blocked: 'current_year' })]
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    expect(wrapper.get('[data-testid="toggle-reference"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="reference-blocked"]').text()).toBe(
      'admin.accounting.periods.referenceBlocked.current_year',
    )
  })

  it('offers the toggle only to those who may book', async () => {
    state.can = false
    state.periods = [period({ id: 'p25', year: 2025 })]
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    expect(wrapper.find('[data-testid="toggle-reference"]').exists()).toBe(false)
  })

  it('reports a failed toggle in the page', async () => {
    state.periods = [period({ id: 'p25', year: 2025 })]
    fetchApi.mockImplementation((url: string, opts?: { method?: string }) =>
      url.endsWith('/reference') && opts?.method === 'POST'
        ? Promise.reject(new ApiError(409, 'inneværende år', 'reference_current_year'))
        : respond(url, opts),
    )
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()
    await wrapper.get('[data-testid="toggle-reference"]').trigger('click')
    await flushPromises()
    // The server's refusal is worded from the viewer's own locale keys.
    expect(wrapper.text()).toContain('common.error: admin.accounting.periods.referenceBlocked.current_year')
    expect(wrapper.text()).not.toContain('inneværende år')
  })
})
