import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import CancelMembershipSection from '@/components/portal/CancelMembershipSection.vue'
import type { MyMembership } from '@/composables/useMyMembership'
import { ApiError } from '@/lib/errors'

const membership = ref<MyMembership | undefined>()
const cancelMembership = vi.fn()
const undoCancel = vi.fn()
const confirm = vi.fn()

vi.mock('@/composables/useMyMembership', () => ({
  useMyMembership: () => ({ data: membership, isLoading: ref(false) }),
  useCancelMembership: () => ({ mutateAsync: cancelMembership, isPending: ref(false) }),
  useUndoCancelMembership: () => ({ mutateAsync: undoCancel, isPending: ref(false) }),
}))

vi.mock('@/stores/confirm', () => ({
  useConfirm: () => confirm,
}))

function base(over: Partial<MyMembership> = {}): MyMembership {
  return {
    member_since: '2020-05-01',
    cancelled_at: null,
    ends_on: null,
    cancel_ends_on: '2026-12-31',
    can_undo: false,
    open_invoices: 0,
    holds_slip: false,
    harbor_share_owner: false,
    on_waiting_list: false,
    board_roles: [],
    ...over,
  }
}

describe('CancelMembershipSection', () => {
  beforeEach(() => {
    cancelMembership.mockReset()
    undoCancel.mockReset()
    confirm.mockReset()
  })

  it('explains the year-end effect and shows no conditional notices for a plain member', () => {
    membership.value = base()
    const w = mountWithPlugins(CancelMembershipSection)
    expect(w.text()).toContain('portal.membership.yearEnd')
    expect(w.text()).toContain('portal.membership.noRefund')
    expect(w.find('[data-testid="notice-waiting-list"]').exists()).toBe(false)
    expect(w.find('[data-testid="notice-slip"]').exists()).toBe(false)
    expect(w.find('[data-testid="notice-invoices"]').exists()).toBe(false)
    expect(w.find('[data-testid="notice-board"]').exists()).toBe(false)
  })

  it('shows the slip, open-faktura and board-role notices', () => {
    membership.value = base({ holds_slip: true, open_invoices: 2, board_roles: ['treasurer'], on_waiting_list: true })
    const w = mountWithPlugins(CancelMembershipSection)
    expect(w.find('[data-testid="notice-waiting-list"]').exists()).toBe(true)
    expect(w.find('[data-testid="notice-slip"]').exists()).toBe(true)
    const invoices = w.find('[data-testid="notice-invoices"]')
    expect(invoices.exists()).toBe(true)
    expect(invoices.find('a').attributes('href')).toBe('/portal/invoices')
    expect(w.find('[data-testid="notice-board"]').exists()).toBe(true)
  })

  it('cancels with the reason after one confirm', async () => {
    membership.value = base()
    confirm.mockResolvedValue(true)
    cancelMembership.mockResolvedValue(base({ cancelled_at: '2026-11-07T10:00:00Z', ends_on: '2026-12-31', can_undo: true }))
    const w = mountWithPlugins(CancelMembershipSection)
    await w.find('textarea').setValue('  Selt båten ')
    await w.find('[data-testid="cancel-membership-button"]').trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(confirm.mock.calls[0][0].tone).toBe('danger')
    expect(cancelMembership).toHaveBeenCalledWith('Selt båten')
  })

  it('does nothing when the confirm is declined', async () => {
    membership.value = base()
    confirm.mockResolvedValue(false)
    const w = mountWithPlugins(CancelMembershipSection)
    await w.find('[data-testid="cancel-membership-button"]').trigger('click')
    await flushPromises()
    expect(cancelMembership).not.toHaveBeenCalled()
  })

  it('shows the cancelled state with an undo button', async () => {
    membership.value = base({ cancelled_at: '2026-11-07T10:00:00Z', ends_on: '2026-12-31', can_undo: true })
    undoCancel.mockResolvedValue(base())
    const w = mountWithPlugins(CancelMembershipSection)
    expect(w.find('[data-testid="cancelled-state"]').text()).toContain('portal.membership.cancelledUntil')
    expect(w.find('[data-testid="cancel-membership-button"]').exists()).toBe(false)
    await w.find('[data-testid="undo-cancel"]').trigger('click')
    await flushPromises()
    expect(undoCancel).toHaveBeenCalledTimes(1)
  })

  it('says the membership has ended when the undo comes too late', async () => {
    membership.value = base({ cancelled_at: '2025-11-07T10:00:00Z', ends_on: '2025-12-31', can_undo: true })
    undoCancel.mockRejectedValue(new ApiError(409, 'ended', 'membership_ended'))
    const w = mountWithPlugins(CancelMembershipSection)
    await w.find('[data-testid="undo-cancel"]').trigger('click')
    await flushPromises()
    expect(w.find('[role="alert"]').text()).toContain('portal.membership.undoEnded')
  })

  it('hides the undo once it can no longer be undone', () => {
    membership.value = base({ cancelled_at: '2025-11-07T10:00:00Z', ends_on: '2025-12-31', can_undo: false })
    const w = mountWithPlugins(CancelMembershipSection)
    expect(w.find('[data-testid="undo-cancel"]').exists()).toBe(false)
  })
})
