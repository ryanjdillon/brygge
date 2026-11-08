import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import { setJoinStatus, TEST_JOIN_PRICE } from '@/test/joinStatus'
import HomeView from '@/views/HomeView.vue'

vi.mock('@/composables/useJoinStatus', async () => (await import('@/test/joinStatus')).joinStatusModule)

// settled: true stands for a finished club load unless a test says otherwise.
function mountHome(club: Record<string, unknown> = {}) {
  return mountWithPlugins(HomeView, { piniaOptions: { initialState: { club: { settled: true, ...club } } } })
}

describe('HomeView', () => {
  beforeEach(() => setJoinStatus())

  it('renders hero section with title', () => {
    const wrapper = mountHome()
    expect(wrapper.find('h1').text()).toBe('home.welcomeWith')
  })

  it('falls back to the default tagline when the club has no description', () => {
    const wrapper = mountHome({ publicDescription: '' })
    expect(wrapper.get('[data-testid="hero-tagline"]').text()).toBe('home.tagline')
  })

  it("shows the club's own description in place of the tagline", () => {
    const wrapper = mountHome({ publicDescription: 'Boats, coffee and a long pier' })
    expect(wrapper.get('[data-testid="hero-tagline"]').text()).toBe('Boats, coffee and a long pier')
    expect(wrapper.text()).not.toContain('home.tagline')
  })

  it('holds the tagline back until the club load has settled', () => {
    const tagline = mountHome({ settled: false, publicDescription: '' }).get('[data-testid="hero-tagline"]')
    expect(tagline.text()).toBe('')
    expect(tagline.classes()).toContain('min-h-7')
  })

  it('falls back to the default tagline when the club load failed', () => {
    const wrapper = mountHome({ settled: true, failed: true, publicDescription: '' })
    expect(wrapper.get('[data-testid="hero-tagline"]').text()).toBe('home.tagline')
  })

  it('treats a blank description as none', () => {
    const wrapper = mountHome({ publicDescription: '   ' })
    expect(wrapper.get('[data-testid="hero-tagline"]').text()).toBe('home.tagline')
  })

  it('renders feature cards', () => {
    const wrapper = mountHome()

    expect(wrapper.text()).toContain('home.featureHarbor')
    expect(wrapper.text()).toContain('home.featureWeather')
    expect(wrapper.text()).toContain('home.featurePricing')
    expect(wrapper.text()).toContain('home.featureCalendar')

    expect(wrapper.text()).toContain('home.featureHarborDesc')
    expect(wrapper.text()).toContain('home.featureWeatherDesc')
    expect(wrapper.text()).toContain('home.featurePricingDesc')
    expect(wrapper.text()).toContain('home.featureCalendarDesc')
  })

  it('titles each section with a real heading', () => {
    const headings = mountHome().findAll('h2').map((h) => h.text())
    expect(headings).toContain('home.sectionVisit')
    expect(headings).toContain('home.sectionClub')
  })

  it('has no FAQ teaser', () => {
    const wrapper = mountHome()
    expect(wrapper.text()).not.toContain('faq.home.title')
    expect(wrapper.text()).not.toContain('faq.seeAll')
    expect(wrapper.find('details').exists()).toBe(false)
  })

  it('renders the hero CTA as a login button without an icon', () => {
    const wrapper = mountHome()
    const ctaLink = wrapper.find('a[href="/login"]')
    expect(ctaLink.exists()).toBe(true)
    expect(ctaLink.text()).toContain('home.ctaLogin')
    expect(ctaLink.find('svg').exists()).toBe(false)
  })

  it('makes the whole join card one link to the join form while joining is open', () => {
    setJoinStatus({ open: true, price: TEST_JOIN_PRICE, year: 2031 })
    const wrapper = mountHome()
    const card = wrapper.get('[data-testid="home-join"]')
    expect(card.element.tagName).toBe('A')
    expect(card.attributes('href')).toBe('/join')
    expect(card.text()).toContain('join.title')
    expect(card.text()).toContain('home.joinCardBody')
    expect(card.text()).toContain('home.joinCardCue')
    // No nested interactive element, and no price line.
    expect(card.findAll('a, button')).toHaveLength(0)
    expect(card.text()).not.toContain('join.form.price')
    // Named by its own content, read once: no label or description override.
    expect(card.attributes('aria-labelledby')).toBeUndefined()
    expect(card.attributes('aria-describedby')).toBeUndefined()
    expect(wrapper.find('a[href="/contact"][data-testid="home-join"]').exists()).toBe(false)
    expect(wrapper.find('a[href^="mailto:"]').exists()).toBe(false)

    const hero = wrapper.get('[data-testid="hero-join"]')
    expect(hero.attributes('href')).toBe('/join')
    expect(hero.text()).toContain('home.ctaJoin')
    expect(hero.find('svg').exists()).toBe(false)
  })

  it('points the card at the contact page and hides the hero join while closed', () => {
    const wrapper = mountHome()
    const card = wrapper.get('[data-testid="home-join"]')
    expect(card.element.tagName).toBe('A')
    expect(card.attributes('href')).toBe('/contact')
    expect(card.text()).toContain('join.closed.body')
    expect(card.text()).toContain('join.closed.contact')
    expect(card.text()).not.toContain('home.joinCardBody')
    expect(card.findAll('a, button')).toHaveLength(0)
    expect(wrapper.find('a[href="/join"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="hero-join"]').exists()).toBe(false)
    expect(wrapper.find('a[href^="mailto:"]').exists()).toBe(false)
  })

  it('gives the tiles and the join card a visible focus ring', () => {
    const wrapper = mountHome()
    for (const href of ['/harbor', '/weather']) {
      expect(wrapper.get(`a[href="${href}"]`).classes()).toContain('focus-visible:ring-2')
    }
    expect(wrapper.get('[data-testid="home-join"]').classes()).toContain('focus-visible:ring-2')
  })
})
