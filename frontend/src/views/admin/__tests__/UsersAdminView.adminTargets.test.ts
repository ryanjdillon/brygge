import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { useQuery } from '@tanstack/vue-query'
import { mountWithPlugins } from '@/test/test-utils'
import UsersAdminView from '@/views/admin/UsersAdminView.vue'

const { viewer } = vi.hoisted(() => ({ viewer: { roles: [] as string[] } }))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ hasRole: (role: string) => viewer.roles.includes(role) }),
}))

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: vi.fn().mockResolvedValue(true), totpAwareFetch: vi.fn() }),
}))

const users = [
  { id: 'u-admin', email: 'a@example.com', first_name: 'Ada', last_name: 'Admin', roles: ['admin'], deactivated_at: null },
  { id: 'u-member', email: 'm@example.com', first_name: 'Mia', last_name: 'Member', roles: ['member'], deactivated_at: null },
]

async function mountAs(viewerRoles: string[]) {
  viewer.roles = viewerRoles
  const wrapper = mountWithPlugins(UsersAdminView, {
    global: {
      stubs: { BulkInvoicesModal: true, DockFilter: true, SpotFilter: true, NotesFilter: true, ColumnSelector: true },
    },
  })
  await flushPromises()
  return wrapper
}

// The row actions: Edit (pencil), then deactivate or reactivate.
function actionsByRow(wrapper: Awaited<ReturnType<typeof mountAs>>) {
  return wrapper.findAll('tbody tr').map((tr) => ({
    name: tr.text(),
    edit: tr.findAll('button').filter((b) => b.attributes('title') === 'common.edit').length,
    deactivate: tr.findAll('button').filter((b) => b.attributes('title') === 'admin.users.deactivate').length,
  }))
}

describe('UsersAdminView admin targets', () => {
  beforeEach(() => {
    vi.mocked(useQuery).mockImplementation((() => ({
      data: ref({ users, total_count: users.length }),
      isLoading: ref(false),
      isError: ref(false),
      error: ref(null),
      refetch: vi.fn(),
    })) as never)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ items: [] }) }))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('hides Edit and deactivate on an admin row for a board viewer', async () => {
    const rows = actionsByRow(await mountAs(['board']))
    const admin = rows.find((r) => r.name.includes('Ada'))!
    const member = rows.find((r) => r.name.includes('Mia'))!
    expect(admin.edit).toBe(0)
    expect(admin.deactivate).toBe(0)
    expect(member.edit).toBe(1)
    expect(member.deactivate).toBe(1)
  })

  it('shows them on every row for an admin viewer', async () => {
    const rows = actionsByRow(await mountAs(['admin']))
    expect(rows).toHaveLength(users.length)
    for (const row of rows) {
      expect(row.edit).toBe(1)
      expect(row.deactivate).toBe(1)
    }
  })
})
