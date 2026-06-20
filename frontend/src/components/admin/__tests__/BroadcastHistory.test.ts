import { describe, it, expect, vi, beforeEach } from 'vitest'
import { nextTick } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import BroadcastHistory from '@/components/admin/BroadcastHistory.vue'

const hoisted = vi.hoisted(() => ({ mutate: vi.fn() }))

vi.mock('@/composables/useBroadcasts', async () => {
  const { ref } = await import('vue')
  const summary = {
    id: 'b1',
    subject: 'Vårdugnad',
    recipients: 'members',
    source_address: 'styret@x.no',
    status: 'complete',
    total: 3,
    sent: 2,
    failed: 1,
    pending: 0,
    sent_at: '2026-06-20T10:00:00Z',
    created_at: '2026-06-20T10:00:00Z',
  }
  const detail = {
    ...summary,
    body_text: 'Hei',
    body_html: '',
    deliveries: [
      { id: 'd1', email: 'a@x.no', status: 'sent', attempts: 1, error: '', sent_at: '2026-06-20T10:00:01Z' },
      { id: 'd2', email: 'b@x.no', status: 'failed', attempts: 3, error: '550 rejected', sent_at: null },
    ],
  }
  return {
    useBroadcasts: () => ({ data: ref([summary]), isLoading: ref(false), isError: ref(false) }),
    useBroadcastDetail: () => ({ data: ref(detail), isLoading: ref(false) }),
    useRetryBroadcast: () => ({ mutate: hoisted.mutate, isPending: ref(false) }),
  }
})

describe('BroadcastHistory', () => {
  beforeEach(() => hoisted.mutate.mockClear())

  it('renders the broadcast list with subject and totals', () => {
    const wrapper = mountWithPlugins(BroadcastHistory)
    expect(wrapper.text()).toContain('Vårdugnad')
    // Raw total interpolation (status counts go through i18n and render as keys in tests).
    expect(wrapper.text()).toContain('3')
  })

  it('shows per-recipient delivery rows after selecting a broadcast', async () => {
    const wrapper = mountWithPlugins(BroadcastHistory)
    await wrapper.find('ul button').trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('a@x.no')
    expect(wrapper.text()).toContain('b@x.no')
    expect(wrapper.text()).toContain('550 rejected')
  })

  it('triggers the retry mutation for failed deliveries', async () => {
    const wrapper = mountWithPlugins(BroadcastHistory)
    await wrapper.find('ul button').trigger('click')
    await nextTick()

    // The retry button is the one outside the list (failed count > 0).
    const buttons = wrapper.findAll('button')
    const retryBtn = buttons[buttons.length - 1]
    await retryBtn.trigger('click')

    expect(hoisted.mutate).toHaveBeenCalledWith('b1')
  })
})
