import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import HarborQrPrintView from '@/views/admin/HarborQrPrintView.vue'
import { minQrPx } from '@/lib/qrSheet'

vi.mock('qrcode', () => ({
  default: { toString: vi.fn(async (url: string) => `<svg data-url="${url}"></svg>`) },
}))

// jsdom lays nothing out, so the fit measurement is replaced; by default
// every card fits.
const { measureQrCard } = vi.hoisted(() => ({
  measureQrCard: vi.fn<(card: HTMLElement, minQrPx: number) => { scale: number; overflow: boolean }>(),
}))
vi.mock('@/lib/qrCardMeasure', () => ({ measureQrCard }))

vi.mock('lucide-vue-next', () => ({ Printer: { template: '<span />' } }))

// Each locale's instruction strings are tagged with its code so a test can
// tell which language a header line came from.
vi.mock('@/i18n', () => {
  const codes = ['nb', 'nn', 'en', 'de', 'fr', 'nl', 'it', 'pl']
  return {
    LOCALE_OPTIONS: codes.map((code) => ({ code, label: `name-${code}` })),
    i18n: {
      global: {
        getLocaleMessage: (code: string) => ({
          harborTargets: { print: { instructions: `${code}: scan`, instructionsFixed: `${code}: scan fixed` } },
        }),
      },
    },
  }
})

type T = {
  id: string
  label: string
  unit_amount: number
  unit_label: string
  fixed_units: boolean
  archived: boolean
  label_translations: Record<string, string>
}

const target = (id: string, over: Partial<T> = {}) => ({
  id,
  label: `Target ${id}`,
  unit_amount: 300,
  unit_label: 'natt',
  fixed_units: false,
  archived: false,
  public_url: `https://club.example.invalid/qr/${id}`,
  short_url: `https://club.example.invalid/q/${id}-dock`,
  label_translations: {} as Record<string, string>,
  ...over,
})

function mockTargets(list: ReturnType<typeof target>[]) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: true, json: async () => list })),
  )
}

async function mountAt(
  query: string,
  club: { name: string; hasLogo: boolean; defaultLanguage?: string } = { name: 'Testklubben', hasLogo: true },
) {
  const wrapper = mountWithPlugins(HarborQrPrintView, {
    initialRoute: `/admin/harbor/payment-targets/print?${query}`,
    piniaOptions: { initialState: { club } },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

const headerLines = (wrapper: Awaited<ReturnType<typeof mountAt>>) =>
  wrapper
    .get('[data-testid="qr-page-header"]')
    .findAll('[data-testid="instruction-line"]')
    .map((l) => l.text())

describe('HarborQrPrintView', () => {
  beforeEach(() => {
    localStorage.clear()
    measureQrCard.mockReset()
    measureQrCard.mockReturnValue({ scale: 1, overflow: false })
    // The test router has no print route; silence only its no-match warning.
    const warn = console.warn
    vi.spyOn(console, 'warn').mockImplementation((...args: unknown[]) => {
      if (String(args[0]).includes('No match found')) return
      warn(...args)
    })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('paginates by cards-per-page with the header once per page', async () => {
    mockTargets(['a', 'b', 'c', 'd', 'e'].map((id) => target(id)))
    const wrapper = await mountAt('ids=a,b,c,d,e&per=4')
    const pages = wrapper.findAll('[data-testid="qr-page"]')
    expect(pages).toHaveLength(2)
    expect(pages[0].findAll('[data-testid="qr-card"]')).toHaveLength(4)
    expect(pages[1].findAll('[data-testid="qr-card"]')).toHaveLength(1)
    expect(wrapper.findAll('[data-testid="qr-page-header"]')).toHaveLength(2)
    expect(pages[0].text()).toContain('Testklubben')
  })

  it('keeps a single card at its full-page cell size, in one centred track', async () => {
    mockTargets([target('a')])
    const wrapper = await mountAt('ids=a&per=6')
    const style = wrapper.get('.qr-page__grid').attributes('style')
    expect(style).toContain('grid-template-columns: repeat(1, calc((100% - 2 * 6mm) / 3))')
    expect(style).toContain('grid-template-rows: repeat(1, calc((100% - 1 * 6mm) / 2))')
  })

  it('shows the logo only when the club has one', async () => {
    mockTargets([target('a')])
    const withLogo = await mountAt('ids=a')
    expect(withLogo.find('[data-testid="club-logo"]').exists()).toBe(true)

    mockTargets([target('a')])
    const without = await mountAt('ids=a', { name: 'Testklubben', hasLogo: false })
    expect(without.find('[data-testid="club-logo"]').exists()).toBe(false)
  })

  it('prints the unit only for per-unit targets', async () => {
    mockTargets([target('a'), target('b', { fixed_units: true, unit_amount: 1600 })])
    const wrapper = await mountAt('ids=a,b')
    const prices = wrapper.findAll('[data-testid="qr-card-price"]').map((p) => p.text())
    expect(prices[0]).toBe('300 kr / natt')
    expect(prices[1]).toMatch(/^1\s600 kr$/)
  })

  it('keeps requested order and drops archived and unknown ids', async () => {
    mockTargets([target('a'), target('b'), target('c', { archived: true })])
    const wrapper = await mountAt('ids=b,zzz,c,a')
    const titles = wrapper.findAll('[data-testid="qr-card"] h2').map((h) => h.text())
    expect(titles).toEqual(['Target b', 'Target a'])
  })

  it('shows an empty state when nothing valid is selected', async () => {
    mockTargets([target('a', { archived: true })])
    const wrapper = await mountAt('ids=a')
    expect(wrapper.find('[data-testid="empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="qr-page"]').exists()).toBe(false)
  })

  it('drops the quantity step from the instructions on an all-fixed page', async () => {
    mockTargets([target('a', { fixed_units: true }), target('b')])
    const fixedPage = await mountAt('ids=a,b&per=2&langs=nb')
    expect(headerLines(fixedPage)).toEqual(['nb: scan'])

    mockTargets([target('a', { fixed_units: true })])
    const allFixed = await mountAt('ids=a&langs=nb')
    expect(headerLines(allFixed)).toEqual(['nb: scan fixed'])
  })

  it('falls back to 4 per page and enables printing once QRs are ready', async () => {
    mockTargets(['a', 'b', 'c', 'd', 'e'].map((id) => target(id)))
    const wrapper = await mountAt('ids=a,b,c,d,e&per=5')
    expect(wrapper.findAll('[data-testid="qr-page"]')).toHaveLength(2)
    expect(wrapper.get('[data-testid="print-button"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="qr-card"] img').attributes('src')).toMatch(/^data:image\/svg\+xml;charset=utf-8,/)
  })

  it('prints the short address under a QR that still encodes the token url', async () => {
    mockTargets([target('a')])
    const wrapper = await mountAt('ids=a')
    expect(wrapper.get('[data-testid="qr-card-short-url"]').text()).toBe('club.example.invalid/q/a-dock')
    const src = decodeURIComponent(wrapper.get('[data-testid="qr-card"] img').attributes('src')!)
    expect(src).toContain('data-url="https://club.example.invalid/qr/a"')
  })

  describe('text fit', () => {
    it('sets each card to the measured scale and flags none when all fit', async () => {
      mockTargets([target('a'), target('b')])
      const wrapper = await mountAt('ids=a,b')
      expect(measureQrCard).toHaveBeenCalled()
      const cards = wrapper.findAll('[data-testid="qr-card"]')
      for (const card of cards) expect((card.element as HTMLElement).style.getPropertyValue('--fit')).toBe('1')
      expect(wrapper.find('[data-testid="qr-card-overflow"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="overflow-count"]').exists()).toBe(false)
    })

    it('flags the cards that overflow and counts them in the toolbar', async () => {
      measureQrCard.mockImplementation((card) =>
        card.dataset.targetId === 'b' ? { scale: 0.6, overflow: true } : { scale: 0.85, overflow: false },
      )
      mockTargets([target('a'), target('b')])
      const wrapper = await mountAt('ids=a,b')
      const cards = wrapper.findAll('[data-testid="qr-card"]')
      expect((cards[0].element as HTMLElement).style.getPropertyValue('--fit')).toBe('0.85')
      expect((cards[1].element as HTMLElement).style.getPropertyValue('--fit')).toBe('0.6')
      expect(cards[0].find('[data-testid="qr-card-overflow"]').exists()).toBe(false)
      expect(cards[1].find('[data-testid="qr-card-overflow"]').exists()).toBe(true)
      expect(wrapper.get('[data-testid="overflow-count"]').text()).toBe('harborTargets.print.overflowCount')
    })

    it('uses the larger minimum QR size on 2-up cards', async () => {
      mockTargets([target('a')])
      await mountAt('ids=a&per=2')
      const sizes = measureQrCard.mock.calls.map(([, min]) => min)
      expect(sizes[sizes.length - 1]).toBeCloseTo(minQrPx(2))
    })
  })

  describe('languages', () => {
    const translated = () =>
      target('a', { label_translations: { nb: 'Gjest', en: 'Guest', de: 'Gast', fr: 'Invité', pl: '' } })

    it('defaults to nb, en, de, nl and writes the choice to the URL', async () => {
      mockTargets([target('a')])
      const wrapper = await mountAt('ids=a')
      expect(headerLines(wrapper)).toEqual(['nb: scan', 'en: scan', 'de: scan', 'nl: scan'])
      expect(wrapper.vm.$route.query.langs).toBe('nb,en,de,nl')
    })

    it('puts the club language first, larger, and always selected', async () => {
      mockTargets([target('a')])
      const wrapper = await mountAt('ids=a&langs=en,nl', { name: 'Testklubben', hasLogo: false, defaultLanguage: 'de' })
      const lines = wrapper.findAll('[data-testid="instruction-line"]')
      expect(lines.map((l) => l.text())).toEqual(['de: scan', 'en: scan', 'nl: scan'])
      expect(lines[0].classes()).toContain('qr-page__instr-main')
      expect(lines[1].classes()).toContain('qr-page__instr-other')
      const clubBox = wrapper.get('[data-testid="lang-de"] input')
      expect((clubBox.element as HTMLInputElement).checked).toBe(true)
      expect(clubBox.attributes('disabled')).toBeDefined()
      expect(wrapper.text().match(/Testklubben/g)).toHaveLength(1)
    })

    it('lets the query win over the remembered choice', async () => {
      localStorage.setItem('brygge-qr-print-langs', 'nb,pl')
      mockTargets([target('a')])
      const fromQuery = await mountAt('ids=a&langs=fr,nb')
      expect(headerLines(fromQuery)).toEqual(['nb: scan', 'fr: scan'])

      mockTargets([target('a')])
      const fromStorage = await mountAt('ids=a')
      expect(headerLines(fromStorage)).toEqual(['nb: scan', 'pl: scan'])
    })

    it('remembers a toggled language and reflects it in the URL', async () => {
      mockTargets([target('a')])
      const wrapper = await mountAt('ids=a&langs=nb,en')
      await wrapper.get('[data-testid="lang-fr"] input').trigger('change')
      await flushPromises()
      expect(localStorage.getItem('brygge-qr-print-langs')).toBe('nb,en,fr')
      expect(wrapper.vm.$route.query.langs).toBe('nb,en,fr')
      expect(headerLines(wrapper)).toEqual(['nb: scan', 'en: scan', 'fr: scan'])
    })

    it('falls back to the defaults when storage is unavailable', async () => {
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('blocked')
      })
      const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new Error('blocked')
      })
      mockTargets([target('a')])
      const wrapper = await mountAt('ids=a')
      expect(headerLines(wrapper)).toEqual(['nb: scan', 'en: scan', 'de: scan', 'nl: scan'])

      await wrapper.get('[data-testid="lang-pl"] input').trigger('change')
      await flushPromises()
      expect(setItem).toHaveBeenCalled()
      expect(headerLines(wrapper)).toContain('pl: scan')
    })

    it('shows only selected translations the target has, without the club language', async () => {
      mockTargets([translated()])
      const defaults = await mountAt('ids=a')
      expect(defaults.get('[data-testid="qr-card-translations"]').text()).toBe('Guest · Gast')

      mockTargets([translated()])
      const picked = await mountAt('ids=a&langs=nb,fr,pl,nl')
      expect(picked.get('[data-testid="qr-card-translations"]').text()).toBe('Invité')

      mockTargets([translated()])
      const none = await mountAt('ids=a&langs=nb,nl')
      expect(none.find('[data-testid="qr-card-translations"]').exists()).toBe(false)
    })
  })
})
