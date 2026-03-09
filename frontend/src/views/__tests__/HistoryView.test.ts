import { describe, it, expect, vi } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import HistoryView from '@/views/HistoryView.vue'

vi.mock('lucide-vue-next', () => ({
  Landmark: { template: '<span data-icon="landmark" />' },
  Award: { template: '<span data-icon="award" />' },
  Users: { template: '<span data-icon="users" />' },
  Anchor: { template: '<span data-icon="anchor" />' },
}))

describe('HistoryView', () => {
  it('renders without errors', () => {
    const wrapper = mountWithPlugins(HistoryView)
    expect(wrapper.exists()).toBe(true)
  })

  it('renders history title', () => {
    const wrapper = mountWithPlugins(HistoryView)
    expect(wrapper.find('h1').text()).toBe('history.title')
  })

  it('renders timeline milestones', () => {
    const wrapper = mountWithPlugins(HistoryView)
    expect(wrapper.text()).toContain('1952')
    expect(wrapper.text()).toContain('1968')
    expect(wrapper.text()).toContain('1985')
    expect(wrapper.text()).toContain('2010')
  })

  it('renders milestone titles', () => {
    const wrapper = mountWithPlugins(HistoryView)
    expect(wrapper.text()).toContain('history.founded')
    expect(wrapper.text()).toContain('history.clubhouse')
    expect(wrapper.text()).toContain('history.expansion')
    expect(wrapper.text()).toContain('history.modernisation')
  })
})
