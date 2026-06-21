<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { formatDate } from '@/lib/format'
import SortableTh from '@/components/admin/SortableTh.vue'
import { UserPlus, GripVertical, X } from 'lucide-vue-next'
import { useApi } from '@/composables/useApi'

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()
const { fetchApi } = useApi()

type SortField = 'position' | 'name' | 'registered'
const sortField = ref<SortField>('position')
const sortDir = ref<'asc' | 'desc'>('asc')

function setSort(field: SortField) {
  if (sortField.value === field) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortField.value = field
    sortDir.value = 'asc'
  }
}

const PAGE_SIZE = 100
const offset = ref(0)

const { data: response, isLoading, isError } = useQuery({
  queryKey: ['admin', 'waiting-list', offset],
  queryFn: async () => {
    const res = unwrap(await client.GET('/api/v1/waiting-list', {
      params: { query: { limit: PAGE_SIZE, offset: offset.value } as any },
    }))
    return res as unknown as { items: any[]; has_more: boolean; limit: number; offset: number }
  },
})

const entries = computed(() => response.value?.items ?? [])
const hasMore = computed(() => response.value?.has_more ?? false)
const hasPrev = computed(() => offset.value > 0)

function nextPage() { if (hasMore.value) offset.value += PAGE_SIZE }
function prevPage() { if (hasPrev.value) offset.value = Math.max(0, offset.value - PAGE_SIZE) }

const sorted = computed(() => {
  const list = [...entries.value]
  list.sort((a, b) => {
    let cmp = 0
    if (sortField.value === 'position') cmp = (a.position ?? 0) - (b.position ?? 0)
    else if (sortField.value === 'name') cmp = (a.full_name ?? '').localeCompare(b.full_name ?? '')
    else if (sortField.value === 'registered') cmp = (a.created_at ?? '') < (b.created_at ?? '') ? -1 : 1
    return sortDir.value === 'asc' ? cmp : -cmp
  })
  return list
})

// ── Drag to reorder ─────────────────────────────────────────────────────────

const draggingId = ref<string | null>(null)
const dragOverId = ref<string | null>(null)
const reorderingId = ref<string | null>(null)

function onDragStart(e: DragEvent, id: string) {
  draggingId.value = id
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', id)
  }
}

function onDragOver(e: DragEvent, id: string) {
  if (!draggingId.value || draggingId.value === id) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  dragOverId.value = id
}

function onDragLeave() {
  dragOverId.value = null
}

function onDragEnd() {
  draggingId.value = null
  dragOverId.value = null
}

const { mutate: reorder } = useMutation({
  mutationFn: async ({ entryId, newPosition }: { entryId: string; newPosition: number }) => {
    const res = await fetch(`/api/v1/waiting-list/${entryId}/position`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ new_position: newPosition }),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => null)
      throw new Error(err?.error ?? `${res.status}`)
    }
  },
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'waiting-list'] }),
  onSettled: () => { reorderingId.value = null },
})

function onDrop(e: DragEvent, targetId: string) {
  e.preventDefault()
  draggingId.value = null
  dragOverId.value = null

  const sourceId = e.dataTransfer?.getData('text/plain')
  if (!sourceId || sourceId === targetId) return

  const target = sorted.value.find(e => e.id === targetId)
  if (!target) return

  reorderingId.value = sourceId
  reorder({ entryId: sourceId, newPosition: target.position })
}

// ── Add member modal ─────────────────────────────────────────────────────────

const enrolledIds = computed(() =>
  new Set(entries.value.filter((e: any) => ['active', 'offered'].includes(e.status)).map((e: any) => e.user_id)),
)

const showAddModal = ref(false)
const memberSearch = ref('')
const searchResults = ref<any[]>([])
const searchLoading = ref(false)
const addError = ref('')

let searchTimer: ReturnType<typeof setTimeout> | null = null

watch(memberSearch, (q) => {
  if (searchTimer) clearTimeout(searchTimer)
  if (!q.trim()) {
    searchResults.value = []
    return
  }
  searchTimer = setTimeout(async () => {
    searchLoading.value = true
    try {
      const data = await fetchApi(`/api/v1/admin/users?q=${encodeURIComponent(q.trim())}&limit=20`) as any
      // The admin users endpoint returns { users: [...] }, not { items }.
      searchResults.value = data.users ?? data.items ?? []
    } catch {
      searchResults.value = []
    } finally {
      searchLoading.value = false
    }
  }, 250)
})

function openAddModal() {
  memberSearch.value = ''
  searchResults.value = []
  addError.value = ''
  showAddModal.value = true
}

function closeAddModal() {
  showAddModal.value = false
  if (searchTimer) clearTimeout(searchTimer)
}

const { mutate: enroll, isPending: enrolling } = useMutation({
  mutationFn: async (userId: string) => {
    const res = await fetch('/api/v1/waiting-list/enroll', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ user_id: userId }),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => null)
      throw new Error(err?.error ?? `${res.status}`)
    }
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'waiting-list'] })
    closeAddModal()
  },
  onError: (err: unknown) => {
    addError.value = err instanceof Error ? err.message : String(err)
  },
})

function selectMember(user: any) {
  addError.value = ''
  enroll(user.id)
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.waitingList') }}</h1>
      <button
        class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white hover:bg-blue-700"
        @click="openAddModal"
      >
        <UserPlus class="h-4 w-4" aria-hidden="true" />
        {{ t('admin.waitingList.addMember') }}
      </button>
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">{{ t('admin.waitingList.loadError') }}</div>

    <div v-else-if="!sorted.length" class="mt-6 text-gray-500">{{ t('admin.waitingList.noEntries') }}</div>

    <div v-else class="mt-6 overflow-x-auto">
      <p v-if="sortField === 'position'" class="mb-2 text-xs text-gray-400">{{ t('admin.waitingList.dragHint') }}</p>
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th v-if="sortField === 'position'" scope="col" class="w-6 px-2 py-3" aria-label="drag handle" />
            <th scope="col" class="w-10 px-3 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-400">#</th>
            <SortableTh :active="sortField === 'position'" :dir="sortDir" @click="setSort('position')">{{ t('admin.waitingList.position') }}</SortableTh>
            <SortableTh :active="sortField === 'name'" :dir="sortDir" @click="setSort('name')">{{ t('admin.waitingList.name') }}</SortableTh>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.waitingList.email') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.waitingList.local') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.waitingList.boat') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.waitingList.beam') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.waitingList.status') }}</th>
            <SortableTh :active="sortField === 'registered'" :dir="sortDir" @click="setSort('registered')">{{ t('admin.waitingList.registered') }}</SortableTh>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr
            v-for="(entry, index) in sorted"
            :key="entry.id"
            :class="[
              'transition-colors',
              draggingId === entry.id && 'opacity-40',
              dragOverId === entry.id && 'bg-blue-50 ring-inset ring-1 ring-blue-300',
              reorderingId === entry.id && 'bg-yellow-50',
              sortField === 'position' && 'cursor-default',
            ]"
            :draggable="sortField === 'position'"
            @dragstart="onDragStart($event, entry.id)"
            @dragover="onDragOver($event, entry.id)"
            @dragleave="onDragLeave"
            @drop="onDrop($event, entry.id)"
            @dragend="onDragEnd"
          >
            <td v-if="sortField === 'position'" class="px-2 py-3">
              <GripVertical class="h-4 w-4 cursor-grab text-gray-300 hover:text-gray-500" aria-hidden="true" />
            </td>
            <td class="whitespace-nowrap px-3 py-3 text-right text-xs text-gray-400 tabular-nums">{{ offset + index + 1 }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ entry.position }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-900">{{ entry.full_name }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ entry.email }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <span :class="['rounded-full px-2.5 py-0.5 text-xs font-medium', entry.is_local ? 'bg-green-100 text-green-800' : 'bg-yellow-100 text-yellow-800']">
                {{ entry.is_local ? t('common.yes') : t('common.no') }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
              <template v-if="entry.boat_name">
                {{ entry.boat_name }}
                <span v-if="entry.boat_confirmed === false" class="ml-1 text-xs text-yellow-600">{{ t('admin.waitingList.unconfirmed') }}</span>
              </template>
              <span v-else class="text-gray-300">—</span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
              {{ entry.boat_beam ? `${entry.boat_beam} m` : '—' }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ entry.status }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ formatDate(entry.created_at) }}</td>
          </tr>
        </tbody>
      </table>

      <div v-if="hasPrev || hasMore" class="mt-3 flex items-center justify-between text-sm text-gray-600">
        <span class="text-xs text-gray-400">{{ t('common.showingFrom', { from: offset + 1, to: offset + sorted.length }) }}</span>
        <div class="flex gap-2">
          <button class="rounded-md px-3 py-1 ring-1 ring-gray-300 hover:bg-gray-50 disabled:opacity-40" :disabled="!hasPrev" @click="prevPage">{{ t('common.previous') }}</button>
          <button class="rounded-md px-3 py-1 ring-1 ring-gray-300 hover:bg-gray-50 disabled:opacity-40" :disabled="!hasMore" @click="nextPage">{{ t('common.next') }}</button>
        </div>
      </div>
    </div>

    <!-- Add member modal -->
    <Teleport to="body">
      <div
        v-if="showAddModal"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
        @click.self="closeAddModal"
      >
        <div class="w-full max-w-md rounded-xl bg-white p-6 shadow-xl">
          <div class="mb-4 flex items-center justify-between">
            <h2 class="text-lg font-semibold text-gray-900">{{ t('admin.waitingList.addMemberTitle') }}</h2>
            <button class="text-gray-400 hover:text-gray-600" :aria-label="t('common.close')" @click="closeAddModal">
              <X class="h-5 w-5" aria-hidden="true" />
            </button>
          </div>

          <input
            v-model="memberSearch"
            type="search"
            autofocus
            class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            :placeholder="t('admin.waitingList.addMemberSearch')"
          />

          <div v-if="addError" class="mt-2 rounded-md bg-red-50 p-2 text-xs text-red-700">{{ addError }}</div>

          <div class="mt-3 max-h-72 overflow-y-auto divide-y divide-gray-100 rounded-md border border-gray-200">
            <div v-if="searchLoading" class="px-3 py-4 text-center text-sm text-gray-400">{{ t('common.loading') }}...</div>
            <div v-else-if="memberSearch && !searchResults.length" class="px-3 py-4 text-center text-sm text-gray-400">
              {{ t('admin.waitingList.addMemberNoResults') }}
            </div>
            <div v-else-if="!memberSearch" class="px-3 py-4 text-center text-sm text-gray-400">
              {{ t('admin.waitingList.addMemberSearch') }}
            </div>
            <button
              v-for="user in searchResults"
              :key="user.id"
              :disabled="enrolledIds.has(user.id) || enrolling"
              class="flex w-full items-center justify-between px-3 py-2.5 text-left text-sm hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50"
              @click="selectMember(user)"
            >
              <div>
                <p class="font-medium text-gray-900">{{ user.full_name }}</p>
                <p class="text-xs text-gray-500">{{ user.email }}</p>
              </div>
              <span v-if="enrolledIds.has(user.id)" class="ml-2 shrink-0 rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500">
                {{ t('admin.waitingList.addMemberAlreadyEnrolled') }}
              </span>
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
