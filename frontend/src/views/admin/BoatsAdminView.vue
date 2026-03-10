<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { ShieldCheck, ChevronDown, ChevronUp } from 'lucide-vue-next'
import type { components } from '@/types/api'

type UnconfirmedBoat = components['schemas']['UnconfirmedBoat']

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

const { data: boats, isLoading, isError } = useQuery({
  queryKey: ['admin', 'boats', 'unconfirmed'],
  queryFn: () => fetchApi<UnconfirmedBoat[]>('/api/v1/admin/boats/unconfirmed'),
})

const expanded = ref<string | null>(null)
const adjusting = ref<Record<string, { length_m?: number; beam_m?: number; draft_m?: number; weight_kg?: number }>>({})
const addToModels = ref<Record<string, boolean>>({})
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

function toggle(id: string, boat: UnconfirmedBoat) {
  if (expanded.value === id) {
    expanded.value = null
  } else {
    expanded.value = id
    if (!adjusting.value[id]) {
      adjusting.value[id] = {
        length_m: boat.length_m,
        beam_m: boat.beam_m,
        draft_m: boat.draft_m,
        weight_kg: boat.weight_kg,
      }
    }
    if (addToModels.value[id] === undefined) {
      addToModels.value[id] = false
    }
  }
}

const { mutate: confirmBoat, isPending: isConfirming } = useMutation({
  mutationFn: (boatId: string) => {
    const adj = adjusting.value[boatId]
    return fetchApi(`/api/v1/admin/boats/${boatId}/confirm`, {
      method: 'POST',
      body: JSON.stringify({
        length_m: adj?.length_m,
        beam_m: adj?.beam_m,
        draft_m: adj?.draft_m,
        weight_kg: adj?.weight_kg,
        add_to_models: addToModels.value[boatId] || false,
      }),
    })
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'boats', 'unconfirmed'] })
    showToast('success', t('admin.boats.approveSuccess'))
    expanded.value = null
  },
  onError: () => {
    showToast('error', t('admin.boats.approveError'))
  },
})

</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.boats') }}</h1>
    <p class="mt-1 text-sm text-gray-500">{{ t('admin.boats.description') }}</p>

    <div
      v-if="toast"
      :class="[
        'mt-4 rounded-md p-3 text-sm',
        toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800',
      ]"
    >
      {{ toast.message }}
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">{{ t('admin.boats.loadError') }}</div>

    <div v-else-if="!boats?.length" class="mt-6 flex flex-col items-center gap-2 py-12 text-gray-400">
      <ShieldCheck class="h-10 w-10" />
      <p>{{ t('admin.boats.allApproved') }}</p>
    </div>

    <div v-else class="mt-6 space-y-3">
      <div
        v-for="boat in boats"
        :key="boat.id"
        class="rounded-lg border border-gray-200 bg-white"
      >
        <button
          class="flex w-full items-center justify-between px-4 py-3 text-left"
          @click="toggle(boat.id, boat)"
        >
          <div>
            <span class="font-semibold text-gray-900">{{ boat.name }}</span>
            <span v-if="boat.manufacturer || boat.model" class="ml-2 text-sm text-gray-500">
              {{ boat.manufacturer }} {{ boat.model }}
            </span>
            <span class="ml-2 text-xs text-gray-400">— {{ boat.owner_name }}</span>
          </div>
          <component :is="expanded === boat.id ? ChevronUp : ChevronDown" class="h-4 w-4 text-gray-400" />
        </button>

        <div v-if="expanded === boat.id" class="border-t border-gray-100 px-4 py-4">
          <div class="mb-3 text-sm text-gray-600">
            {{ t('admin.boats.ownerDimensions') }}
          </div>

          <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div>
              <label class="block text-xs font-medium text-gray-500">{{ t('admin.boats.length') }}</label>
              <input
                v-model.number="adjusting[boat.id].length_m"
                type="number"
                step="0.01"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-500">{{ t('admin.boats.beam') }}</label>
              <input
                v-model.number="adjusting[boat.id].beam_m"
                type="number"
                step="0.01"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-500">{{ t('admin.boats.draft') }}</label>
              <input
                v-model.number="adjusting[boat.id].draft_m"
                type="number"
                step="0.01"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-500">{{ t('admin.boats.weight') }}</label>
              <input
                v-model.number="adjusting[boat.id].weight_kg"
                type="number"
                step="1"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          <div v-if="!boat.boat_model_id" class="mt-3">
            <label class="flex items-center gap-2 text-sm text-gray-700">
              <input v-model="addToModels[boat.id]" type="checkbox" class="rounded border-gray-300" />
              {{ t('admin.boats.addToModels') }}
            </label>
          </div>

          <div class="mt-4 flex gap-3">
            <button
              :disabled="isConfirming"
              class="flex items-center gap-1.5 rounded-md bg-green-600 px-3 py-1.5 text-sm font-semibold text-white shadow-sm hover:bg-green-700 disabled:opacity-50"
              @click="confirmBoat(boat.id)"
            >
              <ShieldCheck class="h-4 w-4" />
              {{ t('admin.boats.approveDimensions') }}
            </button>
            <button
              class="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50"
              @click="expanded = null"
            >
              {{ t('common.cancel') }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
