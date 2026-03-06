import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import HomeView from '@/views/HomeView.vue'

vi.mock('lucide-vue-next', () => ({
  Calendar: { template: '<span data-icon="calendar" />' },
  CloudSun: { template: '<span data-icon="cloudsun" />' },
  Ship: { template: '<span data-icon="ship" />' },
  Users: { template: '<span data-icon="users" />' },
}))

describe('HomeView', () => {
  it('renders hero section with title', () => {
    const wrapper = mountWithPlugins(HomeView)
    expect(wrapper.find('h1').text()).toBe('home.welcome')
    expect(wrapper.text()).toContain('home.tagline')
  })

  it('renders feature cards', () => {
    const wrapper = mountWithPlugins(HomeView)

    expect(wrapper.text()).toContain('home.featureCalendar')
    expect(wrapper.text()).toContain('home.featureWeather')
    expect(wrapper.text()).toContain('home.featureBookings')
    expect(wrapper.text()).toContain('home.featureMembers')

    expect(wrapper.text()).toContain('home.featureCalendarDesc')
    expect(wrapper.text()).toContain('home.featureWeatherDesc')
    expect(wrapper.text()).toContain('home.featureBookingsDesc')
    expect(wrapper.text()).toContain('home.featureMembersDesc')
  })

  it('renders CTA button linking to /join', () => {
    const wrapper = mountWithPlugins(HomeView)
    const ctaLink = wrapper.find('a[href="/join"]')
    expect(ctaLink.exists()).toBe(true)
    expect(ctaLink.text()).toBe('home.ctaJoin')
  })
})
