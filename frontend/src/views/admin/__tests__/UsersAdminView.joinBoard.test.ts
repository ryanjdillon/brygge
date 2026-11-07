import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { createTestingPinia } from '@pinia/testing'
import { useQuery } from '@tanstack/vue-query'
import { mountWithPlugins, createTestRouter } from '@/test/test-utils'
import { formatDate } from '@/lib/format'
import UsersAdminView from '@/views/admin/UsersAdminView.vue'

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ hasRole: (role: string) => role === 'board' }),
}))

const ensureFreshTotp = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch: vi.fn() }),
}))

const toastError = vi.hoisted(() => vi.fn())
vi.mock('@/composables/useToast', () => ({
  useToast: () => ({ success: vi.fn(), error: toastError }),
}))

const client = vi.hoisted(() => ({
  GET: vi.fn(async () => ({ data: { users: [], total_count: 0 }, response: { ok: true } })),
  PATCH: vi.fn(async () => ({ data: { updated: true }, response: { ok: true } })),
  PUT: vi.fn(),
  POST: vi.fn(),
  DELETE: vi.fn(),
}))
vi.mock('@/lib/apiClient', async () => {
  const actual = await vi.importActual<typeof import('@/lib/apiClient')>('@/lib/apiClient')
  return { ...actual, useApiClient: () => client, unwrap: (r: { data: unknown }) => r.data }
})

const COLUMNS_KEY = 'brygge.admin.users.columns'

const applicant = {
  id: 'u-applicant',
  email: 'applicant@example.invalid',
  first_name: 'Ada',
  last_name: 'Applicant',
  roles: ['applicant'],
  deactivated_at: null,
  rejoin_blocked: false,
  membership_ends_on: null,
  member_since: null,
}
const cancelled = {
  id: 'u-cancelled',
  email: 'cancelled@example.invalid',
  first_name: 'Cato',
  last_name: 'Cancelled',
  roles: ['member'],
  deactivated_at: null,
  rejoin_blocked: false,
  membership_ends_on: '2026-12-31',
  member_since: '2026-03-02',
}
const former = {
  id: 'u-former',
  email: 'former@example.invalid',
  first_name: 'Frida',
  last_name: 'Former',
  roles: ['member'],
  deactivated_at: '2026-01-05T10:00:00Z',
  rejoin_blocked: true,
  membership_ends_on: null,
  member_since: '2019-05-01',
}

type QueryOpts = { queryKey: unknown[]; queryFn: () => Promise<unknown> }
let usersQuery: QueryOpts | undefined

function mountView() {
  return mountWithPlugins(UsersAdminView, {
    global: {
      stubs: { BulkInvoicesModal: true, DockFilter: true, SpotFilter: true, NotesFilter: true, HarborShareFilter: true, ColumnSelector: true },
    },
  })
}

async function openDetail(name: string) {
  const wrapper = mountView()
  await flushPromises()
  await wrapper.findAll('tbody tr').find((tr) => tr.text().includes(name))!.trigger('click')
  await flushPromises()
  return wrapper
}

describe('UsersAdminView self-service join board view', () => {
  beforeEach(() => {
    usersQuery = undefined
    vi.mocked(useQuery).mockImplementation(((opts: QueryOpts) => {
      if (opts.queryKey[1] === 'users' && opts.queryKey.length > 3) usersQuery = opts
      return {
        data: ref({ users: [applicant, cancelled, former], total_count: 3 }),
        isLoading: ref(false),
        isError: ref(false),
        error: ref(null),
        refetch: vi.fn(),
      }
    }) as never)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ items: [] }) }))
    localStorage.removeItem(COLUMNS_KEY)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    client.GET.mockClear()
    client.PATCH.mockClear()
    ensureFreshTotp.mockReset()
    ensureFreshTotp.mockImplementation(async () => true)
    localStorage.removeItem(COLUMNS_KEY)
  })

  it('offers the applicant and new-this-year filters and sends them to the server', async () => {
    const wrapper = mountView()
    await flushPromises()
    const select = wrapper.get('#member-status-filter')
    const values = select.findAll('option').map((o) => o.attributes('value'))
    expect(values).toEqual(expect.arrayContaining(['applicant', 'new_this_year']))

    for (const status of ['applicant', 'new_this_year']) {
      await select.setValue(status)
      client.GET.mockClear()
      await usersQuery!.queryFn()
      const [path, init] = client.GET.mock.calls[0] as unknown as [string, { params: { query: Record<string, unknown> } }]
      expect(path).toBe('/api/v1/admin/users')
      expect(init.params.query.status).toBe(status)
    }
  })

  it('hides the member-since column by default and shows the date when picked', async () => {
    let wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-testid="member-since-cell"]').exists()).toBe(false)
    wrapper.unmount()

    localStorage.setItem(COLUMNS_KEY, JSON.stringify(['first_name', 'member_since']))
    wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('thead').text()).toContain('admin.columns.memberSince')
    const cells = wrapper.findAll('[data-testid="member-since-cell"]').map((c) => c.text())
    expect(cells).toEqual(['—', formatDate('2026-03-02', 'en'), formatDate('2019-05-01', 'en')])
  })

  it('badges a cancelled member with the end date and a blocked one as unable to rejoin', async () => {
    const wrapper = mountView()
    await flushPromises()
    const rows = wrapper.findAll('tbody tr')
    const row = (name: string) => rows.find((tr) => tr.text().includes(name))!

    expect(row('Cato').get('[data-testid="cancelled-badge"]').text())
      .toBe(`admin.users.cancelledBadge ${formatDate('2026-12-31', 'en')}`)
    expect(row('Cato').find('[data-testid="rejoin-blocked-badge"]').exists()).toBe(false)
    expect(row('Frida').get('[data-testid="rejoin-blocked-badge"]').text()).toBe('admin.users.rejoinBlockedBadge')
    expect(row('Ada').find('[data-testid="rejoin-blocked-badge"]').exists()).toBe(false)
  })

  it('offers the rejoin block only for a former member or a member already blocked', async () => {
    const active = await openDetail('Ada')
    expect(active.find('[data-testid="rejoin-blocked-control"]').exists()).toBe(false)

    const blocked = await openDetail('Frida')
    const control = blocked.get('[data-testid="rejoin-blocked-control"]')
    expect(control.text()).toContain('admin.users.rejoinBlockedHint')
    expect((control.get('input').element as HTMLInputElement).checked).toBe(true)
  })

  it('asks for a fresh TOTP on the click, then PATCHes only the mark', async () => {
    const wrapper = await openDetail('Frida')
    const box = wrapper.get<HTMLInputElement>('[data-testid="rejoin-blocked-checkbox"]')

    box.element.checked = false
    await box.trigger('change')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(client.PATCH).toHaveBeenCalledTimes(1)
    expect(ensureFreshTotp.mock.invocationCallOrder[0]).toBeLessThan(client.PATCH.mock.invocationCallOrder[0])
    const [path, init] = client.PATCH.mock.calls[0] as unknown as [string, { params: { path: { userID: string } }; body: unknown }]
    expect(path).toBe('/api/v1/admin/users/{userID}')
    expect(init.params.path.userID).toBe('u-former')
    expect(init.body).toEqual({ rejoin_blocked: false })
    expect(wrapper.get<HTMLInputElement>('[data-testid="rejoin-blocked-checkbox"]').element.checked).toBe(false)
  })

  it('keeps the old value and shows a toast when the PATCH fails', async () => {
    client.PATCH.mockRejectedValueOnce(new Error('boom'))
    const wrapper = await openDetail('Frida')
    const box = wrapper.get<HTMLInputElement>('[data-testid="rejoin-blocked-checkbox"]')

    box.element.checked = false
    await box.trigger('change')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(client.PATCH).toHaveBeenCalledTimes(1)
    expect(box.element.checked).toBe(true)
    expect(toastError).toHaveBeenCalledWith('admin.users.updateError')
  })

  it('sorts by member since on the server', async () => {
    localStorage.setItem(COLUMNS_KEY, JSON.stringify(['first_name', 'member_since']))
    const wrapper = mountView()
    await flushPromises()
    const th = wrapper.findAll('th').find((h) => h.text().includes('admin.columns.memberSince'))!
    await th.trigger('click')
    client.GET.mockClear()
    await usersQuery!.queryFn()
    const [, init] = client.GET.mock.calls[0] as unknown as [string, { params: { query: Record<string, unknown> } }]
    expect(init.params.query.sort).toBe('member_since')
  })

  it('leaves the mark alone when the fresh TOTP check is declined', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = await openDetail('Frida')
    const box = wrapper.get<HTMLInputElement>('[data-testid="rejoin-blocked-checkbox"]')

    box.element.checked = false
    await box.trigger('change')
    await flushPromises()

    expect(client.PATCH).not.toHaveBeenCalled()
    expect(box.element.checked).toBe(true)
  })

  it('opens a member linked from a board email even when the list does not hold them', async () => {
    client.GET.mockResolvedValueOnce({
      data: { user: { ...former, id: 'u-elsewhere', first_name: 'Elsa' }, boats: [] },
      response: { ok: true },
    } as never)
    const router = createTestRouter()
    await router.push('/admin?user=u-elsewhere')
    await router.isReady()
    const wrapper = mount(UsersAdminView, {
      global: {
        plugins: [createTestingPinia({ createSpy: vi.fn }), router],
        stubs: { BulkInvoicesModal: true, DockFilter: true, SpotFilter: true, NotesFilter: true, HarborShareFilter: true, ColumnSelector: true },
      },
    })
    await flushPromises()

    const [path, init] = client.GET.mock.calls[0] as unknown as [string, { params: { path: { userID: string } } }]
    expect(path).toBe('/api/v1/admin/users/{userID}')
    expect(init.params.path.userID).toBe('u-elsewhere')
    expect(wrapper.get('h2').text()).toContain('Elsa')
    expect(wrapper.find('[data-testid="rejoin-blocked-control"]').exists()).toBe(true)
  })

  it('shows member since in the detail panel', async () => {
    const wrapper = await openDetail('Cato')
    expect(wrapper.get('[data-testid="detail-member-since"]').text()).toBe(formatDate('2026-03-02', 'en'))
  })
})
