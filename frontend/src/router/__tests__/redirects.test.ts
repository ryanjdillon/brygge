import { describe, it, expect, vi } from 'vitest'
import { router } from '@/router'

vi.unmock('vue-i18n')

describe('legacy route redirects', () => {
  it('/admin/communication redirects to the inbox (broadcast page retired, BRY-168)', () => {
    const record = router.getRoutes().find((r) => r.path === '/admin/communication')
    expect(record?.redirect).toBe('/admin/inbox')
  })
})
