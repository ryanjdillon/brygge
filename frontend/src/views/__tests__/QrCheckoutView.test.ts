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
}

async function mountIn(code: string) {
  locale.value = code
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: true, json: async () => target })),
  )
  const wrapper = mountWithPlugins(QrCheckoutView, { initialRoute: '/' })
  await flushPromises()
  return wrapper
}

describe('QrCheckoutView', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows the name in the visitor language with the club-language name under it', async () => {
    const wrapper = await mountIn('de')
    expect(wrapper.get('[data-testid="qr-heading"]').text()).toBe('Gastliegeplatz')
    expect(wrapper.get('[data-testid="qr-club-label"]').text()).toBe('Gjestehavn')
  })

  it('falls back to the label when there is no translation for the locale', async () => {
    const wrapper = await mountIn('fr')
    expect(wrapper.get('[data-testid="qr-heading"]').text()).toBe('Gjestehavn')
    expect(wrapper.find('[data-testid="qr-club-label"]').exists()).toBe(false)
  })
})
