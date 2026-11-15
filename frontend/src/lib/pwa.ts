/**
 * Platform checks for web push on Apple devices (BRY-682). iOS and iPadOS
 * deliver web push only to a site added to the Home Screen and opened from
 * its icon; in a Safari tab `PushManager` does not exist at all.
 */

/** True on iPhone, iPod and iPad (iPadOS reports itself as a Mac with touch). */
export function isAppleMobile(nav: Navigator = navigator): boolean {
  if (/iPad|iPhone|iPod/.test(nav.userAgent)) return true
  return /Macintosh/.test(nav.userAgent) && nav.maxTouchPoints > 1
}

/** True when the app runs from the Home Screen icon, not in a browser tab. */
export function isStandaloneDisplay(win: Window = window): boolean {
  const iosStandalone = (win.navigator as Navigator & { standalone?: boolean }).standalone === true
  const displayStandalone =
    typeof win.matchMedia === 'function' && win.matchMedia('(display-mode: standalone)').matches
  return iosStandalone || displayStandalone
}
