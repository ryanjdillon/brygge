// Brygge service worker: web push only (no offline cache).
//
// iOS/iPadOS (BRY-682) delivers web push only to a Home-Screen-installed
// PWA and revokes the permission when a push arrives without a visible
// notification, so every push event must end in showNotification, even an
// empty or malformed one.

self.addEventListener('install', () => {
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(clients.claim())
})

function readPushData(event) {
  if (!event.data) return {}
  try {
    return event.data.json() || {}
  } catch {
    try {
      return { body: event.data.text() }
    } catch {
      return {}
    }
  }
}

self.addEventListener('push', (event) => {
  const data = readPushData(event)
  const options = {
    body: data.body || '',
    icon: '/icon-192.png',
    badge: '/icon-192.png',
    tag: data.category || 'default',
    // A second push with the same tag replaces the first; renotify makes it
    // alert again (sound/vibration) instead of updating silently.
    renotify: true,
    data: {
      url: data.url || '/',
    },
  }

  // The server puts the club's name in the title; 'Brygge' is only for a
  // push that arrives without one.
  event.waitUntil(self.registration.showNotification(data.title || 'Brygge', options))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  // Only same-origin paths are opened; anything else falls back to the start page.
  let target = new URL('/', self.location.origin)
  try {
    const candidate = new URL(event.notification.data?.url || '/', self.location.origin)
    if (candidate.origin === self.location.origin) target = candidate
  } catch {
    // keep the start page
  }

  event.waitUntil(
    clients.matchAll({ type: 'window', includeUncontrolled: true }).then(async (windowClients) => {
      const exact = windowClients.find((c) => c.url === target.href && 'focus' in c)
      if (exact) return exact.focus()

      // Reuse an open Brygge window (the installed app on iOS has one) and
      // move it to the target page instead of opening a second window.
      const existing = windowClients.find((c) => new URL(c.url).origin === target.origin && 'focus' in c)
      if (existing) {
        const focused = await existing.focus()
        // navigate() rejects for a window this worker does not control; the
        // focused window is still the right app then.
        if (focused && 'navigate' in focused) return focused.navigate(target.href).catch(() => focused)
        return focused
      }
      return clients.openWindow(target.href)
    }),
  )
})
