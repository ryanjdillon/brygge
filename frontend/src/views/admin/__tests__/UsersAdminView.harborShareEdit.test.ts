import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { useQuery } from '@tanstack/vue-query'
import { mountWithPlugins } from '@/test/test-utils'
import UsersAdminView from '@/views/admin/UsersAdminView.vue'

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ hasRole: (role: string) => role === 'admin' }),
}))

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: vi.fn().mockResolvedValue(true), totpAwareFetch: vi.fn() }),
}))

const owner = {
  id: 'u-owner',
  email: 'o@example.com',
  first_name: 'Ola',
  last_name: 'Owner',
  roles: ['member'],
  deactivated_at: null,
  harbor_share_owner: true,
  harbor_share_paid_on: '2026-03-15',
}
const nonOwner = {
  id: 'u-plain',
  email: 'p@example.com',
  first_name: 'Pia',
  last_name: 'Plain',
  roles: ['member'],
  deactivated_at: null,
  harbor_share_owner: false,
  harbor_share_paid_on: null,
}

async function openEditFor(name: string) {
  const wrapper = mountWithPlugins(UsersAdminView, {
    global: {
      stubs: { BulkInvoicesModal: true, DockFilter: true, SpotFilter: true, NotesFilter: true, HarborShareFilter: true, ColumnSelector: true },
    },
  })
  await flushPromises()
  const row = wrapper.findAll('tbody tr').find((tr) => tr.text().includes(name))!
  await row.findAll('button').find((b) => b.attributes('title') === 'common.edit')!.trigger('click')
  await flushPromises()
  return wrapper
}

function harborShareControls(wrapper: Awaited<ReturnType<typeof openEditFor>>) {
  const ownerLabel = wrapper.findAll('label').find((l) => l.text().includes('admin.users.harborShareOwner'))!
  return {
    ownerBox: ownerLabel.find('input[type="checkbox"]'),
    dateInput: wrapper.find<HTMLInputElement>('#ed-harbor-share-paid-on'),
    clearButton: wrapper.findAll('button').find((b) => b.text() === 'admin.users.harborShareClearDate')!,
  }
}

describe('UsersAdminView harbor share edit', () => {
  beforeEach(() => {
    vi.mocked(useQuery).mockImplementation((() => ({
      data: ref({ users: [owner, nonOwner], total_count: 2 }),
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

  it('unticking ownership clears the paid date and disables the date controls', async () => {
    const wrapper = await openEditFor('Ola')
    let c = harborShareControls(wrapper)
    expect((c.ownerBox.element as HTMLInputElement).checked).toBe(true)
    expect(c.dateInput.element.value).toBe('2026-03-15')
    expect(c.dateInput.attributes('disabled')).toBeUndefined()
    expect(c.clearButton.attributes('disabled')).toBeUndefined()

    await c.ownerBox.setValue(false)
    await flushPromises()

    c = harborShareControls(wrapper)
    expect(c.dateInput.element.value).toBe('')
    expect(c.dateInput.attributes('disabled')).toBeDefined()
    expect(c.clearButton.attributes('disabled')).toBeDefined()
  })

  it('disables the date and Clear for a member who is not an owner', async () => {
    const c = harborShareControls(await openEditFor('Pia'))
    expect((c.ownerBox.element as HTMLInputElement).checked).toBe(false)
    expect(c.dateInput.attributes('disabled')).toBeDefined()
    expect(c.clearButton.attributes('disabled')).toBeDefined()
  })
})
