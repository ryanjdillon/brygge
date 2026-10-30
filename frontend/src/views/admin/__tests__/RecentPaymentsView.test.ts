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

  const lastParams = () => new URLSearchParams(String(fetchApi.mock.calls[fetchApi.mock.calls.length - 1][0]).split('?')[1])
  const requestedKind = () => lastParams().get('kind')
  const pressed = (wrapper: ReturnType<typeof mountWithPlugins>) =>
    wrapper.findAll('[data-testid="kind-filters"] button').filter((b) => b.attributes('aria-pressed') === 'true')
  const pressedIds = (wrapper: ReturnType<typeof mountWithPlugins>) => pressed(wrapper).map((b) => b.attributes('data-filter'))

  async function mountAt(url: string) {
    const wrapper = mountWithPlugins(RecentPaymentsView, { initialRoute: url })
    const router = wrapper.vm.$router as Router
    await router.isReady()
    await flushPromises()
    return { wrapper, router }
  }

  it('defaults to all and writes it to the URL', async () => {
    const { wrapper, router } = await mountAt('/admin')
    expect(requestedKind()).toBeNull()
    expect(pressedIds(wrapper)).toEqual(['all'])
    expect(router.currentRoute.value.query.kind).toBe('all')
  })

  it('lists all first, then harbor and each kind, as targets of at least 44px', async () => {
    const { wrapper } = await mountAt('/admin')
    const buttons = wrapper.findAll('[data-testid="kind-filters"] button')
    expect(buttons.map((b) => b.attributes('data-filter'))).toEqual([
      'all', 'harbor', 'guest', 'motorhome', 'slipping', 'faktura', 'merch', 'other',
    ])
    for (const b of buttons) expect(b.classes()).toContain('min-h-11')
    for (const input of wrapper.findAll('input[type="date"]')) expect(input.classes()).toContain('min-h-11')
  })

  it('reads ?kind= from the URL, one kind or several', async () => {
    const { wrapper } = await mountAt('/admin?kind=faktura')
    expect(requestedKind()).toBe('faktura')
    expect(pressedIds(wrapper)).toEqual(['faktura'])

    fetchApi.mockClear()
    const several = await mountAt('/admin?kind=guest,slipping')
    expect(requestedKind()?.split(',').sort()).toEqual(['guest', 'slipping'])
    expect(pressedIds(several.wrapper)).toEqual(['guest', 'slipping'])
  })

  it('adds the chips pressed together, without repeating a kind', async () => {
    const { wrapper, router } = await mountAt('/admin?q=keep')
    const replace = vi.spyOn(router, 'replace')
    const push = vi.spyOn(router, 'push')

    await wrapper.find('[data-filter="harbor"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['harbor'])
    expect(requestedKind()).toBe('guest,motorhome')

    await wrapper.find('[data-filter="motorhome"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-filter="slipping"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['harbor', 'motorhome', 'slipping'])
    expect(requestedKind()?.split(',').sort()).toEqual(['guest', 'motorhome', 'slipping'])
    expect(router.currentRoute.value.query.kind).toBe('harbor,motorhome,slipping')
    expect(router.currentRoute.value.query.q).toBe('keep')
    expect(replace).toHaveBeenCalled()
    expect(push).not.toHaveBeenCalled()
    expect(localStorage.getItem('recentPayments.kinds')).toBe('harbor,motorhome,slipping')
  })

  it('removes a pressed chip, and falls back to all when none is left', async () => {
    const { wrapper } = await mountAt('/admin?kind=guest,faktura')
    await wrapper.find('[data-filter="guest"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['faktura'])
    expect(requestedKind()).toBe('faktura')

    await wrapper.find('[data-filter="faktura"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['all'])
    expect(requestedKind()).toBeNull()
  })

  it('clears the other chips when all is pressed, and all when another is', async () => {
    const { wrapper } = await mountAt('/admin?kind=guest,merch')
    await wrapper.find('[data-filter="all"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['all'])
    expect(requestedKind()).toBeNull()

    await wrapper.find('[data-filter="merch"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['merch'])
  })

  it('restores the remembered filters when the URL has none', async () => {
    localStorage.setItem('recentPayments.kinds', 'merch,other')
    const { router } = await mountAt('/admin')
    expect(requestedKind()?.split(',').sort()).toEqual(['merch', 'other'])
    expect(router.currentRoute.value.query.kind).toBe('merch,other')
  })

  it('ignores a single choice stored under the previous key', async () => {
    localStorage.setItem('recentPayments.kind', 'harbor')
    const { wrapper } = await mountAt('/admin')
    expect(pressedIds(wrapper)).toEqual(['all'])
  })

  it('lets the URL win over the remembered filters, and ignores junk in either', async () => {
    localStorage.setItem('recentPayments.kinds', 'merch')
    await mountAt('/admin?kind=other')
    expect(requestedKind()).toBe('other')

    for (const junk of ['bogus', 'constructor', 'toString', '__proto__']) {
      fetchApi.mockClear()
      localStorage.setItem('recentPayments.kinds', junk)
      const { router } = await mountAt(`/admin?kind=${junk}`)
      expect(requestedKind()).toBeNull()
      expect(router.currentRoute.value.query.kind).toBe('all')
    }
  })

  it('keeps the valid ids of a mixed list and lets all win', async () => {
    const { wrapper } = await mountAt('/admin?kind=guest,bogus,guest')
    expect(pressedIds(wrapper)).toEqual(['guest'])
    fetchApi.mockClear()
    const withAll = await mountAt('/admin?kind=guest,all')
    expect(pressedIds(withAll.wrapper)).toEqual(['all'])
  })
})

describe('RecentPaymentsView dates', () => {
  const isoDay = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  const lastParams = () => new URLSearchParams(String(fetchApi.mock.calls[fetchApi.mock.calls.length - 1][0]).split('?')[1])

  beforeEach(() => {
    fetchApi.mockReset()
    localStorage.clear()
  })

  async function mountAt(url: string) {
    const wrapper = mountWithPlugins(RecentPaymentsView, { initialRoute: url })
    const router = wrapper.vm.$router as Router
    await router.isReady()
    await flushPromises()
    return wrapper
  }

  it('shows today and the days before as two groups, asking the server for 7 days', async () => {
    const now = new Date()
    const earlier = new Date()
    earlier.setHours(0, 0, 0, 0)
    earlier.setHours(-2)
    fetchApi.mockResolvedValue({
      payments: [
        { ...payment('p-today', 'vipps', null), paid_at: now.toISOString() },
        { ...payment('p-earlier', 'vipps', null), paid_at: earlier.toISOString() },
      ],
    })
    const wrapper = await mountAt('/admin')

    const from = new Date()
    from.setHours(0, 0, 0, 0)
    from.setDate(from.getDate() - 7)
    expect(lastParams().get('since')).toBe(isoDay(from))
    expect(lastParams().get('until')).toBeNull()

    const groups = wrapper.findAll('[data-testid^="payment-group-"]')
    expect(groups.map((g) => g.attributes('data-testid'))).toEqual(['payment-group-today', 'payment-group-last7'])
    expect(groups[0].text()).toContain('recentPayments.group.today')
    expect(groups[1].text()).toContain('recentPayments.group.last7')
    expect(groups[0].findAll('li')).toHaveLength(1)
    expect(groups[1].findAll('li')).toHaveLength(1)
    expect(wrapper.find('[data-testid="clear-range"]').exists()).toBe(false)
  })

  it('hides a group with nothing in it', async () => {
    fetchApi.mockResolvedValue({ payments: [{ ...payment('p-today', 'vipps', null), paid_at: new Date().toISOString() }] })
    const wrapper = await mountAt('/admin')
    expect(wrapper.findAll('[data-testid^="payment-group-"]').map((g) => g.attributes('data-testid'))).toEqual(['payment-group-today'])
  })

  it('shows a chosen range as one list, and clearing it returns to the groups', async () => {
    fetchApi.mockResolvedValue({ payments: [{ ...payment('p-old', 'vipps', null), paid_at: '2026-06-01T10:00:00Z' }] })
    const wrapper = await mountAt('/admin')

    const [since, until] = wrapper.findAll('input[type="date"]')
    await since.setValue('2026-06-01')
    await until.setValue('2026-06-30')
    await since.trigger('change')
    await flushPromises()

    expect(lastParams().get('since')).toBe('2026-06-01')
    expect(lastParams().get('until')).toBe('2026-06-30')
    expect(lastParams().get('limit')).toBe('200')
    expect(wrapper.findAll('[data-testid^="payment-group-"]').map((g) => g.attributes('data-testid'))).toEqual(['payment-group-range'])
    expect(wrapper.find('[data-testid="clear-range"]').classes()).toContain('min-h-11')

    await wrapper.find('[data-testid="clear-range"]').trigger('click')
    await flushPromises()
    expect(lastParams().get('until')).toBeNull()
    expect(lastParams().get('since')).not.toBe('2026-06-01')
    expect(wrapper.find('[data-testid="payment-group-range"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="clear-range"]').exists()).toBe(false)
  })

  it('says there is nothing in the last 7 days', async () => {
    fetchApi.mockResolvedValue({ payments: [] })
    const wrapper = await mountAt('/admin')
    expect(wrapper.text()).toContain('recentPayments.emptyRecent')
  })

  it('says when the newest payments are all that is shown', async () => {
    fetchApi.mockResolvedValue({ payments: [payment('p1', 'vipps', null)], has_more: true })
    const wrapper = await mountAt('/admin')
    expect(wrapper.find('[data-testid="truncated-note"]').exists()).toBe(true)

    fetchApi.mockResolvedValue({ payments: [payment('p1', 'vipps', null)], has_more: false })
    await wrapper.find('button[aria-label="recentPayments.refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="truncated-note"]').exists()).toBe(false)
  })
})

describe('RecentPaymentsView load failure', () => {
  beforeEach(() => {
    fetchApi.mockReset()
    localStorage.clear()
  })

  async function mountFailing() {
    fetchApi.mockRejectedValue(new Error('boom'))
    const wrapper = mountWithPlugins(RecentPaymentsView)
    await flushPromises()
    return wrapper
  }

  it('shows an error with a retry, not the empty-list message', async () => {
    const wrapper = await mountFailing()
    expect(wrapper.find('[data-testid="load-error"]').text()).toContain('recentPayments.loadError')
    expect(wrapper.text()).not.toContain('recentPayments.emptyRecent')
    expect(wrapper.find('[data-testid="load-retry"]').classes()).toContain('min-h-11')
  })

  it('retries, and clears the error when it works', async () => {
    const wrapper = await mountFailing()
    fetchApi.mockResolvedValue({ payments: [payment('p1', 'vipps', null)] })
    await wrapper.find('[data-testid="load-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="load-error"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="payment-method-chip"]')).toHaveLength(1)
  })

  it('shows the error above rows kept from an earlier load', async () => {
    fetchApi.mockResolvedValue({ payments: [payment('p1', 'vipps', null)] })
    const wrapper = mountWithPlugins(RecentPaymentsView)
    await flushPromises()
    expect(wrapper.find('[data-testid="load-error"]').exists()).toBe(false)

    fetchApi.mockRejectedValue(new Error('boom'))
    await wrapper.find('button[aria-label="recentPayments.refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="load-error"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="payment-method-chip"]')).toHaveLength(1)
  })
})

describe('RecentPaymentsView provider links', () => {
  beforeEach(() => {
    fetchApi.mockReset()
    localStorage.clear()
  })

  async function mountWith(response: Record<string, unknown>) {
    fetchApi.mockResolvedValue({ payments: [], ...response })
    const wrapper = mountWithPlugins(RecentPaymentsView)
    await flushPromises()
    return wrapper
  }

  const providers = (wrapper: Awaited<ReturnType<typeof mountWith>>) =>
    wrapper.findAll('[data-testid="provider-links"] a')

  const STRIPE = 'https://dashboard.stripe.com/payments'
  const VIPPS = 'https://portal.vippsmobilepay.com/123456/home'

  it('links to each configured provider, in a new tab, from the URL the server sent', async () => {
    const links = providers(await mountWith({ provider_links: { stripe: STRIPE, vipps: VIPPS } }))
    expect(links.map((a) => a.attributes('data-provider'))).toEqual(['stripe', 'vipps'])
    expect(links.map((a) => a.attributes('href'))).toEqual([STRIPE, VIPPS])
    for (const a of links) {
      expect(a.attributes('target')).toBe('_blank')
      expect(a.attributes('rel')).toBe('noopener noreferrer')
      expect(a.classes()).toContain('min-h-11')
    }
    expect(links.map((a) => a.text())).toEqual([
      'recentPayments.providerLinks.stripe',
      'recentPayments.providerLinks.vipps',
    ])
  })

  it('shows only the provider the club has configured', async () => {
    const stripeOnly = providers(await mountWith({ provider_links: { stripe: STRIPE } }))
    expect(stripeOnly.map((a) => a.attributes('data-provider'))).toEqual(['stripe'])

    const vippsOnly = providers(await mountWith({ provider_links: { vipps: VIPPS } }))
    expect(vippsOnly.map((a) => a.attributes('data-provider'))).toEqual(['vipps'])
  })

  it('shows no links when the server sent none', async () => {
    expect((await mountWith({})).find('[data-testid="provider-links"]').exists()).toBe(false)
    expect((await mountWith({ provider_links: {} })).find('[data-testid="provider-links"]').exists()).toBe(false)
  })

  it('wraps on narrow screens', async () => {
    const wrapper = await mountWith({ provider_links: { stripe: STRIPE, vipps: VIPPS } })
    expect(wrapper.find('[data-testid="provider-links"]').classes()).toContain('flex-wrap')
  })
})
