import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import type { Router } from 'vue-router'
import { mountWithPlugins } from '@/test/test-utils'
import RecentPaymentsView from '@/views/admin/RecentPaymentsView.vue'

const { fetchApi } = vi.hoisted(() => ({ fetchApi: vi.fn() }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))

const payment = (id: string, method: string, cardBrand: string | null, kind = 'guest') => ({
  id,
  paid_at: '2026-06-01T10:00:00Z',
  amount: 450,
  payer: 'Synthetic Payer',
  summary: '',
  kind,
  channel: method === 'vipps' ? 'vipps' : 'stripe',
  method,
  card_brand: cardBrand,
})

describe('RecentPaymentsView payment methods', () => {
  beforeEach(() => {
    fetchApi.mockReset()
    fetchApi.mockResolvedValue({
      payments: [
        payment('p1', 'vipps', null),
        payment('p2', 'card', 'visa'),
        payment('p3', 'apple_pay', 'mastercard'),
        payment('p4', 'google_pay', null),
        payment('p5', 'card', null),
      ],
    })
  })

  it('shows how each payment was made', async () => {
    const wrapper = mountWithPlugins(RecentPaymentsView)
    await flushPromises()
    const chips = wrapper.findAll('[data-testid="payment-method-chip"]')
    expect(chips.map((c) => c.attributes('data-method'))).toEqual(['vipps', 'card', 'apple_pay', 'google_pay', 'card'])
    expect(chips.map((c) => c.text())).toEqual([
      'recentPayments.method.vipps',
      'Visa',
      'recentPayments.method.apple_pay · Mastercard',
      'recentPayments.method.google_pay',
      'recentPayments.method.card',
    ])
  })

  it('keeps the chip row wrapping on narrow screens', async () => {
    const wrapper = mountWithPlugins(RecentPaymentsView)
    await flushPromises()
    const row = wrapper.find('[data-testid="payment-method-chip"]').element.parentElement!
    expect(row.className).toContain('flex-wrap')
  })
})

describe('RecentPaymentsView kind filter', () => {
  beforeEach(() => {
    fetchApi.mockReset()
    fetchApi.mockResolvedValue({ payments: [] })
    localStorage.clear()
  })

  const requestedKind = () => new URLSearchParams(String(fetchApi.mock.calls[fetchApi.mock.calls.length - 1][0]).split('?')[1]).get('kind')
  const pressed = (wrapper: ReturnType<typeof mountWithPlugins>) =>
    wrapper.findAll('[data-testid="kind-filters"] button').filter((b) => b.attributes('aria-pressed') === 'true')

  async function mountAt(url: string) {
    const wrapper = mountWithPlugins(RecentPaymentsView, { initialRoute: url })
    const router = wrapper.vm.$router as Router
    await router.isReady()
    await flushPromises()
    return { wrapper, router }
  }

  it('defaults to the harbor filter and writes it to the URL', async () => {
    const { wrapper, router } = await mountAt('/admin')
    expect(requestedKind()).toBe('guest,motorhome')
    const on = pressed(wrapper)
    expect(on).toHaveLength(1)
    expect(on[0].attributes('data-filter')).toBe('harbor')
    expect(on[0].text()).toBe('recentPayments.kind.harbor')
    expect(router.currentRoute.value.query.kind).toBe('harbor')
  })

  it('offers harbor, all and each kind as targets of at least 44px', async () => {
    const { wrapper } = await mountAt('/admin')
    const buttons = wrapper.findAll('[data-testid="kind-filters"] button')
    expect(buttons.map((b) => b.attributes('data-filter'))).toEqual([
      'harbor', 'all', 'guest', 'motorhome', 'faktura', 'merch', 'other',
    ])
    for (const b of buttons) expect(b.classes()).toContain('min-h-11')
    for (const input of wrapper.findAll('input[type="date"]')) expect(input.classes()).toContain('min-h-11')
  })

  it('reads ?kind= from the URL', async () => {
    const { wrapper } = await mountAt('/admin?kind=faktura')
    expect(requestedKind()).toBe('faktura')
    expect(pressed(wrapper)[0].attributes('data-filter')).toBe('faktura')
  })

  it('round-trips a chosen filter through the URL and remembers it', async () => {
    const { wrapper, router } = await mountAt('/admin?kind=harbor&q=keep')
    const replace = vi.spyOn(router, 'replace')
    const push = vi.spyOn(router, 'push')

    await wrapper.find('[data-filter="all"]').trigger('click')
    await flushPromises()

    expect(replace).toHaveBeenCalled()
    expect(push).not.toHaveBeenCalled()
    expect(router.currentRoute.value.query).toEqual({ kind: 'all', q: 'keep' })
    expect(requestedKind()).toBeNull()
    expect(localStorage.getItem('recentPayments.kind')).toBe('all')
    expect(pressed(wrapper)[0].attributes('data-filter')).toBe('all')

    await wrapper.find('[data-filter="guest"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.kind).toBe('guest')
    expect(requestedKind()).toBe('guest')
  })

  it('restores the remembered filter when the URL has none', async () => {
    localStorage.setItem('recentPayments.kind', 'merch')
    const { router } = await mountAt('/admin')
    expect(requestedKind()).toBe('merch')
    expect(router.currentRoute.value.query.kind).toBe('merch')
  })

  it('lets the URL win over the remembered filter, and ignores junk in either', async () => {
    localStorage.setItem('recentPayments.kind', 'merch')
    await mountAt('/admin?kind=other')
    expect(requestedKind()).toBe('other')

    for (const junk of ['bogus', 'constructor', 'toString', '__proto__']) {
      fetchApi.mockClear()
      localStorage.setItem('recentPayments.kind', junk)
      const { router } = await mountAt(`/admin?kind=${junk}`)
      expect(requestedKind()).toBe('guest,motorhome')
      expect(router.currentRoute.value.query.kind).toBe('harbor')
    }
  })
})
