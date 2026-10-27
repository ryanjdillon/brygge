import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import HarborQrPrintView from '@/views/admin/HarborQrPrintView.vue'

vi.mock('qrcode', () => ({
  default: { toString: vi.fn(async (url: string) => `<svg data-url="${url}"></svg>`) },
}))

vi.mock('lucide-vue-next', () => ({ Printer: { template: '<span />' } }))

type T = { id: string; label: string; unit_amount: number; unit_label: string; fixed_units: boolean; archived: boolean }

const target = (id: string, over: Partial<T> = {}) => ({
  id,
  label: `Target ${id}`,
  unit_amount: 300,
  unit_label: 'natt',
  fixed_units: false,
  archived: false,
  public_url: `https://club.example.invalid/qr/${id}`,
  short_url: `https://club.example.invalid/q/${id}-dock`,
  ...over,
})

function mockTargets(list: ReturnType<typeof target>[]) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: true, json: async () => list })),
  )
}

async function mountAt(query: string, club = { name: 'Testklubben', hasLogo: true }) {
  const wrapper = mountWithPlugins(HarborQrPrintView, {
    initialRoute: `/admin/harbor/payment-targets/print?${query}`,
    piniaOptions: { initialState: { club } },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('HarborQrPrintView', () => {
  beforeEach(() => {
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

  it('renders a single card at the cell size of the chosen layout', async () => {
    mockTargets([target('a')])
    const wrapper = await mountAt('ids=a&per=6')
    const card = wrapper.get('[data-testid="qr-card"]')
    expect(card.attributes('style')).toContain('width: 88.3mm')
    expect(card.attributes('style')).toContain('height: 80mm')
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
    const fixedPage = await mountAt('ids=a,b&per=2')
    expect(fixedPage.get('[data-testid="instructions"]').text()).toBe('harborTargets.print.instructions')

    mockTargets([target('a', { fixed_units: true })])
    const allFixed = await mountAt('ids=a')
    expect(allFixed.get('[data-testid="instructions"]').text()).toBe('harborTargets.print.instructionsFixed')
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
})
