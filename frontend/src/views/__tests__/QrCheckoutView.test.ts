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

async function mountIn(code: string, providers: string[] = target.providers) {
  locale.value = code
  const fetchMock = vi.fn(async (url: string) => {
    if (url.endsWith('/pay')) return { ok: true, json: async () => ({ redirect_url: '' }) }
    return { ok: true, json: async () => ({ ...target, providers }) }
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

  it('says payment is unavailable without a provider', async () => {
    const { wrapper } = await mountIn('en', [])
    expect(shown(wrapper)).toEqual([])
    expect(wrapper.get('[data-testid="qr-unavailable"]').text()).toBe('qr.unavailable')
  })
})
