import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { Router } from 'vue-router'
import { openPayLink, PAY_LINK_FALLBACK } from '@/lib/joinFlow'

describe('openPayLink', () => {
  const push = vi.fn().mockResolvedValue(undefined)
  const router = { push } as unknown as Router
  const assign = vi.fn()

  beforeEach(() => {
    push.mockClear()
    assign.mockClear()
    vi.stubGlobal('location', { origin: 'https://club.example.test', assign })
  })
  afterEach(() => vi.unstubAllGlobals())

  it('routes a same-origin link inside the app', async () => {
    await openPayLink(router, 'https://club.example.test/betal/tok?x=1#y', false)
    expect(push).toHaveBeenCalledWith('/betal/tok?x=1#y')
    expect(assign).not.toHaveBeenCalled()
  })

  it('routes an app path', async () => {
    await openPayLink(router, '/betal/tok', false)
    expect(push).toHaveBeenCalledWith('/betal/tok')
  })

  it('follows another origin over https', async () => {
    await openPayLink(router, 'https://pay.example.test/session/1', false)
    expect(assign).toHaveBeenCalledWith('https://pay.example.test/session/1')
    expect(push).not.toHaveBeenCalled()
  })

  it('follows plain http to another origin only in development', async () => {
    await openPayLink(router, 'http://localhost:8080/betal/tok', true)
    expect(assign).toHaveBeenCalledWith('http://localhost:8080/betal/tok')

    assign.mockClear()
    await openPayLink(router, 'http://pay.example.test/betal/tok', false)
    expect(assign).not.toHaveBeenCalled()
    expect(push).toHaveBeenCalledWith(PAY_LINK_FALLBACK)
  })

  it.each([
    '//evil.example.test/betal/tok',
    '/\\evil.example.test/betal/tok',
    'javascript:alert(1)',
    'data:text/html,hi',
    'ftp://files.example.test/x',
    'not a url',
  ])('sends %s to the fallback', async (url) => {
    await openPayLink(router, url, true)
    expect(assign).not.toHaveBeenCalled()
    expect(push).toHaveBeenCalledWith(PAY_LINK_FALLBACK)
  })
})
