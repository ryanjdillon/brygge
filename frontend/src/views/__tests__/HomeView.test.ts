import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import { setJoinStatus, TEST_JOIN_PRICE } from '@/test/joinStatus'
import HomeView from '@/views/HomeView.vue'

vi.mock('@/composables/useJoinStatus', async () => (await import('@/test/joinStatus')).joinStatusModule)

describe('HomeView', () => {
  beforeEach(() => setJoinStatus())

  it('renders hero section with title', () => {
    const wrapper = mountWithPlugins(HomeView)
    expect(wrapper.find('h1').text()).toBe('home.welcomeWith')
    expect(wrapper.text()).toContain('home.tagline')
  })

  it('renders feature cards', () => {
    const wrapper = mountWithPlugins(HomeView)

    expect(wrapper.text()).toContain('home.featureHarbor')
    expect(wrapper.text()).toContain('home.featureWeather')
    expect(wrapper.text()).toContain('home.featurePricing')
    expect(wrapper.text()).toContain('home.featureCalendar')

    expect(wrapper.text()).toContain('home.featureHarborDesc')
    expect(wrapper.text()).toContain('home.featureWeatherDesc')
    expect(wrapper.text()).toContain('home.featurePricingDesc')
    expect(wrapper.text()).toContain('home.featureCalendarDesc')
  })

  it('renders the hero CTA as a login button', () => {
    const wrapper = mountWithPlugins(HomeView)
    const ctaLink = wrapper.find('a[href="/login"]')
    expect(ctaLink.exists()).toBe(true)
    expect(ctaLink.text()).toContain('home.ctaLogin')
  })

  it('offers the join form from the card and the hero while joining is open', () => {
    setJoinStatus({ open: true, price: TEST_JOIN_PRICE, year: 2031 })
    const wrapper = mountWithPlugins(HomeView)
    const card = wrapper.find('[data-testid="home-join"]')
    expect(card.text()).toContain('join.title')
    expect(card.find('a').attributes('href')).toBe('/join')
    expect(card.find('a').text()).toContain('nav.join')
    expect(card.find('[data-testid="home-join-price"]').text()).toBe('join.form.price')
    expect(card.find('a[href="/contact"]').exists()).toBe(false)
    expect(wrapper.find('a[href^="mailto:"]').exists()).toBe(false)

    const hero = wrapper.find('[data-testid="hero-join"]')
    expect(hero.attributes('href')).toBe('/join')
    expect(hero.text()).toContain('home.ctaJoin')
  })

  it('leaves the price out of the open card when there is none', () => {
    setJoinStatus({ open: true })
    const card = mountWithPlugins(HomeView).find('[data-testid="home-join"]')
    expect(card.find('a').attributes('href')).toBe('/join')
    expect(card.find('[data-testid="home-join-price"]').exists()).toBe(false)
  })

  it('points the card at the contact page and hides the hero join while closed', () => {
    const wrapper = mountWithPlugins(HomeView)
    const card = wrapper.find('[data-testid="home-join"]')
    expect(card.text()).toContain('join.closed.body')
    expect(card.find('a').attributes('href')).toBe('/contact')
    expect(card.find('a[href="/join"]').exists()).toBe(false)
    expect(wrapper.find('a[href="/join"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="hero-join"]').exists()).toBe(false)
    expect(wrapper.find('a[href^="mailto:"]').exists()).toBe(false)
  })
})
