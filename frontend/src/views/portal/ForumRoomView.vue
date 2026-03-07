<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useMatrix, type MatrixMessage } from '@/composables/useMatrix'

const { t } = useI18n()
const route = useRoute()

const { rooms, useRoomMessages, sendMessage, isSending } = useMatrix()

const roomId = computed(() => route.params.roomId as string)
const beforeToken = ref<string | undefined>(undefined)

const { data: messagesData, isLoading: isLoadingMessages } = useRoomMessages(
  () => roomId.value,
  50,
  () => undefined,
)

const olderMessagesToken = ref<string | undefined>(undefined)
const olderMessages = ref<MatrixMessage[]>([])
const isLoadingOlder = ref(false)

const allMessages = computed(() => {
  const current = messagesData.value?.messages ?? []
  return [...olderMessages.value, ...current]
})

const currentRoom = computed(() =>
  rooms.value?.find((r) => r.id === roomId.value),
)

const messageInput = ref('')
const messagesContainer = ref<HTMLElement | null>(null)

watch(roomId, () => {
  olderMessages.value = []
  olderMessagesToken.value = undefined
  beforeToken.value = undefined
})

watch(
  () => messagesData.value?.end,
  (token) => {
    if (token && !olderMessagesToken.value) {
      olderMessagesToken.value = token
    }
  },
)

watch(
  () => messagesData.value?.messages,
  () => {
    nextTick(scrollToBottom)
  },
  { deep: true },
)

onMounted(() => {
  nextTick(scrollToBottom)
})

function scrollToBottom() {
  if (messagesContainer.value) {
    messagesContainer.value.scrollTop = messagesContainer.value.scrollHeight
  }
}

async function loadOlder() {
  if (!olderMessagesToken.value || isLoadingOlder.value) return

  isLoadingOlder.value = true
  try {
    const params = new URLSearchParams({
      limit: '50',
      before: olderMessagesToken.value,
    })
    const response = await fetch(`/api/v1/forum/rooms/${roomId.value}/messages?${params}`, {
      headers: {
        'Authorization': `Bearer ${localStorage.getItem('access_token')}`,
      },
    })
    if (!response.ok) throw new Error('Failed to load older messages')
    const data = await response.json()
    olderMessages.value = [...(data.messages ?? []), ...olderMessages.value]
    olderMessagesToken.value = data.end || undefined
  } finally {
    isLoadingOlder.value = false
  }
}

async function handleSend() {
  const content = messageInput.value.trim()
  if (!content || !roomId.value) return

  messageInput.value = ''
  await sendMessage({ roomId: roomId.value, content })
  nextTick(scrollToBottom)
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault()
    handleSend()
  }
}

function formatTime(timestamp: string): string {
  const date = new Date(timestamp)
  const now = new Date()
  const isToday = date.toDateString() === now.toDateString()

  if (isToday) {
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }
  return date.toLocaleDateString([], {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}
</script>

<template>
  <div class="flex flex-1 flex-col min-h-0">
    <!-- Room header -->
    <header class="shrink-0 border-b border-gray-200 bg-white px-4 py-3">
      <div class="flex items-center gap-2">
        <span class="text-gray-400">#</span>
        <h2 class="font-semibold text-gray-900">
          {{ currentRoom?.name ?? roomId }}
        </h2>
      </div>
      <p v-if="currentRoom?.topic" class="mt-1 text-sm text-gray-500">
        {{ currentRoom.topic }}
      </p>
    </header>

    <!-- Messages area -->
    <div
      ref="messagesContainer"
      class="flex-1 overflow-y-auto px-4 py-2"
    >
      <!-- Load more -->
      <div v-if="olderMessagesToken" class="mb-4 text-center">
        <button
          class="rounded-md bg-gray-100 px-4 py-2 text-sm text-gray-600 hover:bg-gray-200 disabled:opacity-50"
          :disabled="isLoadingOlder"
          @click="loadOlder"
        >
          {{ isLoadingOlder ? t('common.loading') : t('forum.loadMore') }}
        </button>
      </div>

      <!-- Loading state -->
      <div v-if="isLoadingMessages" class="flex flex-1 items-center justify-center py-12">
        <span class="text-sm text-gray-400">{{ t('forum.loading') }}</span>
      </div>

      <!-- Empty state -->
      <div
        v-else-if="allMessages.length === 0"
        class="flex flex-1 items-center justify-center py-12"
      >
        <span class="text-sm text-gray-400">{{ t('forum.noMessages') }}</span>
      </div>

      <!-- Messages list -->
      <div v-else class="space-y-3">
        <div
          v-for="message in allMessages"
          :key="message.id"
          class="group flex gap-3"
        >
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-blue-100 text-sm font-medium text-blue-700">
            {{ message.senderName.charAt(0).toUpperCase() }}
          </div>
          <div class="min-w-0">
            <div class="flex items-baseline gap-2">
              <span class="text-sm font-medium text-gray-900">
                {{ message.senderName }}
              </span>
              <span class="text-xs text-gray-400">
                {{ formatTime(message.timestamp) }}
              </span>
            </div>
            <p class="mt-0.5 whitespace-pre-wrap break-words text-sm text-gray-700">
              {{ message.content }}
            </p>
          </div>
        </div>
      </div>
    </div>

    <!-- Message input -->
    <div class="shrink-0 border-t border-gray-200 bg-white p-4">
      <div class="flex gap-2">
        <textarea
          v-model="messageInput"
          aria-label="Skriv en melding"
          class="flex-1 resize-none rounded-md border border-gray-300 px-3 py-2 text-sm placeholder-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          rows="1"
          :placeholder="t('forum.messagePlaceholder')"
          :disabled="isSending"
          @keydown="handleKeydown"
        />
        <button
          class="shrink-0 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          :disabled="!messageInput.trim() || isSending"
          @click="handleSend"
        >
          {{ t('forum.send') }}
        </button>
      </div>
    </div>
  </div>
</template>
