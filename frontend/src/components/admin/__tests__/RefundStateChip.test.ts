import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import RefundStateChip from '@/components/admin/RefundStateChip.vue'
import { disputeAwaitsAnswer, disputeChipKind, refundChipKind } from '@/lib/refundState'

// The i18n mock in src/test/setup.ts returns keys, so labels show as their key.
function chip(props: Record<string, unknown>) {
  return mount(RefundStateChip, { props: props as never }).get('[data-testid="refund-state-chip"]')
}

describe('RefundStateChip', () => {
  it.each([
    ['refunded', 'admin.refund.chip.refunded', 'bg-sky-50'],
    ['partial', 'admin.refund.chip.partial', 'bg-yellow-50'],
    ['disputed', 'admin.refund.chip.disputed', 'bg-red-50'],
    ['dispute_lost', 'admin.refund.chip.disputeLost', 'bg-red-600'],
    ['dispute_won', 'admin.refund.chip.disputeWon', 'bg-emerald-50'],
  ])('%s reads %s in its own colour, with an icon', (kind, label, colour) => {
    const c = chip({ kind })
    expect(c.text()).toBe(label)
    expect(c.attributes('data-kind')).toBe(kind)
    expect(c.get('span').classes()).toContain(colour)
    expect(c.get('svg').attributes('aria-hidden')).toBe('true')
    expect(c.element.tagName).toBe('SPAN')
  })

  it('names the amount of a partial refund and explains a full one', () => {
    expect(chip({ kind: 'partial', amount: 100 }).text()).toContain('admin.refund.chip.partialAmount')
    expect(chip({ kind: 'refunded', amount: 450 }).attributes('title')).toContain('admin.refund.chip.refundedTitle')
  })

  it('says when a dispute must be answered by', () => {
    expect(chip({ kind: 'disputed', dueBy: '2026-11-03T12:00:00Z' }).attributes('title')).toContain('admin.refund.chip.disputedDue')
  })

  it('becomes a Stripe link with a 44px tap area when given an address', () => {
    const c = chip({ kind: 'disputed', href: 'https://dashboard.stripe.com/disputes/dp_1' })
    expect(c.element.tagName).toBe('A')
    expect(c.attributes('href')).toBe('https://dashboard.stripe.com/disputes/dp_1')
    expect(c.attributes('target')).toBe('_blank')
    expect(c.attributes('rel')).toContain('noopener')
    // 20px pill + 2 x 12px padding, pulled back with a negative margin so rows do not grow.
    expect(c.classes()).toEqual(expect.arrayContaining(['py-3', '-my-3']))
    expect(c.attributes('title')).toContain('admin.refund.chip.openStripe')
  })
})

describe('refund chip kinds', () => {
  it('maps the refund state', () => {
    expect(refundChipKind('full')).toBe('refunded')
    expect(refundChipKind('partial')).toBe('partial')
    expect(refundChipKind('none')).toBeNull()
    expect(refundChipKind(undefined)).toBeNull()
  })

  it('maps Stripe dispute statuses, leaving the ones that took no money out', () => {
    for (const s of ['needs_response', 'under_review', 'warning_needs_response', 'warning_under_review']) {
      expect(disputeChipKind(s), s).toBe('disputed')
    }
    expect(disputeChipKind('lost')).toBe('dispute_lost')
    expect(disputeChipKind('won')).toBe('dispute_won')
    for (const s of ['warning_closed', 'charge_refunded', 'prevented', 'something_new', '', null, undefined]) {
      expect(disputeChipKind(s), String(s)).toBeNull()
    }
  })

  it('knows which disputes wait for the club', () => {
    expect(disputeAwaitsAnswer('needs_response')).toBe(true)
    expect(disputeAwaitsAnswer('warning_needs_response')).toBe(true)
    expect(disputeAwaitsAnswer('under_review')).toBe(false)
    expect(disputeAwaitsAnswer(null)).toBe(false)
  })
})
