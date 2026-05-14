<script setup lang="ts">
// Inbox indicator with unread badge (DIL-275 polish). Reads from
// the shared inboxUnread Pinia store, which both this component
// and the InboxView refresh — so the count updates instantly when
// the user marks/archives in-page, not only on the 60s poll.

import { computed, onMounted, onUnmounted } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Inbox } from 'lucide-vue-next'
import { useAuthStore } from '@/stores/auth'
import { useInboxUnreadStore } from '@/stores/inboxUnread'
import { storeToRefs } from 'pinia'

const { t } = useI18n()
const auth = useAuthStore()
const inbox = useInboxUnreadStore()
const { accessible, totalUnread } = storeToRefs(inbox)

const display = computed(() => (totalUnread.value > 99 ? '99+' : String(totalUnread.value)))

let timer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  if (auth.isAuthenticated) inbox.refresh({ silent: true })
  // Background floor: 60s catches drift if no in-page action triggered a refresh.
  timer = setInterval(() => {
    if (auth.isAuthenticated) inbox.refresh({ silent: true })
  }, 60_000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <RouterLink
    v-if="accessible"
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
