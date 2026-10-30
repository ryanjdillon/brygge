import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import VippsFallbackReclass from '@/components/admin/VippsFallbackReclass.vue'
import { ApiError } from '@/lib/errors'

const { ensureFreshTotp, fetchApi, confirm, icon } = vi.hoisted(() => ({
  ensureFreshTotp: vi.fn(),
  fetchApi: vi.fn(),
  confirm: vi.fn(),
  icon: { template: '<span />' },
}))

vi.mock('lucide-vue-next', () => ({ RefreshCw: icon }))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => confirm }))
vi.mock('@tanstack/vue-query', async () => {
  const { ref } = await import('vue')
  type Handlers = { onSuccess?: (r: unknown) => void; onError?: (e: unknown) => void }
  return {
    useMutation: (opts: { mutationFn: (v: unknown) => Promise<unknown> }) => ({
      mutate: (v: unknown, h: Handlers = {}) =>
        opts.mutationFn(v).then((r) => h.onSuccess?.(r), (e) => h.onError?.(e)),
      isPending: ref(false),
    }),
    useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  }
})

const base = {
  applied: false,
  auto_assign_enabled: true,
  from_account: '3900',
  clearing_account: '2900',
  proposed: 1,
  totals: [{ account_code: '3200', count: 1, amount: 800 }],
  needs_tildel: 1,
  needs_tildel_amount: 450,
  stays: 1,
  stays_amount: 500,
  reclassified: 0,
  skipped: 0,
  failed: 0,
  stale_ids: [] as string[],
  closed_period_charges: 2,
  closed_period_amount: 300,
  items: [
    { entry_id: 'entry-1', row_id: 'row-1', entry_number: 11, date: '2026-05-11', amount: 800, from: '3900',
      status: 'reclassify', to: '3200', price_item: 'Gjesteplass synthetic' },
    { entry_id: 'entry-2', row_id: 'row-2', entry_number: 12, date: '2026-05-12', amount: 450, from: '3900',
      status: 'needs_tildel', invoice_number: 1234, reason_code: 'needs_tildel', reason: 'faktura 1234 matches' },
    { entry_id: 'entry-3', row_id: 'row-3', entry_number: 13, date: '2026-05-13', amount: 500, from: '3900',
      status: 'stays', reason_code: 'several_categories', reason: 'amount fits price items in several categories' },
  ],
}

const applied = {
  ...base,
  applied: true,
  reclassified: 1,
  items: base.items.map((i) =>
    i.entry_id === 'entry-1' ? { ...i, outcome: 'reclassified', new_entry_number: 90, reversal_entry_number: 89 } : i,
  ),
}

const openUrl = '/api/v1/admin/accounting/sync/vipps-fallback-reclass/preview'
const applyUrl = '/api/v1/admin/accounting/sync/vipps-fallback-reclass'
const applies = () => fetchApi.mock.calls.filter(([url]) => url === applyUrl)

async function opened() {
  const wrapper = mountWithPlugins(VippsFallbackReclass)
  await wrapper.get('[data-testid="vipps-fallback-reclass-open"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('VippsFallbackReclass', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset().mockResolvedValue(true)
    confirm.mockReset().mockResolvedValue(true)
    fetchApi.mockReset().mockImplementation((url: string) =>
      Promise.resolve(url === applyUrl ? applied : base),
    )
  })

  it('previews without a step-up and lists every charge, totals and the stays reasons', async () => {
    const wrapper = await opened()

    expect(ensureFreshTotp).not.toHaveBeenCalled()
    expect(fetchApi).toHaveBeenCalledWith(openUrl, expect.objectContaining({ method: 'POST' }))
    // One total card per target account (the i18n mock returns bare keys).
    expect(wrapper.findAll('[data-testid^="vipps-fallback-reclass-total-"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="vipps-fallback-reclass-total-3200"]').exists()).toBe(true)
    const table = wrapper.get('[data-testid="vipps-fallback-reclass-table"]')
    expect(table.get('[data-testid="vipps-fallback-reclass-row-entry-1"]').text()).toContain('3200 · Gjesteplass synthetic')
    expect(table.get('[data-testid="vipps-fallback-reclass-row-entry-2"]').text()).toContain('admin.accounting.vippsFallbackReclass.needsTildel')
    // The reason is translated from its code; the server's English text is not shown.
    const stays = table.get('[data-testid="vipps-fallback-reclass-row-entry-3"]').text()
    expect(stays).toContain('admin.accounting.vippsFallbackReclass.reason.several_categories')
    expect(stays).not.toContain('amount fits price items')
    expect(wrapper.text()).not.toContain('amount fits price items')
    expect(wrapper.text()).toContain('admin.accounting.vippsFallbackReclass.closedNote')
  })

  it('is table on wide screens and stacked cards below md', async () => {
    const wrapper = await opened()
    expect(wrapper.get('[data-testid="vipps-fallback-reclass-table"]').classes()).toEqual(expect.arrayContaining(['hidden', 'md:block']))
    const cards = wrapper.get('[data-testid="vipps-fallback-reclass-cards"]')
    expect(cards.classes()).toContain('md:hidden')
    expect(cards.findAll('li')).toHaveLength(3)
    // Touch targets are at least 44px (min-h-11).
    expect(wrapper.get('[data-testid="vipps-fallback-reclass-open"]').classes()).toContain('min-h-11')
    expect(wrapper.get('[data-testid="vipps-fallback-reclass-apply"]').classes()).toContain('min-h-11')
  })

  it('steps up at click time, confirms with counts and totals, then sends only the previewed targets', async () => {
    const wrapper = await opened()
    await wrapper.get('[data-testid="vipps-fallback-reclass-apply"]').trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    const opts = confirm.mock.calls[0]![0]
    expect(opts.title).toContain('admin.accounting.vippsFallbackReclass.confirmTitle')
    expect(opts.body).toContain('admin.accounting.vippsFallbackReclass.confirmBody')
    // One detail line per target account.
    expect(opts.details).toEqual(['admin.accounting.vippsFallbackReclass.totalLine'])
    expect(applies()).toEqual([
      [applyUrl, expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ targets: { 'entry-1': '3200' } }),
      })],
    ])
    // The result replaces the preview: the moved charge shows its new bilag.
    expect(wrapper.get('[data-testid="vipps-fallback-reclass-result"]').text()).toContain('admin.accounting.vippsFallbackReclass.done')
    expect(wrapper.get('[data-testid="vipps-fallback-reclass-row-entry-1"]').text()).toContain('admin.accounting.vippsFallbackReclass.outcomeReclassified')
    expect(wrapper.find('[data-testid="vipps-fallback-reclass-apply"]').exists()).toBe(false)
  })

  it('sends nothing when the step-up is dismissed or the confirmation cancelled', async () => {
    ensureFreshTotp.mockResolvedValueOnce(false)
    const wrapper = await opened()
    await wrapper.get('[data-testid="vipps-fallback-reclass-apply"]').trigger('click')
    await flushPromises()
    expect(confirm).not.toHaveBeenCalled()
    expect(applies()).toEqual([])

    confirm.mockResolvedValueOnce(false)
    await wrapper.get('[data-testid="vipps-fallback-reclass-apply"]').trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledOnce()
    expect(applies()).toEqual([])
  })

  it('disables the apply when auto-assign is off', async () => {
    fetchApi.mockResolvedValue({ ...base, auto_assign_enabled: false, proposed: 0, totals: [], items: [
      { ...base.items[2]!, reason_code: 'auto_assign_off' },
    ] })
    const wrapper = await opened()
    expect(wrapper.text()).toContain('admin.accounting.vippsFallbackReclass.autoAssignOff')
    expect(wrapper.get('[data-testid="vipps-fallback-reclass-apply"]').attributes('disabled')).toBeDefined()
  })

  it('shows an empty state and a server error', async () => {
    fetchApi.mockResolvedValueOnce({ ...base, items: [], proposed: 0, totals: [], needs_tildel: 0, stays: 0 })
    const wrapper = await opened()
    expect(wrapper.find('[data-testid="vipps-fallback-reclass-empty"]').exists()).toBe(true)

    // The server's text is not shown: a generic error, or a translated one for a known code.
    fetchApi.mockRejectedValueOnce(new Error('boom'))
    await wrapper.get('[data-testid="vipps-fallback-reclass-open"]').trigger('click')
    await flushPromises()
    const shown = wrapper.get('[data-testid="vipps-fallback-reclass-error"]').text()
    expect(shown).toBe('common.error')

    fetchApi.mockRejectedValueOnce(new ApiError(409, 'Vipps-autotildeling er av', 'auto_assign_off'))
    await wrapper.get('[data-testid="vipps-fallback-reclass-open"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="vipps-fallback-reclass-error"]').text()).toBe('admin.accounting.vippsFallbackReclass.autoAssignOff')
  })
})
