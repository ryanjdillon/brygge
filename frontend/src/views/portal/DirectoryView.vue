<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Search } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()

interface DirectoryMember {
  id: string
  name: string
  phone: string | null
  email: string | null
}

const searchQuery = ref('')

const { data: members, isLoading, isError } = useQuery({
  queryKey: ['portal', 'directory'],
  queryFn: () => fetchApi<DirectoryMember[]>('/api/v1/members/directory'),
})

const filteredMembers = computed(() => {
  if (!members.value) return []
  const q = searchQuery.value.toLowerCase().trim()
  if (!q) return members.value
  return members.value.filter((m) => m.name.toLowerCase().includes(q))
})
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.directory.title') }}</h1>

    <div class="mt-6 relative max-w-sm">
      <Search class="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
      <input
        v-model="searchQuery"
        type="text"
        :placeholder="t('portal.directory.searchPlaceholder')"
        class="block w-full rounded-md border border-gray-300 py-2 pl-10 pr-3 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
      />
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">
      {{ t('portal.directory.loadError') }}
    </div>

    <div v-else-if="!filteredMembers.length" class="mt-6 text-gray-500">
      {{ t('portal.directory.noMembers') }}
    </div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.directory.name') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.directory.phone') }}</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.directory.email') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="member in filteredMembers" :key="member.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ member.name }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ member.phone ?? '—' }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
              <a v-if="member.email" :href="`mailto:${member.email}`" class="text-blue-600 hover:text-blue-800">
                {{ member.email }}
              </a>
              <span v-else>—</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
