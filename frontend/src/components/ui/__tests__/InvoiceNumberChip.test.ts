import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import InvoiceNumberChip from '@/components/ui/InvoiceNumberChip.vue'

describe('InvoiceNumberChip (BRY-192)', () => {
  it('renders the number with the green class when paid', () => {
    const w = mount(InvoiceNumberChip, { props: { number: 70, status: 'paid', title: 'Paid' } })
    expect(w.text()).toBe('70')
    expect(w.find('span').classes()).toContain('bg-green-100')
    expect(w.find('span').attributes('title')).toBe('Paid')
  })

  it('uses yellow for waiting and red for past_due', () => {
    const waiting = mount(InvoiceNumberChip, { props: { number: 71, status: 'waiting' } })
    expect(waiting.find('span').classes()).toContain('bg-yellow-100')

    const overdue = mount(InvoiceNumberChip, { props: { number: 72, status: 'past_due' } })
    expect(overdue.find('span').classes()).toContain('bg-red-100')
  })
})
