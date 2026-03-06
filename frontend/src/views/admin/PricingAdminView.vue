<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

const { data: pricing, isLoading } = useQuery({
  queryKey: ['pricing'],
  queryFn: () => fetchApi<Record<string, number>>('/api/v1/pricing'),
})

const form = ref<Record<string, number>>({})
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

watch(pricing, (p) => {
  if (p) form.value = { ...p }
}, { immediate: true })

const { mutate: savePricing, isPending } = useMutation({
  mutationFn: () =>
    fetchApi('/api/v1/admin/pricing', {
      method: 'PUT',
      body: JSON.stringify(form.value),
    }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['pricing'] })
    toast.value = { type: 'success', message: 'Priser oppdatert' }
    setTimeout(() => (toast.value = null), 3000)
  },
  onError: () => {
    toast.value = { type: 'error', message: 'Kunne ikke oppdatere priser' }
    setTimeout(() => (toast.value = null), 3000)
  },
})
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.pricing') }}</h1>

    <div
      v-if="toast"
      :class="['mt-4 rounded-md p-3 text-sm', toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800']"
    >
      {{ toast.message }}
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <form v-else class="mt-6 max-w-lg space-y-4" @submit.prevent="savePricing()">
      <div v-for="(value, key) in form" :key="key">
        <label class="block text-sm font-medium text-gray-700">{{ key }}</label>
        <input
          v-model.number="form[key]"
          type="number"
          step="1"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>
      <button
        type="submit"
        :disabled="isPending"
        class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
      >
        {{ t('common.save') }}
      </button>
    </form>
  </div>
</template>
