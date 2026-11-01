import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import JournalEntriesView from '@/views/admin/JournalEntriesView.vue'

// Two synthetic bilags: one booked by a person, one by the system actor.
const { fetchApi, icon } = vi.hoisted(() => ({
  fetchApi: vi.fn(),
  icon: { template: '<span />' },
}))

vi.mock('lucide-vue-next', () => ({
  Plus: icon, ChevronDown: icon, ChevronUp: icon, ChevronRight: icon, Send: icon, Ban: icon,
  Trash2: icon, RefreshCw: icon, Lock: icon, Unlock: icon, Search: icon, Paperclip: icon,
}))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp: vi.fn() }) }))
vi.mock('@/composables/useCan', () => ({ useCan: () => ({ can: () => true }) }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => vi.fn(), useConfirmStore: () => ({ checklistValues: [] }) }))
vi.mock('@tanstack/vue-query', async () => {
  const { ref } = await import('vue')
  return {
    useQuery: (opts: { queryFn: () => Promise<unknown> }) => {
      const data = ref<unknown>(undefined)
      void opts.queryFn().then((r) => { data.value = r })
      return { data, isLoading: ref(false), isError: ref(false), error: ref(null), refetch: vi.fn() }
    },
    useMutation: () => ({ mutate: vi.fn(), isPending: ref(false) }),
    useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  }
})

const entry = (id: string, number: number, automatic: boolean) => ({
  id, club_id: 'c', fiscal_period_id: 'p1', entry_number: number, entry_date: '2026-04-01',
  description: `Synthetic bilag ${number}`, status: 'posted', source: 'manual', source_id: null,
  source_table: null, attachment_url: null, created_by: 'u', posted_by: 'u', posted_at: null,
  voided_by: null, voided_at: null, created_at: '2026-04-01T00:00:00Z', automatic,
  lines: [{
    id: `${id}-l`, journal_entry_id: id, account_id: 'a', account_code: '1920', account_name: 'Bank',
    account_type: 'asset', debit: 200, credit: 0, description: '', mva_amount: 0, mva_eligible: 'no',
  }],
})

function respond(url: string) {
  if (url.endsWith('/periods')) return Promise.resolve([{ id: 'p1', year: 2026, status: 'open' }])
  if (url.includes('/journal?')) return Promise.resolve([entry('e-person', 1, false), entry('e-system', 2, true)])
  return Promise.resolve([])
}

describe('JournalEntriesView automatic badge', () => {
  beforeEach(() => {
    fetchApi.mockReset()
    fetchApi.mockImplementation(respond)
  })

  it('marks only the system-booked bilag, in the list row and in the opened detail', async () => {
    const wrapper = mountWithPlugins(JournalEntriesView)
    await flushPromises()

    const rows = wrapper.findAll('tbody > tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].find('[data-testid="automatic-entry-badge"]').exists()).toBe(false)
    expect(rows[1].find('[data-testid="automatic-entry-badge"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="automatic-entry-badge"]')).toHaveLength(1)

    // Opening the person's bilag adds no detail badge; opening the system's
    // adds one inside the detail row.
    await rows[0].trigger('click')
    expect(wrapper.findAll('[data-testid="automatic-entry-badge"]')).toHaveLength(1)
    expect(wrapper.find('td[colspan="9"] [data-testid="automatic-entry-badge"]').exists()).toBe(false)
    await rows[1].trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="automatic-entry-badge"]')).toHaveLength(2)
    expect(wrapper.find('td[colspan="9"] [data-testid="automatic-entry-badge"]').exists()).toBe(true)
  })
})
