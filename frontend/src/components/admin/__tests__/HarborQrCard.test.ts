import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import HarborQrCard from '@/components/admin/HarborQrCard.vue'

const base = { label: 'Dock A', price: '300 kr / natt', qrSrc: 'data:image/svg+xml,x', per: 4 as const }

describe('HarborQrCard', () => {
  it('prints the short address without the scheme, breakable after each slash', () => {
    const wrapper = mount(HarborQrCard, {
      props: { ...base, shortUrl: 'https://club.example.invalid/q/gjest' },
    })
    const el = wrapper.get('[data-testid="qr-card-short-url"]')
    expect(el.text()).toBe('club.example.invalid/q/gjest')
    expect(el.findAll('wbr')).toHaveLength(2)
    expect(el.html()).toMatch(/invalid\/<wbr[^>]*>q\/<wbr[^>]*>gjest/)
  })

  it('omits the line when there is no short address', () => {
    const wrapper = mount(HarborQrCard, { props: base })
    expect(wrapper.find('[data-testid="qr-card-short-url"]').exists()).toBe(false)
  })
})
