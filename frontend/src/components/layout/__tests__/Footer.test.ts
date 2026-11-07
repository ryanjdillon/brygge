import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import { setJoinStatus } from '@/test/joinStatus'
import Footer from '@/components/layout/Footer.vue'

vi.mock('@/composables/useJoinStatus', async () => (await import('@/test/joinStatus')).joinStatusModule)

describe('Footer', () => {
  beforeEach(() => setJoinStatus())

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

  it('links the public terms of sale and privacy pages', () => {
    const wrapper = mountWithPlugins(Footer)
    expect(wrapper.find('a[href="/salgsvilkar"]').text()).toContain('footer.terms')
    expect(wrapper.find('a[href="/privacy"]').text()).toContain('footer.privacy')
  })

  it('links the join form while joining is open', () => {
    setJoinStatus({ open: true })
    const join = mountWithPlugins(Footer).find('[data-testid="footer-join"]')
    expect(join.attributes('href')).toBe('/join')
    expect(join.text()).toBe('nav.join')
  })

  it('has no join link while joining is closed', () => {
    const wrapper = mountWithPlugins(Footer)
    expect(wrapper.find('a[href="/join"]').exists()).toBe(false)
  })
})
