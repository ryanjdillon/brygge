import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { mountWithPlugins } from '@/test/test-utils'
import UsersAdminView from '@/views/admin/UsersAdminView.vue'

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ hasRole: (role: string) => role === 'admin' }),
}))

const api = vi.hoisted(() => {
  const ok = () => Promise.resolve({ data: {}, response: { ok: true, status: 200 } })
  return { GET: vi.fn(ok), PATCH: vi.fn(ok), PUT: vi.fn(ok), POST: vi.fn(ok), DELETE: vi.fn(ok) }
})
vi.mock('@/lib/apiClient', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/apiClient')>()),
  useApiClient: () => api,
}))

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: vi.fn().mockResolvedValue(true), totpAwareFetch: vi.fn() }),
}))

const member = (id: string, first: string, email: string) => ({
  id,
  email,
  first_name: first,
  last_name: 'Testesen',
  roles: ['member'],
  deactivated_at: null,
  harbor_share_owner: false,
  harbor_share_paid_on: null,
})
const bounced = member('u-bounced', 'Kari', 'kari@example.invalid')
const fine = member('u-fine', 'Per', 'per@example.invalid')
const spam = member('u-spam', 'Siri', 'siri@example.invalid')
const blocked = member('u-blocked', 'Bjørn', 'bjorn@example.invalid')

const bounces = {
  'u-bounced': { status: 'rejected', at: '2026-10-02T08:00:00Z', detail: '550 5.1.1 no such user' },
  'u-spam': { status: 'complaint', at: '2026-10-03T08:00:00Z', detail: '' },
  'u-blocked': { status: 'suppressed', at: '2026-10-04T08:00:00Z', detail: '' },
}

function mount() {
  return mountWithPlugins(UsersAdminView, {
    global: {
      stubs: { BulkInvoicesModal: true, DockFilter: true, SpotFilter: true, NotesFilter: true, HarborShareFilter: true, ColumnSelector: true },
    },
  })
}

function row(wrapper: ReturnType<typeof mount>, name: string) {
  return wrapper.findAll('tbody tr').find((tr) => tr.text().includes(name))!
}

let queryKeys: unknown[][] = []

describe('UsersAdminView email bounce badge', () => {
  beforeEach(() => {
    queryKeys = []
    vi.mocked(useQuery).mockImplementation(((opts: { queryKey: unknown[] }) => {
      queryKeys.push(opts.queryKey)
      return {
        data: ref(opts.queryKey.includes('email-bounces') ? { bounces } : { users: [bounced, fine, spam, blocked], total_count: 4 }),
        isLoading: ref(false),
        isError: ref(false),
        error: ref(null),
        refetch: vi.fn(),
      }
    }) as never)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ items: [] }) }))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows the badge by a bounced member and not by others', async () => {
    const wrapper = mount()
    await flushPromises()
    const badge = row(wrapper, 'Kari').find('[data-testid="delivery-status-badge"]')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('admin.users.emailBounce.badge')
    expect(badge.classes()).toContain('bg-red-50')
    expect(row(wrapper, 'Per').find('[data-testid="delivery-status-badge"]').exists()).toBe(false)
    expect(row(wrapper, 'Siri').find('[data-testid="delivery-status-badge"]').text()).toBe(
      'admin.users.emailBounce.badgeComplaint',
    )
    expect(row(wrapper, 'Bjørn').find('[data-testid="delivery-status-badge"]').text()).toBe(
      'admin.users.emailBounce.badgeSuppressed',
    )
  })

  it('explains the bounce in the detail view', async () => {
    const wrapper = mount()
    await flushPromises()
    await row(wrapper, 'Kari').trigger('click')
    await flushPromises()
    const alert = wrapper.find('[data-testid="email-bounce-alert"]')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toBe('admin.users.emailBounce.alert')
  })

  it('words a suppressed address as blocked', async () => {
    const wrapper = mount()
    await flushPromises()
    await row(wrapper, 'Bjørn').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="email-bounce-alert"]').text()).toBe('admin.users.emailBounce.alertSuppressed')
  })

  it('shows no alert for a member without a bounce', async () => {
    const wrapper = mount()
    await flushPromises()
    await row(wrapper, 'Per').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="email-bounce-alert"]').exists()).toBe(false)
  })

  it('saving a member refetches the bounces, so a corrected address clears the badge', async () => {
    const invalidateQueries = vi.fn()
    vi.mocked(useQueryClient).mockReturnValue({ invalidateQueries } as never)
    const wrapper = mount()
    await flushPromises()
    await row(wrapper, 'Kari').findAll('button').find((b) => b.attributes('title') === 'common.edit')!.trigger('click')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(api.PATCH).toHaveBeenCalled()
    const bouncesKey = queryKeys.find((k) => k.includes('email-bounces'))!
    const invalidated = invalidateQueries.mock.calls.map(([arg]) => (arg as { queryKey: unknown[] }).queryKey)
    expect(invalidated.some((k) => k.every((part, i) => bouncesKey[i] === part))).toBe(true)
  })
})
