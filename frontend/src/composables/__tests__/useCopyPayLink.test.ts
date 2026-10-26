import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { copyPayLink, payLinkErrorKey } from '@/composables/useCopyPayLink'

const writeText = vi.fn().mockResolvedValue(undefined)

beforeEach(() => {
  writeText.mockClear()
  Object.defineProperty(globalThis.navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

function mockFetch(impl: () => Promise<Response> | Response) {
  globalThis.fetch = vi.fn(impl) as unknown as typeof fetch
}

describe('copyPayLink (BRY-274/275 shared)', () => {
  it('copies the pay_url to the clipboard on success', async () => {
    mockFetch(() => ({ ok: true, json: async () => ({ pay_url: 'https://x/betal/abc' }) }) as Response)
    const r = await copyPayLink('/api/v1/admin/financials/users/u1/pay-link')
    expect(r).toEqual({ ok: true, url: 'https://x/betal/abc' })
    expect(writeText).toHaveBeenCalledWith('https://x/betal/abc')
  })

  it('reports vipps_unavailable distinctly on a 409', async () => {
    mockFetch(() => ({ ok: false, status: 409, json: async () => ({ error: 'vipps_unavailable' }) }) as Response)
    const r = await copyPayLink('/x')
    expect(r).toEqual({ ok: false, reason: 'vipps_unavailable' })
    expect(writeText).not.toHaveBeenCalled()
  })

  it('reports an imported faktura without an opening balance by its code (BRY-386)', async () => {
    mockFetch(
      () =>
        ({
          ok: false,
          status: 409,
          json: async () => ({ error: 'This faktura was imported…', code: 'imported_no_opening_balance' }),
        }) as Response,
    )
    const r = await copyPayLink('/x')
    expect(r).toEqual({ ok: false, reason: 'imported_no_opening_balance' })
    expect(payLinkErrorKey('imported_no_opening_balance')).toBe('admin.accounting.importedNoOpeningBalance')
  })

  it('reports a generic failure for other non-OK responses', async () => {
    mockFetch(() => ({ ok: false, status: 500, json: async () => ({}) }) as Response)
    const r = await copyPayLink('/x')
    expect(r).toEqual({ ok: false, reason: 'failed' })
  })

  it('reports a generic failure when the request throws', async () => {
    mockFetch(() => {
      throw new Error('network')
    })
    const r = await copyPayLink('/x')
    expect(r).toEqual({ ok: false, reason: 'failed' })
  })
})
