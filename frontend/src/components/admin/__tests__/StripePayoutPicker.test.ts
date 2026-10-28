import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import StripePayoutPicker from '@/components/admin/StripePayoutPicker.vue'
import { ApiError } from '@/lib/errors'
import type { UnclearedStripePayout } from '@/composables/useStripePayouts'

const { payouts, loading, mutateAsync, toast } = vi.hoisted(() => ({
  payouts: { value: [] as unknown[] },
  loading: { value: false },
  mutateAsync: vi.fn(),
  toast: { success: vi.fn(), info: vi.fn(), error: vi.fn() },
}))

vi.mock('lucide-vue-next', () => ({ X: { template: '<span />' } }))
vi.mock('@/composables/useToast', () => ({ useToast: () => toast }))
vi.mock('@/composables/useStripePayouts', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useStripePayouts')>('@/composables/useStripePayouts')
  return {
    ...actual,
    useUnclearedStripePayouts: () => ({ data: ref(payouts.value), isLoading: ref(loading.value) }),
    useClearStripePayout: () => ({ mutateAsync, isPending: ref(false) }),
  }
})

const payout = (id: string, ore: number, extra: Partial<UnclearedStripePayout> = {}): UnclearedStripePayout => ({
  id,
  payout_id: 'po_test_' + id,
  amount: ore / 100,
  amount_ore: ore,
  arrival_date: '2026-06-10',
  automatic: true,
  members_listed: true,
  members_count: 1,
  open_members: 0,
  members_net_ore: ore,
  open_balance: 0,
  ...extra,
})

const row = { id: 'row-1', amount: 50, row_date: '2026-06-11' }

function mountPicker() {
  return mountWithPlugins(StripePayoutPicker, {
    props: { open: true, row },
    global: { stubs: { teleport: true } },
  })
}

describe('StripePayoutPicker', () => {
  beforeEach(() => {
    payouts.value = []
    loading.value = false
    mutateAsync.mockReset()
    Object.values(toast).forEach((f) => f.mockReset())
  })

  it('shows loading and empty states', () => {
    loading.value = true
    expect(mountPicker().find('[data-testid="stripe-payout-picker-loading"]').exists()).toBe(true)
    loading.value = false
    const wrapper = mountPicker()
    expect(wrapper.find('[data-testid="stripe-payout-picker-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="stripe-payout-picker-confirm"]').attributes('disabled')).toBeDefined()
  })

  it('lists the payout of the deposit amount first and preselects it; others cannot be picked', () => {
    payouts.value = [payout('other', 7000), payout('manual', 5000, { automatic: false, members_listed: false, members_count: 0 })]
    const wrapper = mountPicker()
    const items = wrapper.findAll('[role="radio"]')
    expect(items[0].attributes('data-testid')).toBe('stripe-payout-po_test_manual')
    expect(items[0].attributes('aria-checked')).toBe('true')
    expect(items[0].text()).toContain('admin.bankReconcile.stripePayoutPicker.manual')
    expect(items[0].text()).toContain('admin.bankReconcile.stripePayoutPicker.matchesRow')
    expect(items[1].attributes('disabled')).toBeDefined()
    expect(items[1].text()).toContain('admin.bankReconcile.stripePayoutPicker.amountDiffers')
  })

  it('does not preselect a payout with open members', () => {
    payouts.value = [payout('open', 5000, { open_members: 2 })]
    const wrapper = mountPicker()
    expect(wrapper.find('[data-testid="stripe-payout-open-members"]').exists()).toBe(true)
    expect(wrapper.find('[role="radio"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="stripe-payout-picker-confirm"]').attributes('disabled')).toBeDefined()
  })

  it('cannot pick an automatic payout Stripe has not listed, or a manual one with open balance rows', () => {
    payouts.value = [
      payout('unlisted', 5000, { members_listed: false, members_count: 0 }),
      payout('manual', 5000, { automatic: false, members_listed: false, members_count: 0, open_balance: 3 }),
    ]
    const wrapper = mountPicker()
    const [unlisted, manual] = wrapper.findAll('[role="radio"]')
    expect(unlisted.attributes('disabled')).toBeDefined()
    expect(unlisted.find('[data-testid="stripe-payout-not-listed"]').exists()).toBe(true)
    expect(manual.attributes('disabled')).toBeDefined()
    expect(manual.find('[data-testid="stripe-payout-open-balance"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="stripe-payout-picker-confirm"]').attributes('disabled')).toBeDefined()
  })

  it('starts from no selection when the row changes', async () => {
    payouts.value = [payout('a', 5000), payout('b', 5000), payout('c', 7000)]
    const wrapper = mountPicker()
    expect(wrapper.findAll('[aria-checked="true"]')).toHaveLength(0)
    await wrapper.find('[data-testid="stripe-payout-po_test_a"]').trigger('click')
    expect(wrapper.find('[data-testid="stripe-payout-po_test_a"]').attributes('aria-checked')).toBe('true')

    await wrapper.setProps({ row: { id: 'row-2', amount: 50, row_date: '2026-06-12' } })
    expect(wrapper.findAll('[aria-checked="true"]')).toHaveLength(0)

    await wrapper.setProps({ row: { id: 'row-3', amount: 70, row_date: '2026-06-12' } })
    expect(wrapper.find('[data-testid="stripe-payout-po_test_c"]').attributes('aria-checked')).toBe('true')
  })

  it('reports a cleared manual payout without a remainder', async () => {
    payouts.value = [payout('m', 5000, { automatic: false, members_listed: false, members_count: 0 })]
    mutateAsync.mockResolvedValue({
      id: 'm', payout_id: 'po_test_m', bank_row_id: 'row-1', journal_entry_id: 'je-2', members_count: 0, members_net_ore: 0,
    })
    const wrapper = mountPicker()
    await wrapper.find('[data-testid="stripe-payout-picker-confirm"]').trigger('click')
    await flushPromises()
    expect(toast.info).toHaveBeenCalledWith('admin.bankReconcile.stripePayoutPicker.residueManual', 10000)
  })

  it('clears the selected payout against the row and reports a remainder', async () => {
    payouts.value = [payout('a', 5000)]
    mutateAsync.mockResolvedValue({
      id: 'a', payout_id: 'po_test_a', bank_row_id: 'row-1', journal_entry_id: 'je-1',
      members_count: 1, members_net_ore: 3000, clearing_balance_ore: -2000,
    })
    const wrapper = mountPicker()
    await wrapper.find('[data-testid="stripe-payout-picker-confirm"]').trigger('click')
    await flushPromises()
    expect(mutateAsync).toHaveBeenCalledWith({ payoutId: 'a', bankRowId: 'row-1' })
    expect(toast.success).toHaveBeenCalled()
    expect(toast.info).toHaveBeenCalledWith('admin.bankReconcile.stripePayoutPicker.residue', 15000)
    expect(wrapper.emitted('cleared')?.[0]?.[0]).toMatchObject({ journal_entry_id: 'je-1' })
    expect(wrapper.emitted('update:open')?.[0]).toEqual([false])
  })

  it('shows a refusal in words and stays open', async () => {
    payouts.value = [payout('a', 5000)]
    mutateAsync.mockRejectedValue(new ApiError(409, 'refused', 'members_open', { code: 'members_open', open_members: 2 }))
    const wrapper = mountPicker()
    await wrapper.find('[data-testid="stripe-payout-picker-confirm"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="stripe-payout-picker-error"]').text()).toBe(
      'admin.bankReconcile.stripePayoutPicker.errors.members_open',
    )
    expect(wrapper.emitted('update:open')).toBeUndefined()
  })
})
