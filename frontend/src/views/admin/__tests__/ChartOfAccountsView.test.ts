import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import ChartOfAccountsView from '@/views/admin/ChartOfAccountsView.vue'

const { icon, update } = vi.hoisted(() => ({
  icon: { template: '<span />' },
  update: { mutate: vi.fn(), reset: vi.fn(), isPending: { value: false }, isError: { value: false }, error: { value: null } },
}))

vi.mock('lucide-vue-next', () => ({
  Plus: icon,
  Check: icon,
  X: icon,
  ChevronUp: icon,
  ChevronDown: icon,
  Lock: icon,
}))

vi.mock('@/composables/useAccounting', () => {
  const mutation = () => ({ mutate: vi.fn(), isPending: { value: false }, isSuccess: { value: false }, isError: { value: false }, error: { value: null }, data: { value: null } })
  return {
    useAccountsList: () => ({
      data: ref([
        { id: 'sys', club_id: 'c', code: '3300', name: 'Salgsinntekter', account_type: 'revenue', parent_code: '3000', is_system: true, is_active: true, mva_eligible: 'not_applicable', description: '', sort_order: 120 },
        { id: 'own', club_id: 'c', code: '8000', name: 'Eigen konto', account_type: 'expense', parent_code: '', is_system: false, is_active: true, mva_eligible: 'ineligible', description: '', sort_order: 999 },
      ]),
      isLoading: ref(false),
      isError: ref(false),
    }),
    useSeedAccounts: mutation,
    useCreateAccount: mutation,
    useUpdateAccount: () => update,
  }
})

const asTreasurer = { initialState: { auth: { user: { roles: ['treasurer'] } } } }

function rowFor(wrapper: ReturnType<typeof mountWithPlugins>, name: string) {
  return wrapper.findAll('tbody tr').find((r) => r.text().includes(name))!
}

describe('ChartOfAccountsView', () => {
  beforeEach(() => update.mutate.mockReset())

  it('renames a system account and keeps its MVA status', async () => {
    const wrapper = mountWithPlugins(ChartOfAccountsView, { piniaOptions: asTreasurer })
    await rowFor(wrapper, 'Salgsinntekter').trigger('click')

    expect(wrapper.find('[data-testid="account-mva-locked"]').exists()).toBe(true)
    await wrapper.get('[data-testid="account-edit-name"] input').setValue('Salgs- og tjenesteinntekter')
    await wrapper.get('[data-testid="account-edit-save"]').trigger('click')

    expect(update.mutate).toHaveBeenCalledWith(
      { id: 'sys', name: 'Salgs- og tjenesteinntekter', description: '', mva_eligible: 'not_applicable' },
      expect.anything(),
    )
  })

  it('leaves MVA editable on a club account', async () => {
    const wrapper = mountWithPlugins(ChartOfAccountsView, { piniaOptions: asTreasurer })
    await rowFor(wrapper, 'Eigen konto').trigger('click')
    expect(wrapper.find('[data-testid="account-edit-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="account-mva-locked"]').exists()).toBe(false)
  })

  it('does not open editing for a board member who may only read', async () => {
    const wrapper = mountWithPlugins(ChartOfAccountsView, {
      piniaOptions: { initialState: { auth: { user: { roles: ['member', 'chair'] } } } },
    })
    await rowFor(wrapper, 'Salgsinntekter').trigger('click')
    expect(wrapper.find('[data-testid="account-edit-name"]').exists()).toBe(false)
  })
})
