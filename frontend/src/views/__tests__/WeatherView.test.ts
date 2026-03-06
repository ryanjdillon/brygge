import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import WeatherView from '@/views/WeatherView.vue'

vi.mock('lucide-vue-next', () => ({
  Wind: { template: '<span data-icon="wind" />' },
  Waves: { template: '<span data-icon="waves" />' },
  Thermometer: { template: '<span data-icon="thermometer" />' },
  Droplets: { template: '<span data-icon="droplets" />' },
}))

vi.mock('@/composables/useWeather', () => ({
  useWeather: () => ({
    data: { value: null },
    isLoading: { value: false },
    isError: { value: false },
  }),
}))

describe('WeatherView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(WeatherView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders weather heading', () => {
    const wrapper = mountWithPlugins(WeatherView)
    expect(wrapper.find('h1').text()).toBe('weather.title')
  })

  it('renders attribution text', () => {
    const wrapper = mountWithPlugins(WeatherView)
    expect(wrapper.text()).toContain('weather.attribution')
  })
})
