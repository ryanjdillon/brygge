import { describe, it, expect } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import Footer from '@/components/layout/Footer.vue'

describe('Footer', () => {
  it('renders copyright with current year', () => {
    const wrapper = mountWithPlugins(Footer)
    const year = new Date().getFullYear()
    expect(wrapper.text()).toContain(`${year}`)
    expect(wrapper.text()).toContain('Brygge')
  })

  it('renders contact link', () => {
    const wrapper = mountWithPlugins(Footer)
    const contactLink = wrapper.find('a[href="/contact"]')
    expect(contactLink.exists()).toBe(true)
    expect(contactLink.text()).toContain('nav.contact')
  })
})
