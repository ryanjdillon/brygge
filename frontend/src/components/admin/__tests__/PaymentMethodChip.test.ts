import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import PaymentMethodChip from '@/components/admin/PaymentMethodChip.vue'

// The i18n mock in src/test/setup.ts returns keys, so translated labels show
// as their key.
function chip(method: string, cardBrand?: string | null) {
  return mount(PaymentMethodChip, { props: { method, cardBrand } }).find('[data-testid="payment-method-chip"]')
}

describe('PaymentMethodChip', () => {
  it.each([
    ['vipps', null, 'recentPayments.method.vipps', 'bg-orange-50'],
    ['card', 'visa', 'Visa', 'ring-stone-300'],
    ['card', 'mastercard', 'Mastercard', 'ring-stone-300'],
    ['card', null, 'recentPayments.method.card', 'ring-stone-300'],
    ['card', 'unknown', 'recentPayments.method.card', 'ring-stone-300'],
    ['apple_pay', null, 'recentPayments.method.apple_pay', 'bg-indigo-50'],
    ['google_pay', null, 'recentPayments.method.google_pay', 'bg-violet-50'],
  ])('%s (%s) reads %s', (method, brand, label, colour) => {
    const c = chip(method, brand)
    expect(c.attributes('role')).toBeUndefined()
    expect(c.text()).toBe(label)
    expect(c.attributes('title')).toBe(label)
    expect(c.find('svg').attributes('aria-hidden')).toBe('true')
    expect(c.classes()).toContain(colour)
    expect(c.find('svg').exists()).toBe(true)
  })

  it('names the card behind a wallet, beside the wallet on wider screens', () => {
    const c = chip('apple_pay', 'visa')
    // The brand is in the text (and so the accessible name) on every screen;
    // only its display is held back on a phone.
    expect(c.text()).toBe('recentPayments.method.apple_pay · Visa')
    const suffix = c.find('span.sr-only')
    expect(suffix.text()).toBe('· Visa')
    expect(suffix.classes()).toContain('sm:not-sr-only')
    expect(c.find('.truncate').classes()).toContain('min-w-0')
  })

  it('shows an unknown method as a card', () => {
    const c = chip('klarna', null)
    expect(c.attributes('data-method')).toBe('card')
    expect(c.text()).toBe('recentPayments.method.card')
  })

  it('uses none of the payment-kind hues', () => {
    for (const m of ['vipps', 'card', 'apple_pay', 'google_pay']) {
      const cls = chip(m).attributes('class') ?? ''
      expect(cls).not.toMatch(/\b(bg|ring|text)-(blue|teal|slate|amber|gray)-/)
    }
  })
})
