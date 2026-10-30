import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import QrCheckoutView from '@/views/QrCheckoutView.vue'

const locale = vi.hoisted(() => ({ value: 'en' }))
vi.mock('@/i18n', () => ({ i18n: { global: { locale } } }))

vi.mock('lucide-vue-next', () => ({
  Loader2: { template: '<span />' },
  Minus: { template: '<span />' },
  Plus: { template: '<span />' },
  CreditCard: { template: '<span />' },
}))

const target = {
  label: 'Gjestehavn',
  location: '',
  kind: 'guest_slip',
  unit_label: 'natt',
  unit_amount: 300,
  currency: 'NOK',
  min_units: 1,
  max_units: 14,
  fixed_units: false,
  club_name: 'Testklubben',
  label_translations: { en: 'Guest harbour', de: 'Gastliegeplatz' },
  providers: ['vipps', 'stripe'],
}

async function mountIn(
  code: string,
  providers: string[] = target.providers,
  extra: Partial<typeof target> & { provider_min_amount?: Record<string, number> } = {},
  payResponse: { ok: boolean; status: number; body: unknown } = { ok: true, status: 200, body: { redirect_url: '' } },
) {
  locale.value = code
  const fetchMock = vi.fn(async (url: string) => {
    if (url.endsWith('/pay')) return { ok: payResponse.ok, status: payResponse.status, json: async () => payResponse.body }
    return { ok: true, status: 200, json: async () => ({ ...target, providers, ...extra }) }
  })
  vi.stubGlobal('fetch', fetchMock)
  const wrapper = mountWithPlugins(QrCheckoutView, { initialRoute: '/' })
  await flushPromises()
  return { wrapper, fetchMock }
}

const shown = (w: Awaited<ReturnType<typeof mountIn>>['wrapper']) =>
  w.findAll('[data-provider]').map((b) => b.attributes('data-provider'))

describe('QrCheckoutView', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows the name in the visitor language with the club-language name under it', async () => {
    const { wrapper } = await mountIn('de')
    expect(wrapper.get('[data-testid="qr-heading"]').text()).toBe('Gastliegeplatz')
    expect(wrapper.get('[data-testid="qr-club-label"]').text()).toBe('Gjestehavn')
  })

  it('falls back to the label when there is no translation for the locale', async () => {
    const { wrapper } = await mountIn('fr')
    expect(wrapper.get('[data-testid="qr-heading"]').text()).toBe('Gjestehavn')
    expect(wrapper.find('[data-testid="qr-club-label"]').exists()).toBe(false)
  })

  it('leads with card when both providers are configured', async () => {
    const { wrapper } = await mountIn('en')
    expect(shown(wrapper)).toEqual(['stripe', 'vipps'])
  })

  it('offers card alone on a Stripe-only deployment and posts it', async () => {
    const { wrapper, fetchMock } = await mountIn('en', ['stripe'])
    expect(shown(wrapper)).toEqual(['stripe'])
    await wrapper.get('[data-provider="stripe"]').trigger('click')
    await flushPromises()
    const call = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/pay')) as unknown as [string, RequestInit]
    expect(JSON.parse(String(call[1].body))).toMatchObject({ units: 1, provider: 'stripe' })
  })

  it('hides card while the total is under its minimum and shows it once units reach it', async () => {
    const { wrapper } = await mountIn('en', ['vipps', 'stripe'], { unit_amount: 1, provider_min_amount: { stripe: 3 } })
    expect(shown(wrapper)).toEqual(['vipps'])
    expect(wrapper.get('[data-testid="qr-card-minimum"]').text()).toBe('qr.cardMinimum')

    const more = wrapper.get('button[aria-label="qr.increase"]')
    await more.trigger('click')
    expect(shown(wrapper)).toEqual(['vipps'])
    await more.trigger('click')
    expect(shown(wrapper)).toEqual(['stripe', 'vipps'])
    expect(wrapper.find('[data-testid="qr-card-minimum"]').exists()).toBe(false)

    await wrapper.get('button[aria-label="qr.decrease"]').trigger('click')
    expect(shown(wrapper)).toEqual(['vipps'])
  })

  it('says the amount is too small when no provider takes it', async () => {
    const { wrapper } = await mountIn('en', ['stripe'], { unit_amount: 1, fixed_units: true, provider_min_amount: { stripe: 3 } })
    expect(shown(wrapper)).toEqual([])
    expect(wrapper.get('[data-testid="qr-below-minimum"]').text()).toBe('qr.belowMinimum')
    expect(wrapper.find('[data-testid="qr-unavailable"]').exists()).toBe(false)
  })

  it('says card was refused when the server refuses a total over the known minimum', async () => {
    const { wrapper } = await mountIn('en', ['stripe'], { unit_amount: 2.5, provider_min_amount: { stripe: 3 } }, {
      ok: false,
      status: 409,
      body: { error: 'amount_below_minimum' },
    })
    // 2 units = 5 kr is over the minimum the page knows, so quoting that
    // minimum would mislead: the provider applied a higher one of its own.
    await wrapper.get('button[aria-label="qr.increase"]').trigger('click')
    await wrapper.get('[data-provider="stripe"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('qr.cardRefused')
  })

  it('says the currency is unsupported when the server refuses it', async () => {
    const { wrapper } = await mountIn('en', ['vipps'], {}, {
      ok: false,
      status: 409,
      body: { error: 'unsupported_currency' },
    })
    await wrapper.get('[data-provider="vipps"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('qr.unsupportedCurrency')
  })

  it('says payment is unavailable without a provider', async () => {
    const { wrapper } = await mountIn('en', [])
    expect(shown(wrapper)).toEqual([])
    expect(wrapper.get('[data-testid="qr-unavailable"]').text()).toBe('qr.unavailable')
  })
})
