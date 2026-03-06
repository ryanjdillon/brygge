<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Plus } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

interface Slip {
  id: string
  number: string
  section: string
  length_m: number | null
  width_m: number | null
  depth_m: number | null
  status: string
  assignee_name: string | null
}

const { data: slips, isLoading, isError } = useQuery({
  queryKey: ['admin', 'slips'],
  queryFn: () => fetchApi<Slip[]>('/api/v1/admin/slips'),
})

const showForm = ref(false)
const form = ref({ number: '', section: '', length_m: '', width_m: '', depth_m: '' })

const { mutate: createSlip, isPending: isCreating } = useMutation({
  mutationFn: () =>
    fetchApi('/api/v1/admin/slips', {
      method: 'POST',
      body: JSON.stringify({
        number: form.value.number,
        section: form.value.section,
        length_m: form.value.length_m ? parseFloat(form.value.length_m) : null,
        width_m: form.value.width_m ? parseFloat(form.value.width_m) : null,
        depth_m: form.value.depth_m ? parseFloat(form.value.depth_m) : null,
      }),
    }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'slips'] })
    showForm.value = false
    form.value = { number: '', section: '', length_m: '', width_m: '', depth_m: '' }
  },
})
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.slips') }}</h1>
      <button
        v-if="!showForm"
        class="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
        @click="showForm = true"
      >
        <Plus class="h-4 w-4" />
        Ny plass
      </button>
    </div>

    <form
      v-if="showForm"
      class="mt-6 max-w-lg space-y-4 rounded-lg border border-gray-200 bg-white p-5"
      @submit.prevent="createSlip()"
    >
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="block text-sm font-medium text-gray-700">Nummer</label>
          <input v-model="form.number" type="text" required class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">Seksjon</label>
          <input v-model="form.section" type="text" required class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
      </div>
      <div class="grid grid-cols-3 gap-3">
        <div>
          <label class="block text-sm font-medium text-gray-700">Lengde (m)</label>
          <input v-model="form.length_m" type="number" step="0.1" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">Bredde (m)</label>
          <input v-model="form.width_m" type="number" step="0.1" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">Dybde (m)</label>
          <input v-model="form.depth_m" type="number" step="0.1" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
      </div>
      <div class="flex gap-3">
        <button type="submit" :disabled="isCreating" class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50">{{ t('common.save') }}</button>
        <button type="button" class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50" @click="showForm = false">{{ t('common.cancel') }}</button>
      </div>
    </form>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">Kunne ikke hente plasser</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Nummer</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Seksjon</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Størrelse</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Status</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Tildelt</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="slip in slips" :key="slip.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">#{{ slip.number }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ slip.section }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ slip.length_m ?? '—' }} × {{ slip.width_m ?? '—' }} m</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <span :class="['rounded-full px-2.5 py-0.5 text-xs font-medium', slip.status === 'available' ? 'bg-green-100 text-green-800' : 'bg-blue-100 text-blue-800']">
                {{ slip.status }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ slip.assignee_name ?? '—' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
