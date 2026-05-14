// Shared inbox-unread state (DIL-275). Centralises the "how many
// unread threads across all accessible shared mailboxes" count so
// both the top-nav InboxIndicator and the in-page InboxView agree
// in real time — instead of each component polling /mailboxes on
// its own clock and drifting.

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

interface MailboxView {
  address: string
  unread: number
  total: number
}

export const useInboxUnreadStore = defineStore('inboxUnread', () => {
  const mailboxes = ref<MailboxView[]>([])
  const accessible = ref(false) // backend returned at least one mailbox
  const lastError = ref<Error | null>(null)

  const totalUnread = computed(() =>
    mailboxes.value.reduce((s, m) => s + (m.unread || 0), 0),
  )

  async function refresh(opts: { silent?: boolean } = {}) {
    try {
      const response = await fetch('/api/v1/admin/inbox/mailboxes', {
        credentials: 'include',
      })
      if (!response.ok) {
        if (!opts.silent) lastError.value = new Error(`HTTP ${response.status}`)
        accessible.value = false
        return
      }
      const body = (await response.json()) as { mailboxes: MailboxView[] }
      mailboxes.value = body.mailboxes ?? []
      accessible.value = mailboxes.value.length > 0
      lastError.value = null
    } catch (e) {
      if (!opts.silent) lastError.value = e as Error
      accessible.value = false
    }
  }

  function reset() {
    mailboxes.value = []
    accessible.value = false
    lastError.value = null
  }

  return {
    mailboxes,
    accessible,
    lastError,
    totalUnread,
    refresh,
    reset,
  }
})
