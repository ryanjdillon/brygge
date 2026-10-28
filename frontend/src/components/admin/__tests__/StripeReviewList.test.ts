import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref, type Ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import StripeReviewList from '@/components/admin/StripeReviewList.vue'
import type { StripeTxnItem, StripeTxnListStatus, StripeTxnPage } from '@/composables/useStripeTransactions'

const { calls, pages } = vi.hoisted(() => ({
  calls: [] as { status: string; offset: number }[],
  pages: {} as Record<string, unknown>,
}))

vi.mock('@/components/admin/StripeTransactionCard.vue', () => ({
  default: { props: ['item'], template: '<div class="card" :data-id="item.id" />' },
}))
vi.mock('@/composables/useStripeTransactions', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useStripeTransactions')>(
    '@/composables/useStripeTransactions',
  )
  const { computed } = await import('vue')
  return {
    ...actual,
    useStripeTransactions: (status: Ref<StripeTxnListStatus>, offset: Ref<number>) => {
      const data = computed(() => {
        calls.push({ status: status.value, offset: offset.value })
        return pages[`${status.value}:${offset.value}`] ?? { items: [], limit: 20, offset: offset.value, has_more: false }
      })
      return { data, isLoading: ref(false) }
    },
  }
})

const item = (id: string, extra: Partial<StripeTxnItem> = {}): StripeTxnItem => ({
  id,
  balance_transaction_id: 'txn_test_' + id,
  reporting_category: 'charge',
  stripe_type: 'charge',
  amount: 123,
  fee: 3,
  net: 120,
  currency: 'nok',
  date: '2026-05-04',
  created_at: '2026-05-04T10:00:00Z',
  status: 'needs_review',
  review_reason: 'no_attempt',
  order_id: null,
  attempt_id: null,
  payout_id: null,
  payout_cleared: false,
  allowed_actions: ['assign_invoice', 'assign_account', 'dismiss'],
  ...extra,
})

const page = (items: StripeTxnItem[], offset: number, hasMore: boolean): StripeTxnPage => ({
  items,
  limit: 20,
  offset,
  has_more: hasMore,
})

describe('StripeReviewList', () => {
  beforeEach(() => {
    calls.length = 0
    for (const k of Object.keys(pages)) delete pages[k]
  })

  it('renders one card per row in review and pages forward', async () => {
    pages['needs_review:0'] = page([item('a'), item('b')], 0, true)
    pages['needs_review:20'] = page([item('c')], 20, false)
    const wrapper = mountWithPlugins(StripeReviewList)
    expect(wrapper.findAll('.card').map((c) => c.attributes('data-id'))).toEqual(['a', 'b'])
    await wrapper.find('[data-testid="stripe-review-next"]').trigger('click')
    expect(wrapper.findAll('.card').map((c) => c.attributes('data-id'))).toEqual(['c'])
    expect(wrapper.find('[data-testid="stripe-review-prev"]').attributes('disabled')).toBeUndefined()
  })

  it('switches to the dismissed rows from the first page', async () => {
    pages['needs_review:0'] = page([item('a')], 0, true)
    pages['dismissed:0'] = page([item('d', { status: 'dismissed', allowed_actions: ['undismiss'] })], 0, false)
    const wrapper = mountWithPlugins(StripeReviewList)
    await wrapper.find('[data-testid="stripe-review-next"]').trigger('click')
    await wrapper.find('[data-testid="stripe-review-tab-dismissed"]').trigger('click')
    expect(wrapper.findAll('.card').map((c) => c.attributes('data-id'))).toEqual(['d'])
    expect(calls[calls.length - 1]).toEqual({ status: 'dismissed', offset: 0 })
    expect(wrapper.find('[data-testid="stripe-review-tab-dismissed"]').attributes('aria-selected')).toBe('true')
  })

  it('says when nothing waits', () => {
    const wrapper = mountWithPlugins(StripeReviewList)
    expect(wrapper.find('[data-testid="stripe-review-empty"]').text()).toBe('admin.bankImports.stripeTxn.empty.review')
    expect(wrapper.find('[data-testid="stripe-review-pager"]').exists()).toBe(false)
  })
})
