import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

vi.unmock('vue-i18n')

vi.mock('@simplewebauthn/browser', () => ({
  startRegistration: vi.fn(async () => ({ id: 'att', response: {} })),
  startAuthentication: vi.fn(async () => ({ id: 'ass', response: {} })),
  browserSupportsWebAuthn: () => true,
}))

import { usePasskey, passkeysSupported } from '@/composables/usePasskey'
import { startRegistration, startAuthentication } from '@simplewebauthn/browser'

function respond(url: string): Response {
  if (url.endsWith('/register/begin') || url.endsWith('/step-up/begin')) {
    return new Response(JSON.stringify({ challenge: 'x' }), { status: 200 })
  }
  if (url.endsWith('/register/finish') || url.endsWith('/step-up/finish')) {
    return new Response(JSON.stringify({ message: 'ok' }), { status: 200 })
  }
  if (url.endsWith('/auth/passkeys')) {
    return new Response(
      JSON.stringify({
        passkeys: [
          {
            id: 'p1',
            name: 'Key',
            transports: [],
            backup_eligible: false,
            backup_state: false,
            created_at: '2026-01-01',
          },
        ],
      }),
      { status: 200 },
    )
  }
  // /session/me and anything else
  return new Response('', { status: 401 })
}

describe('usePasskey', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => respond(String(url)))
    setActivePinia(createPinia())
  })

  it('register runs begin → startRegistration → finish', async () => {
    const { register } = usePasskey()
    await register()
    expect(startRegistration).toHaveBeenCalledOnce()
    const urls = vi.mocked(globalThis.fetch).mock.calls.map((c) => String(c[0]))
    expect(urls.some((u) => u.endsWith('/register/begin'))).toBe(true)
    expect(urls.some((u) => u.endsWith('/register/finish'))).toBe(true)
  })

  it('stepUp runs begin → startAuthentication → finish', async () => {
    const { stepUp } = usePasskey()
    await stepUp()
    expect(startAuthentication).toHaveBeenCalledOnce()
    const urls = vi.mocked(globalThis.fetch).mock.calls.map((c) => String(c[0]))
    expect(urls.some((u) => u.endsWith('/step-up/finish'))).toBe(true)
  })

  it('list returns the passkeys array', async () => {
    const { list } = usePasskey()
    const pks = await list()
    expect(pks).toHaveLength(1)
    expect(pks[0].id).toBe('p1')
  })

  it('surfaces a PasskeyError on a failing request', async () => {
    vi.mocked(globalThis.fetch).mockImplementation(async (url) => {
      if (String(url).endsWith('/register/begin')) {
        return new Response(JSON.stringify({ error: 'totp_fresh_required' }), { status: 403 })
      }
      return respond(String(url))
    })
    const { register } = usePasskey()
    await expect(register()).rejects.toMatchObject({ status: 403, message: 'totp_fresh_required' })
  })

  it('passkeysSupported reflects browser support', () => {
    expect(passkeysSupported()).toBe(true)
  })
})
