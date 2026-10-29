import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import WaitingListAdminView from '@/views/admin/WaitingListAdminView.vue'

const ensureFreshTotp = vi.fn()
const totpAwareFetch = vi.fn()
const clientGet = vi.fn()
let queryFn: (() => Promise<unknown>) | undefined

// The global setup stubs vue-query inertly; this view's behaviour is in its
// query and mutation functions, so run them for real.
vi.mock('@tanstack/vue-query', () => ({
  useQuery: (opts: { queryFn: () => Promise<unknown> }) => {
    queryFn = opts.queryFn
    return {
      data: ref({ items: entries, has_more: false, limit: 100, offset: 0 }),
      isLoading: ref(false),
      isError: ref(false),
    }
  },
  useMutation: (opts: {
    mutationFn: (v: unknown) => Promise<unknown>
    onSuccess?: () => void
    onError?: (e: unknown) => void
    onSettled?: () => void
  }) => ({
    mutate: (v: unknown) => {
      opts.mutationFn(v).then(() => opts.onSuccess?.(), (e) => opts.onError?.(e)).finally(() => opts.onSettled?.())
    },
    isPending: ref(false),
  }),
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}))

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({
    ensureFreshTotp: () => ensureFreshTotp(),
    totpAwareFetch: (...args: unknown[]) => totpAwareFetch(...args),
  }),
}))

vi.mock('@/composables/useApi', () => ({
  useApi: () => ({ fetchApi: vi.fn().mockResolvedValue({ users: [] }) }),
}))

vi.mock('@/lib/apiClient', () => ({
  useApiClient: () => ({ GET: (...args: unknown[]) => clientGet(...args) }),
  unwrap: (res: { data: unknown }) => res.data,
}))

vi.mock('@/components/admin/SortableTh.vue', () => ({ default: { template: '<th><slot /></th>' } }))

const entries = [
  { id: 'e1', position: 1, full_name: 'Test One', email: 'one@example.test', status: 'active', created_at: '2026-01-01T00:00:00Z' },
  { id: 'e2', position: 2, full_name: 'Test Two', email: 'two@example.test', status: 'active', created_at: '2026-01-02T00:00:00Z' },
]

describe('WaitingListAdminView step-up (BRY-477)', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset().mockResolvedValue(true)
    totpAwareFetch.mockReset().mockResolvedValue({ ok: true })
    clientGet.mockReset().mockResolvedValue({ data: { items: entries, has_more: false, limit: 100, offset: 0 } })
    document.body.innerHTML = ''
  })

  it('reads the list from the admin path', async () => {
    mountWithPlugins(WaitingListAdminView)
    await flushPromises()
    await queryFn?.()
    expect(clientGet).toHaveBeenCalledWith('/api/v1/admin/waiting-list', expect.anything())
  })

  it('steps up when Add member is clicked, before the search modal opens', async () => {
    const wrapper = mountWithPlugins(WaitingListAdminView, { attachTo: document.body })
    await flushPromises()
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(document.body.querySelector('input[type="search"]')).not.toBeNull()
  })

  it('does not open the modal when the step-up is cancelled', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(WaitingListAdminView, { attachTo: document.body })
    await flushPromises()
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('input[type="search"]')).toBeNull()
  })

  it('steps up on drop and then reorders through the admin path', async () => {
    const wrapper = mountWithPlugins(WaitingListAdminView)
    await flushPromises()
    const rows = wrapper.findAll('tbody tr')
    await rows[1].trigger('drop', { dataTransfer: { getData: () => 'e1' } })
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(totpAwareFetch).toHaveBeenCalledTimes(1)
    expect(totpAwareFetch.mock.calls[0][0]).toBe('/api/v1/admin/waiting-list/e1/position')
  })

  it('does not reorder when the step-up is cancelled', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(WaitingListAdminView)
    await flushPromises()
    const rows = wrapper.findAll('tbody tr')
    await rows[1].trigger('drop', { dataTransfer: { getData: () => 'e1' } })
    await flushPromises()
    expect(totpAwareFetch).not.toHaveBeenCalled()
  })
})
