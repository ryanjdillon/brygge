<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useMatrix } from '@/composables/useMatrix'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()

const { rooms, isLoadingRooms, roomsError } = useMatrix()
const sidebarOpen = ref(true)

const currentRoomId = ref<string | null>(
  (route.params.roomId as string) || null
)

watch(
  () => route.params.roomId,
  (id) => {
    currentRoomId.value = (id as string) || null
  },
)

watch(rooms, (roomList) => {
  if (roomList && roomList.length > 0 && !currentRoomId.value) {
    selectRoom(roomList[0].id)
  }
})

function selectRoom(roomId: string) {
  currentRoomId.value = roomId
  sidebarOpen.value = false
  router.push({ name: 'forum-room', params: { roomId } })
}

function toggleSidebar() {
  sidebarOpen.value = !sidebarOpen.value
}
</script>

<template>
  <div class="flex h-full min-h-0">
    <!-- Mobile sidebar toggle -->
    <button
      class="fixed bottom-4 left-4 z-50 rounded-full bg-blue-600 p-3 text-white shadow-lg md:hidden"
      @click="toggleSidebar"
    >
      <svg xmlns="http://www.w3.org/2000/svg" class="h-6 w-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16" />
      </svg>
    </button>

    <!-- Room sidebar -->
    <aside
      :class="[
        'flex w-64 shrink-0 flex-col border-r border-gray-200 bg-gray-50',
        'transition-transform duration-200',
        'fixed inset-y-0 left-0 z-40 md:relative md:translate-x-0',
        sidebarOpen ? 'translate-x-0' : '-translate-x-full',
      ]"
    >
      <div class="border-b border-gray-200 p-4">
        <h2 class="text-lg font-semibold text-gray-900">
          {{ t('forum.title') }}
        </h2>
      </div>

      <div v-if="isLoadingRooms" class="p-4 text-sm text-gray-500">
        {{ t('forum.loading') }}
      </div>

      <div v-else-if="roomsError" class="p-4 text-sm text-red-500">
        {{ t('common.error') }}
      </div>

      <nav v-else class="flex-1 overflow-y-auto p-2">
        <button
          v-for="room in rooms"
          :key="room.id"
          :class="[
            'flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors',
            currentRoomId === room.id
              ? 'bg-blue-100 text-blue-800'
              : 'text-gray-700 hover:bg-gray-100',
          ]"
          @click="selectRoom(room.id)"
        >
          <span class="text-gray-400">#</span>
          <span class="truncate">{{ room.name }}</span>
          <span class="ml-auto text-xs text-gray-400">{{ room.memberCount }}</span>
        </button>
      </nav>
    </aside>

    <!-- Backdrop for mobile -->
    <div
      v-if="sidebarOpen"
      class="fixed inset-0 z-30 bg-black/20 md:hidden"
      @click="sidebarOpen = false"
    />

    <!-- Main content area -->
    <main class="flex flex-1 flex-col min-w-0">
      <router-view v-if="currentRoomId" />
      <div v-else class="flex flex-1 items-center justify-center text-gray-400">
        {{ t('forum.selectRoom') }}
      </div>
    </main>
  </div>
</template>
