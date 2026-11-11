import { describe, it, expect, beforeEach } from 'vitest'
import { safeRedirect, rememberLoginRedirect, takeLoginRedirect, clearLoginRedirect } from '@/lib/loginRedirect'

const ACCOUNT = 'kasserer@example.test'

describe('safeRedirect', () => {
  it.each([
    ['/admin/accounting/journal?openPeriod=2027', '/admin/accounting/journal?openPeriod=2027'],
    ['/admin/accounting/journal?reopenPeriod=abc#top', '/admin/accounting/journal?reopenPeriod=abc#top'],
    ['/portal', '/portal'],
  ])('keeps the same-origin path %s', (input, want) => {
    expect(safeRedirect(input)).toBe(want)
  })

  it.each([
    ['protocol-relative', '//evil.example/x'],
    ['dot segment to protocol-relative', '/.//evil.example'],
    ['parent segment to protocol-relative', '/..//evil.example'],
    ['absolute URL', 'https://evil.example/x'],
    ['javascript URL', 'javascript:alert(1)'],
    ['backslash host', '/\\evil.example'],
    ['tab trick', '/\t/evil.example'],
    ['relative without slash', 'admin'],
    ['empty', ''],
    ['the login page itself', '/login?redirect=/admin'],
    ['the login page in capitals', '/LOGIN'],
    ['the verify page', '/Login/verify?token=x'],
  ])('rejects %s', (_, input) => {
    expect(safeRedirect(input)).toBeNull()
  })

  it('rejects anything that is not a string', () => {
    expect(safeRedirect(['/admin'])).toBeNull()
    expect(safeRedirect(undefined)).toBeNull()
  })
})

describe('kept login redirect', () => {
  beforeEach(() => localStorage.clear())

  it('is taken once, by the account the link went to (any case)', () => {
    rememberLoginRedirect('/admin/accounting/journal?openPeriod=2027', ACCOUNT)
    expect(takeLoginRedirect('Kasserer@Example.test')).toBe('/admin/accounting/journal?openPeriod=2027')
    expect(takeLoginRedirect(ACCOUNT)).toBeNull()
  })

  it('is not handed to another account, and is cleared', () => {
    rememberLoginRedirect('/admin', ACCOUNT)
    expect(takeLoginRedirect('someone@example.test')).toBeNull()
    expect(takeLoginRedirect(ACCOUNT)).toBeNull()
  })

  it('needs an account', () => {
    rememberLoginRedirect('/admin', ACCOUNT)
    expect(takeLoginRedirect(undefined)).toBeNull()
  })

  it('never stores an unsafe path, and clears an old one instead', () => {
    rememberLoginRedirect('/admin', ACCOUNT)
    rememberLoginRedirect('//evil.example', ACCOUNT)
    expect(takeLoginRedirect(ACCOUNT)).toBeNull()
  })

  it('expires', () => {
    rememberLoginRedirect('/admin', ACCOUNT)
    expect(takeLoginRedirect(ACCOUNT, Date.now() + 2 * 60 * 60 * 1000)).toBeNull()
  })

  it('is cleared on demand', () => {
    rememberLoginRedirect('/admin', ACCOUNT)
    clearLoginRedirect()
    expect(takeLoginRedirect(ACCOUNT)).toBeNull()
  })

  it('re-checks what it reads back', () => {
    localStorage.setItem('brygge.loginRedirect', JSON.stringify({ path: '/.//evil.example', email: ACCOUNT, at: Date.now() }))
    expect(takeLoginRedirect(ACCOUNT)).toBeNull()
  })
})
