import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PayMethodButtons from '@/components/pay/PayMethodButtons.vue'

vi.mock('lucide-vue-next', () => ({
  Loader2: { template: '<span data-testid="spinner" />' },
  CreditCard: { template: '<span data-testid="card-icon" />' },
}))

function mountButtons(props: { providers: string[]; emphasis?: 'vipps' | 'card'; busy?: boolean; disabled?: boolean }) {
  return mount(PayMethodButtons, { props })
}

const order = (w: ReturnType<typeof mountButtons>) => w.findAll('button').map((b) => b.attributes('data-provider'))

describe('PayMethodButtons', () => {
  it('renders nothing without a provider', () => {
    const w = mountButtons({ providers: [] })
    expect(w.find('[data-testid="pay-methods"]').exists()).toBe(false)
  })

  it('renders one labelled button for a single provider, with no choice title', () => {
    const w = mountButtons({ providers: ['stripe'] })
    const buttons = w.findAll('button')
    expect(buttons).toHaveLength(1)
    expect(buttons[0].text()).toBe('pay.methods.card')
    // The visible label is the accessible name; the icon is decorative.
    expect(buttons[0].attributes('aria-label')).toBeUndefined()
    expect(buttons[0].get('[data-testid="card-icon"]').attributes('aria-hidden')).toBe('true')
    expect(buttons[0].find('[data-testid="card-icon"]').exists()).toBe(true)
    expect(w.text()).not.toContain('pay.methods.chooseTitle')
  })

  it('offers both methods in the server order with a choice title', () => {
    const w = mountButtons({ providers: ['vipps', 'stripe'] })
    expect(order(w)).toEqual(['vipps', 'stripe'])
    expect(w.text()).toContain('pay.methods.chooseTitle')
    expect(w.findAll('button')[0].text()).toBe('pay.methods.vipps')
    expect(w.findAll('button')[0].classes()).toContain('bg-[#ff5b24]')
  })

  it('leads with card when emphasised', () => {
    const w = mountButtons({ providers: ['vipps', 'stripe'], emphasis: 'card' })
    expect(order(w)).toEqual(['stripe', 'vipps'])
    expect(w.findAll('button')[0].classes()).toContain('bg-slate-800')
  })

  it('skips a provider it has no method for', () => {
    const w = mountButtons({ providers: ['klarna', 'vipps'] })
    expect(order(w)).toEqual(['vipps'])
  })

  it('emits the chosen provider', async () => {
    const w = mountButtons({ providers: ['vipps', 'stripe'] })
    await w.findAll('button')[1].trigger('click')
    expect(w.emitted('pay')).toEqual([['stripe']])
  })

  it('marks the pressed button busy and disables both while the payment starts', async () => {
    const w = mountButtons({ providers: ['vipps', 'stripe'] })
    await w.findAll('button')[0].trigger('click')
    await w.setProps({ busy: true })
    const [vipps, card] = w.findAll('button')
    expect(vipps.attributes('aria-busy')).toBe('true')
    expect(vipps.text()).toBe('pay.processing')
    expect(vipps.find('[data-testid="spinner"]').exists()).toBe(true)
    expect(card.attributes('aria-busy')).toBe('false')
    // Only the other button fades; the pressed one stays solid.
    expect(vipps.classes()).not.toContain('disabled:opacity-50')
    expect(card.classes()).toContain('disabled:opacity-50')
    expect(vipps.attributes('disabled')).toBeDefined()
    expect(card.attributes('disabled')).toBeDefined()
    await card.trigger('click')
    expect(w.emitted('pay')).toHaveLength(1)
  })

  it('does not emit when disabled', async () => {
    const w = mountButtons({ providers: ['vipps'], disabled: true })
    await w.find('button').trigger('click')
    expect(w.emitted('pay')).toBeUndefined()
  })
})
