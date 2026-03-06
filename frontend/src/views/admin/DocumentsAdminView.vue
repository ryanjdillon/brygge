<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

const { t } = useI18n()
const { fetchApi } = useApi()

interface Document {
  id: string
  title: string
  filename: string
  visibility: string
  created_at: string
  uploaded_by: string
}

const { data: documents, isLoading, isError } = useQuery({
  queryKey: ['admin', 'documents'],
  queryFn: () => fetchApi<Document[]>('/api/v1/documents'),
})
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.documents') }}</h1>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">Kunne ikke hente dokumenter</div>

    <div v-else-if="!documents?.length" class="mt-6 text-gray-500">Ingen dokumenter</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Tittel</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Fil</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Synlighet</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Dato</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="doc in documents" :key="doc.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ doc.title }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ doc.filename }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <span class="rounded-full bg-gray-100 px-2.5 py-0.5 text-xs font-medium text-gray-700">{{ doc.visibility }}</span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ new Date(doc.created_at).toLocaleDateString() }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
