import { describe, it, expect } from 'vitest'
import { isAppleMobile, isStandaloneDisplay } from '@/lib/pwa'

function nav(userAgent: string, maxTouchPoints = 0, standalone?: boolean): Navigator {
  return { userAgent, maxTouchPoints, standalone } as unknown as Navigator
}

function win(navigator: Navigator, displayStandalone: boolean): Window {
  return {
    navigator,
    matchMedia: (q: string) => ({ matches: displayStandalone && q === '(display-mode: standalone)' }),
  } as unknown as Window
}

const IPHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1'
const IPAD_DESKTOP_UA = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15'
const ANDROID = 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36'

describe('isAppleMobile', () => {
  it('detects iPhone', () => expect(isAppleMobile(nav(IPHONE))).toBe(true))
  it('detects iPadOS, which reports a Mac with touch', () => expect(isAppleMobile(nav(IPAD_DESKTOP_UA, 5))).toBe(true))
  it('does not flag a Mac without touch', () => expect(isAppleMobile(nav(IPAD_DESKTOP_UA, 0))).toBe(false))
  it('does not flag Android', () => expect(isAppleMobile(nav(ANDROID, 5))).toBe(false))
})

describe('isStandaloneDisplay', () => {
  it('is false in a Safari tab', () => expect(isStandaloneDisplay(win(nav(IPHONE, 5, false), false))).toBe(false))
  it('is true from the iOS Home Screen (navigator.standalone)', () =>
    expect(isStandaloneDisplay(win(nav(IPHONE, 5, true), false))).toBe(true))
  it('is true for display-mode standalone', () => expect(isStandaloneDisplay(win(nav(ANDROID), true))).toBe(true))
})
