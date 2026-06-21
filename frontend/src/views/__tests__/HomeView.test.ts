import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import HomeView from '@/views/HomeView.vue'

describe('HomeView', () => {
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

  it('renders the Bli medlem section pointing at the club mailbox', () => {
    const wrapper = mountWithPlugins(HomeView)
    expect(wrapper.text()).toContain('home.join.title')
    expect(wrapper.find('a[href^="mailto:post@"]').exists()).toBe(true)
  })
})
