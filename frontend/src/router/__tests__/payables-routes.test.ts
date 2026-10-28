import { describe, it, expect } from 'vitest'
import { router } from '@/router'

// The per-member payables route (BRY-275) must be matched ahead of the
// per-invoice /betal/:token route, or a member link would resolve to the
// single-invoice page. This guards that ordering.
describe('payables routes (BRY-275)', () => {
  it('matches /betal/bruker/:token to the payables page, not /betal/:token', () => {
    const resolved = router.resolve('/betal/bruker/abc123')
    expect(resolved.matched[0]?.path).toBe('/betal/bruker/:token')
  })

  it('matches the receipt page a payment provider returns to', () => {
    const resolved = router.resolve('/betal/abc123/kvittering')
    expect(resolved.matched[0]?.path).toBe('/betal/:token/kvittering')
    expect(resolved.params.token).toBe('abc123')
  })

  it('still matches a plain /betal/:token to the single-invoice page', () => {
    const resolved = router.resolve('/betal/abc123')
    expect(resolved.matched[0]?.path).toBe('/betal/:token')
  })
})
