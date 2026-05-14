<script setup lang="ts">
// Inbox indicator with unread badge (DIL-275 polish). Polls
// /admin/inbox/mailboxes every 60s for the signed-in user; renders
// nothing when the user has no board-mailbox role (the endpoint
// returns an empty list, which we treat as "no chrome shown"). The
// badge counts unread *threads* across all accessible mailboxes,
// gmail-style capping at 99+.

import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Inbox } from 'lucide-vue-next'
import { useAuthStore } from '@/stores/auth'
import { useApi } from '@/composables/useApi'

interface MailboxView {
  address: string
  unread: number
}

const { t } = useI18n()
const auth = useAuthStore()
const { fetchApi } = useApi()

const totalUnread = ref(0)
const visible = ref(false)

const display = computed(() => (totalUnread.value > 99 ? '99+' : String(totalUnread.value)))

async function refresh() {
  if (!auth.isAuthenticated) return
  try {
    const res = await fetchApi<{ mailboxes: MailboxView[] }>('/api/v1/admin/inbox/mailboxes')
    const boxes = res.mailboxes ?? []
    visible.value = boxes.length > 0
    totalUnread.value = boxes.reduce((s, m) => s + (m.unread || 0), 0)
  } catch {
    // Backend may be unconfigured (no Stalwart) or the user lacks
    // any board-mailbox role — both render as "hide the chrome".
    visible.value = false
  }
}

let timer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  refresh()
  timer = setInterval(refresh, 60_000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <RouterLink
    v-if="visible"
    to="/admin/inbox"
    class="relative inline-flex items-center rounded-full p-2 text-slate-500 hover:bg-slate-100 hover:text-slate-700"
    :title="t('admin.sidebar.inbox')"
    :aria-label="t('admin.sidebar.inbox')"
  >
    <Inbox class="h-5 w-5" />
    <span
      v-if="totalUnread > 0"
      class="absolute -right-0.5 -top-0.5 inline-flex min-w-[1rem] items-center justify-center rounded-full bg-red-600 px-1 text-[10px] font-semibold leading-tight text-white"
    >
      {{ display }}
    </span>
  </RouterLink>
</template>
