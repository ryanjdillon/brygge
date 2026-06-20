<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { formatDate } from '@/lib/format'
import SortableTh from '@/components/admin/SortableTh.vue'

const { t } = useI18n()
const client = useApiClient()

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
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.waitingList') }}</h1>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">{{ t('admin.waitingList.loadError') }}</div>

    <div v-else-if="!sorted.length" class="mt-6 text-gray-500">{{ t('admin.waitingList.noEntries') }}</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
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
          <tr v-for="(entry, index) in sorted" :key="entry.id">
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
  </div>
</template>
