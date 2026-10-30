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

// The chips come from the groups the server sends with the payments (BRY-522).
const group = (slug: string, extra: Record<string, unknown> = {}) => ({
  slug,
  name: null,
  label_key: slug,
  icon: 'circle-dollar-sign',
  builtin: null,
  ...extra,
})
const GROUPS = [
  group('guest', { icon: 'sailboat' }),
  group('motorhome', { icon: 'van' }),
  group('slipping', { name: 'Haul-out', label_key: null, icon: 'forklift' }),
  group('faktura', { icon: 'file-text', builtin: 'faktura' }),
  group('other', { builtin: 'other' }),
]

describe('RecentPaymentsView kind filter', () => {
  beforeEach(() => {
    fetchApi.mockReset()
    fetchApi.mockResolvedValue({ payments: [], groups: GROUPS })
    localStorage.clear()
  })

  const lastParams = () => new URLSearchParams(String(fetchApi.mock.calls[fetchApi.mock.calls.length - 1][0]).split('?')[1])
  const requestedKind = () => lastParams().get('kind')
  const pressed = (wrapper: ReturnType<typeof mountWithPlugins>) =>
    wrapper.findAll('[data-testid="kind-filters"] button').filter((b) => b.attributes('aria-pressed') === 'true')
  const pressedIds = (wrapper: ReturnType<typeof mountWithPlugins>) => pressed(wrapper).map((b) => b.attributes('data-filter'))

  async function mountAt(url: string, piniaOptions?: Record<string, unknown>) {
    const wrapper = mountWithPlugins(RecentPaymentsView, { initialRoute: url, piniaOptions })
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

  it('lists all first, then the groups in the order the server sent, as targets of at least 44px', async () => {
    const { wrapper } = await mountAt('/admin')
    const buttons = wrapper.findAll('[data-testid="kind-filters"] button')
    expect(buttons.map((b) => b.attributes('data-filter'))).toEqual(['all', 'guest', 'motorhome', 'slipping', 'faktura', 'other'])
    for (const b of buttons) {
      expect(b.classes()).toContain('min-h-11')
      expect(b.classes()).toContain('truncate')
      expect(b.classes()).toContain('max-w-full')
    }
    for (const input of wrapper.findAll('input[type="date"]')) expect(input.classes()).toContain('min-h-11')
  })

  it('labels a chip with the name the board typed, or the translation of a seeded key', async () => {
    const { wrapper } = await mountAt('/admin')
    const text = (slug: string) => wrapper.find(`[data-filter="${slug}"]`).text()
    expect(text('slipping')).toBe('Haul-out')
    expect(text('guest')).toBe('recentPayments.kind.guest')
    expect(text('other')).toBe('recentPayments.kind.other')
  })

  it('shows placeholders of chip height until the first answer, and no chip the server did not send', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchApi.mockReturnValue(new Promise((resolve) => (answer = resolve)))
    const wrapper = mountWithPlugins(RecentPaymentsView, { initialRoute: '/admin' })
    await flushPromises()
    expect(wrapper.findAll('[data-testid="kind-filters"] button').map((b) => b.attributes('data-filter'))).toEqual(['all'])
    const placeholders = wrapper.findAll('[data-testid="kind-filter-placeholder"]')
    expect(placeholders.length).toBeGreaterThan(0)
    for (const p of placeholders) expect(p.classes()).toContain('h-11')

    answer({ payments: [], groups: [group('guest')] })
    await flushPromises()
    expect(wrapper.findAll('[data-testid="kind-filter-placeholder"]')).toHaveLength(0)
    expect(wrapper.findAll('[data-testid="kind-filters"] button').map((b) => b.attributes('data-filter'))).toEqual(['all', 'guest'])
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

    await wrapper.find('[data-filter="guest"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['guest'])
    expect(requestedKind()).toBe('guest')

    await wrapper.find('[data-filter="motorhome"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-filter="slipping"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['guest', 'motorhome', 'slipping'])
    expect(requestedKind()?.split(',').sort()).toEqual(['guest', 'motorhome', 'slipping'])
    expect(router.currentRoute.value.query.kind).toBe('guest,motorhome,slipping')
    expect(router.currentRoute.value.query.q).toBe('keep')
    expect(replace).toHaveBeenCalled()
    expect(push).not.toHaveBeenCalled()
    expect(localStorage.getItem('recentPayments.kinds')).toBe('guest,motorhome,slipping')
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
    const { wrapper } = await mountAt('/admin?kind=guest,other')
    await wrapper.find('[data-filter="all"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['all'])
    expect(requestedKind()).toBeNull()

    await wrapper.find('[data-filter="other"]').trigger('click')
    await flushPromises()
    expect(pressedIds(wrapper)).toEqual(['other'])
  })

  it('restores the remembered filters when the URL has none', async () => {
    localStorage.setItem('recentPayments.kinds', 'faktura,other')
    const { router } = await mountAt('/admin')
    expect(requestedKind()?.split(',').sort()).toEqual(['faktura', 'other'])
    expect(router.currentRoute.value.query.kind).toBe('faktura,other')
  })

  it('lets the URL win over the remembered filters, and ignores junk in either', async () => {
    localStorage.setItem('recentPayments.kinds', 'faktura')
    await mountAt('/admin?kind=other')
    expect(requestedKind()).toBe('other')

    for (const junk of ['Bogus!', 'constructor,', '../x', '__proto__', 'a b']) {
      fetchApi.mockClear()
      localStorage.setItem('recentPayments.kinds', junk)
      const { router } = await mountAt(`/admin?kind=${encodeURIComponent(junk)}`)
      expect(requestedKind()).toBeNull()
      expect(router.currentRoute.value.query.kind).toBe('all')
    }
  })

  it('expands the old harbor chip to guest and motorhome, from a link or from storage', async () => {
    const { wrapper } = await mountAt('/admin?kind=harbor')
    expect(requestedKind()?.split(',').sort()).toEqual(['guest', 'motorhome'])
    expect(pressedIds(wrapper)).toEqual(['guest', 'motorhome'])

    fetchApi.mockClear()
    localStorage.setItem('recentPayments.kinds', 'harbor,faktura')
    const stored = await mountAt('/admin')
    expect(requestedKind()?.split(',').sort()).toEqual(['faktura', 'guest', 'motorhome'])
    expect(pressedIds(stored.wrapper)).toEqual(['guest', 'motorhome', 'faktura'])
  })

  it('drops a slug that is no group, and rewrites the URL and storage, then falls back to all', async () => {
    // merch and slipping-2 were chips once; this club has neither.
    const { wrapper, router } = await mountAt('/admin?kind=guest,merch,gone-group')
    expect(router.currentRoute.value.query.kind).toBe('guest')
    expect(localStorage.getItem('recentPayments.kinds')).toBe('guest')
    expect(pressedIds(wrapper)).toEqual(['guest'])
    expect(requestedKind()).toBe('guest')

    fetchApi.mockClear()
    const none = await mountAt('/admin?kind=merch')
    expect(none.router.currentRoute.value.query.kind).toBe('all')
    expect(localStorage.getItem('recentPayments.kinds')).toBe('all')
    expect(pressedIds(none.wrapper)).toEqual(['all'])
    expect(requestedKind()).toBeNull()
  })

  it('keeps the valid ids of a mixed list and lets all win', async () => {
    const { wrapper } = await mountAt('/admin?kind=guest,bogus!,guest')
    expect(pressedIds(wrapper)).toEqual(['guest'])
    fetchApi.mockClear()
    const withAll = await mountAt('/admin?kind=guest,all')
    expect(pressedIds(withAll.wrapper)).toEqual(['all'])
  })

  it('draws an unknown kind as the club\'s fallback group, renamed and re-iconed as it is', async () => {
    fetchApi.mockResolvedValue({
      payments: [payment('p1', 'vipps', null, 'removed-group')],
      groups: [group('guest'), group('other', { builtin: 'other', name: 'Diverse', label_key: null, icon: 'ticket' })],
    })
    const { wrapper } = await mountAt('/admin')
    const row = wrapper.find('li')
    expect(row.find('[data-testid="filter-group-icon"]').attributes('data-icon')).toBe('ticket')
    expect(row.text()).toContain('Diverse')
    expect(row.text()).not.toContain('recentPayments.kind.other')
  })

  it('draws each row with the icon and label of its group, an unknown kind as Annet', async () => {
    fetchApi.mockResolvedValue({
      payments: [
        payment('p1', 'vipps', null, 'slipping'),
        payment('p2', 'vipps', null, 'removed-group'),
      ],
      groups: GROUPS,
    })
    const { wrapper } = await mountAt('/admin')
    const rows = wrapper.findAll('li')
    expect(rows[0].find('[data-testid="filter-group-icon"]').attributes('data-icon')).toBe('forklift')
    expect(rows[0].text()).toContain('Haul-out')
    expect(rows[1].find('[data-testid="filter-group-icon"]').attributes('data-icon')).toBe('circle-dollar-sign')
    expect(rows[1].text()).toContain('recentPayments.kind.other')
  })

  it('draws an icon this build does not know as the fallback', async () => {
    fetchApi.mockResolvedValue({
      payments: [payment('p1', 'vipps', null, 'new-group')],
      groups: [group('new-group', { name: 'New', label_key: null, icon: 'from-a-newer-server' })],
    })
    const { wrapper } = await mountAt('/admin')
    const badge = wrapper.find('li [data-testid="filter-group-icon"]')
    expect(badge.exists()).toBe(true)
    expect(badge.classes().join(' ')).toContain('bg-gray-50')
  })

  it('links to the group settings for every viewer, as a 44px target', async () => {
    const { wrapper } = await mountAt('/admin', { initialState: { auth: { user: { roles: ['chair'] } } } })
    const link = wrapper.find('[data-testid="filters-link"]')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).toBe('/admin/harbor/recent-payments/filters')
    expect(link.classes()).toContain('min-h-11')
  })

  it('nudges those who may assign prices when some are in no group', async () => {
    fetchApi.mockResolvedValue({ payments: [], groups: GROUPS, unassigned_items: 3 })
    const writer = await mountAt('/admin', { initialState: { auth: { user: { roles: ['harbor_master'] } } } })
    const hint = writer.wrapper.find('[data-testid="unassigned-hint"]')
    expect(hint.exists()).toBe(true)
    expect(hint.text()).toContain('recentPayments.filters.unassignedHint')
    expect(hint.attributes('href')).toBe('/admin/harbor/recent-payments/filters')
    expect(hint.classes()).toContain('min-h-11')

    // A viewer who cannot assign gets no nudge, even if the server sent a count.
    const reader = await mountAt('/admin', { initialState: { auth: { user: { roles: ['chair'] } } } })
    expect(reader.wrapper.find('[data-testid="unassigned-hint"]').exists()).toBe(false)

    fetchApi.mockResolvedValue({ payments: [], groups: GROUPS, unassigned_items: 0 })
    const none = await mountAt('/admin', { initialState: { auth: { user: { roles: ['harbor_master'] } } } })
    expect(none.wrapper.find('[data-testid="unassigned-hint"]').exists()).toBe(false)
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
