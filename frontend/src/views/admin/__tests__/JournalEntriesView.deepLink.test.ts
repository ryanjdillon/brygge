import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import JournalEntriesView from '@/views/admin/JournalEntriesView.vue'

// BRY-575: the treasurer's e-mail about payments waiting for a fiscal year
// links to Bilag with ?openPeriod=<year>, ?reopenPeriod=<id> or ?period=<id>.
// Synthetic periods only.
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
  id: 'p', year: 2026, status: 'open', reference_only: false, reference_blocked: null, open_invoice_bookings: 0, ...over,
})

function respond(url: string, opts?: { method?: string; body?: string }) {
  if (url.endsWith('/periods') && opts?.method === 'POST') {
    const { year } = JSON.parse(opts.body ?? '{}') as { year: number }
    return Promise.resolve(period({ id: `new-${year}`, year }))
  }
  if (url.endsWith('/periods')) return Promise.resolve(state.periods)
  if (url.endsWith('/reopen') && opts?.method === 'POST') return Promise.resolve({ message: 'ok' })
  return Promise.resolve([])
}

const posts = (suffix: string) =>
  fetchApi.mock.calls.filter(([url, opts]) => String(url).endsWith(suffix) && (opts as { method?: string })?.method === 'POST')

async function mountAt(route: string) {
  const wrapper = mountWithPlugins(JournalEntriesView, { initialRoute: route })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('JournalEntriesView deep links from the fiscal year notice', () => {
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

  it('opens the create confirm prefilled with the linked year, not the next free one', async () => {
    // The next free year is 2027; the link asks for 2028.
    const wrapper = await mountAt('/?openPeriod=2028&tab=x')
    expect(confirm).toHaveBeenCalledOnce()
    expect(confirm.mock.calls[0][0]).toMatchObject({
      title: 'admin.accounting.journal.createPeriodConfirmTitle',
      confirmLabel: 'admin.accounting.journal.createPeriod',
    })
    expect(posts('/periods')).toEqual([
      ['/api/v1/admin/accounting/periods', expect.objectContaining({ body: JSON.stringify({ year: 2028 }) })],
    ])
    // The deep-link keys are dropped; anything else in the query stays.
    expect(wrapper.vm.$route.query).toEqual({ tab: 'x' })
  })

  it('creates nothing when the confirm is declined, and still drops the query', async () => {
    confirm.mockResolvedValue(false)
    const wrapper = await mountAt('/?openPeriod=2027')
    expect(confirm).toHaveBeenCalledOnce()
    expect(posts('/periods')).toEqual([])
    expect(wrapper.vm.$route.query).toEqual({})
  })

  it('selects a linked year that already exists instead of creating it', async () => {
    state.periods = [period({ id: 'p26', year: 2026 }), period({ id: 'p25', year: 2025, status: 'closed' })]
    await mountAt('/?openPeriod=2025')
    expect(confirm).not.toHaveBeenCalled()
    expect(posts('/periods')).toEqual([])
  })

  it('shows no dialog to a user without the ledger role', async () => {
    state.can = false
    const wrapper = await mountAt('/?openPeriod=2027')
    expect(confirm).not.toHaveBeenCalled()
    expect(ensureFreshTotp).not.toHaveBeenCalled()
    expect(posts('/periods')).toEqual([])
    expect(wrapper.vm.$route.query).toEqual({})
  })

  it('asks for a fresh TOTP before the reopen confirm, then reopens the linked period', async () => {
    state.periods = [period({ id: 'p26', year: 2026 }), period({ id: 'p25', year: 2025, status: 'closed' })]
    const wrapper = await mountAt('/?reopenPeriod=p25')
    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    expect(confirm).toHaveBeenCalledOnce()
    expect(ensureFreshTotp.mock.invocationCallOrder[0]).toBeLessThan(confirm.mock.invocationCallOrder[0])
    expect(confirm.mock.calls[0][0]).toMatchObject({ title: 'admin.accounting.journal.reopenConfirmTitle' })
    expect(posts('/reopen').map(([url]) => url)).toEqual(['/api/v1/admin/accounting/periods/p25/reopen'])
    expect(wrapper.vm.$route.query).toEqual({})
  })

  it('does nothing for a reopen link that points at a locked period', async () => {
    state.periods = [period({ id: 'p26', year: 2026 }), period({ id: 'p25', year: 2025, status: 'locked' })]
    const wrapper = await mountAt('/?reopenPeriod=p25')
    expect(ensureFreshTotp).not.toHaveBeenCalled()
    expect(confirm).not.toHaveBeenCalled()
    expect(posts('/reopen')).toEqual([])
    expect(wrapper.vm.$route.query).toEqual({})
    // The locked period is selected and labelled as locked; no reopen button.
    expect(wrapper.text()).toContain('admin.accounting.periods.locked')
    expect(wrapper.find('[data-testid="reopen-period"]').exists()).toBe(false)
  })

  it('only selects the period for ?period=<id> (the locked-year link)', async () => {
    state.periods = [period({ id: 'p26', year: 2026 }), period({ id: 'p24', year: 2024, status: 'locked' })]
    const wrapper = await mountAt('/?period=p24')
    expect(ensureFreshTotp).not.toHaveBeenCalled()
    expect(confirm).not.toHaveBeenCalled()
    expect(fetchApi.mock.calls.some(([, o]) => (o as { method?: string })?.method === 'POST')).toBe(false)
    expect(wrapper.text()).toContain('admin.accounting.periods.locked')
    expect(wrapper.vm.$route.query).toEqual({})
  })

  it('opens the create confirm from the empty state when the club has no periods yet', async () => {
    state.periods = []
    const wrapper = await mountAt('/?openPeriod=2027')
    expect(confirm).toHaveBeenCalledOnce()
    expect(posts('/periods')).toEqual([
      ['/api/v1/admin/accounting/periods', expect.objectContaining({ body: JSON.stringify({ year: 2027 }) })],
    ])
    expect(wrapper.vm.$route.query).toEqual({})
  })

  it('reopens nothing when the step-up is refused', async () => {
    state.periods = [period({ id: 'p25', year: 2025, status: 'closed' })]
    ensureFreshTotp.mockResolvedValue(false)
    await mountAt('/?reopenPeriod=p25')
    expect(confirm).not.toHaveBeenCalled()
    expect(posts('/reopen')).toEqual([])
  })
})

describe('JournalEntriesView period buttons', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset()
    fetchApi.mockReset()
    confirm.mockReset()
    fetchApi.mockImplementation(respond)
    confirm.mockResolvedValue(true)
    ensureFreshTotp.mockResolvedValue(true)
    state.can = true
  })

  it('the reopen button asks for a fresh TOTP at click time, before the confirm', async () => {
    state.periods = [period({ id: 'p25', year: 2025, status: 'closed' })]
    const wrapper = await mountAt('/')
    ensureFreshTotp.mockResolvedValue(false)
    await wrapper.get('[data-testid="reopen-period"]').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    expect(confirm).not.toHaveBeenCalled()

    ensureFreshTotp.mockResolvedValue(true)
    await wrapper.get('[data-testid="reopen-period"]').trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledOnce()
    expect(ensureFreshTotp.mock.invocationCallOrder[1]).toBeLessThan(confirm.mock.invocationCallOrder[0])
    expect(posts('/reopen').map(([url]) => url)).toEqual(['/api/v1/admin/accounting/periods/p25/reopen'])
  })

  it('both create buttons confirm the year first', async () => {
    state.periods = []
    let wrapper = await mountAt('/')
    confirm.mockResolvedValue(false)
    await wrapper.get('[data-testid="create-period-empty"]').trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledOnce()
    expect(posts('/periods')).toEqual([])

    confirm.mockReset()
    confirm.mockResolvedValue(true)
    state.periods = [period({ id: 'p26', year: 2026 })]
    wrapper = await mountAt('/')
    // First click shows the year input, the second asks to confirm.
    await wrapper.get('[data-testid="create-period"]').trigger('click')
    await flushPromises()
    expect(confirm).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="create-period"]').trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledOnce()
    expect(posts('/periods')).toEqual([
      ['/api/v1/admin/accounting/periods', expect.objectContaining({ body: JSON.stringify({ year: 2027 }) })],
    ])
  })
})
