<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

const { t } = useI18n()
const { fetchApi } = useApi()

interface WaitingListEntry {
  id: string
  user_id: string
  full_name: string
  email: string
  position: number
  is_local: boolean
  status: string
  created_at: string
  boat_id: string | null
  boat_name: string | null
  boat_beam: number | null
  boat_confirmed: boolean | null
}

const { data: entries, isLoading, isError } = useQuery({
  queryKey: ['admin', 'waiting-list'],
  queryFn: () => fetchApi<WaitingListEntry[]>('/api/v1/waiting-list'),
})
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.waitingList') }}</h1>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">Kunne ikke hente ventelisten</div>

    <div v-else-if="!entries?.length" class="mt-6 text-gray-500">Ingen på ventelisten</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">#</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Navn</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">E-post</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Lokal</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">B&aring;t</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Bredde</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Status</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Registrert</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="entry in entries" :key="entry.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ entry.position }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-900">{{ entry.full_name }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ entry.email }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <span :class="['rounded-full px-2.5 py-0.5 text-xs font-medium', entry.is_local ? 'bg-green-100 text-green-800' : 'bg-yellow-100 text-yellow-800']">
                {{ entry.is_local ? 'Ja' : 'Nei' }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
              <template v-if="entry.boat_name">
                {{ entry.boat_name }}
                <span v-if="entry.boat_confirmed === false" class="ml-1 text-xs text-yellow-600">(ubekreftet)</span>
              </template>
              <span v-else class="text-gray-300">—</span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
              {{ entry.boat_beam ? `${entry.boat_beam} m` : '—' }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ entry.status }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ new Date(entry.created_at).toLocaleDateString() }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
