<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Plus, Pencil, Trash2, Search, ShieldCheck, AlertTriangle } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

import type { components } from '@/types/api'

type BoatModel = components['schemas']['BoatModel']
type Boat = components['schemas']['Boat']

interface BoatForm {
  name: string
  type: string
  manufacturer: string
  model: string
  length_m?: number
  beam_m?: number
  draft_m?: number
  weight_kg?: number
  registration_number: string
  boat_model_id?: string
}

const emptyForm = (): BoatForm => ({
  name: '',
  type: '',
  manufacturer: '',
  model: '',
  length_m: undefined,
  beam_m: undefined,
  draft_m: undefined,
  weight_kg: undefined,
  registration_number: '',
  boat_model_id: undefined,
})

const showForm = ref(false)
const editingId = ref<string | null>(null)
const editingConfirmed = ref(false)
const form = ref<BoatForm>(emptyForm())
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

// Model search
const modelQuery = ref('')
const modelResults = ref<BoatModel[]>([])
const showModelResults = ref(false)
let searchTimeout: ReturnType<typeof setTimeout> | null = null

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

watch(modelQuery, (q) => {
  if (searchTimeout) clearTimeout(searchTimeout)
  if (q.length < 2) {
    modelResults.value = []
    showModelResults.value = false
    return
  }
  searchTimeout = setTimeout(async () => {
    try {
      const results = await fetchApi<BoatModel[]>(`/api/v1/boat-models?q=${encodeURIComponent(q)}`)
      modelResults.value = results
      showModelResults.value = results.length > 0
    } catch {
      modelResults.value = []
    }
  }, 300)
})

function delayHideResults() {
  setTimeout(() => (showModelResults.value = false), 200)
}

function selectModel(m: BoatModel) {
  form.value.manufacturer = m.manufacturer
  form.value.model = m.model
  form.value.type = m.boat_type
  form.value.length_m = m.length_m
  form.value.beam_m = m.beam_m
  form.value.draft_m = m.draft_m
  form.value.weight_kg = m.weight_kg
  form.value.boat_model_id = m.id
  modelQuery.value = `${m.manufacturer} ${m.model}`
  showModelResults.value = false
}

const { data: boats, isLoading } = useQuery({
  queryKey: ['portal', 'boats'],
  queryFn: () => fetchApi<Boat[]>('/api/v1/members/me/boats'),
})

const { mutate: saveBoat, isPending: isSaving } = useMutation({
  mutationFn: () => {
    // If editing a confirmed boat and user changed dimensions, show warning
    if (editingId.value && editingConfirmed.value) {
      const original = boats.value?.find((b) => b.id === editingId.value)
      if (original) {
        const dimsChanged =
          form.value.length_m !== original.length_m ||
          form.value.beam_m !== original.beam_m ||
          form.value.draft_m !== original.draft_m
        if (dimsChanged) {
          if (
            !confirm(
              'Endring av mål vil kreve ny godkjenning fra styret. Vil du fortsette?',
            )
          ) {
            return Promise.reject(new Error('cancelled'))
          }
        }
      }
    }

    const url = editingId.value
      ? `/api/v1/members/me/boats/${editingId.value}`
      : '/api/v1/members/me/boats'
    const method = editingId.value ? 'PUT' : 'POST'
    return fetchApi<Boat>(url, { method, body: JSON.stringify(form.value) })
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'boats'] })
    showToast('success', t('portal.boats.saveSuccess'))
    closeForm()
  },
  onError: (err) => {
    if (err.message === 'cancelled') return
    showToast('error', t('portal.boats.saveError'))
  },
})

const { mutate: deleteBoat } = useMutation({
  mutationFn: (id: string) =>
    fetchApi(`/api/v1/members/me/boats/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'boats'] })
    showToast('success', t('portal.boats.deleteSuccess'))
  },
  onError: () => {
    showToast('error', t('portal.boats.deleteError'))
  },
})

function openAdd() {
  editingId.value = null
  editingConfirmed.value = false
  form.value = emptyForm()
  modelQuery.value = ''
  showForm.value = true
}

function openEdit(boat: Boat) {
  editingId.value = boat.id
  editingConfirmed.value = boat.measurements_confirmed
  form.value = {
    name: boat.name,
    type: boat.type,
    manufacturer: boat.manufacturer,
    model: boat.model,
    length_m: boat.length_m,
    beam_m: boat.beam_m,
    draft_m: boat.draft_m,
    weight_kg: boat.weight_kg,
    registration_number: boat.registration_number,
    boat_model_id: boat.boat_model_id,
  }
  modelQuery.value = boat.manufacturer && boat.model ? `${boat.manufacturer} ${boat.model}` : ''
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingId.value = null
  editingConfirmed.value = false
  form.value = emptyForm()
  modelQuery.value = ''
}

function confirmDelete(id: string) {
  if (confirm(t('portal.boats.deleteConfirm'))) {
    deleteBoat(id)
  }
}

function formatDim(v?: number): string {
  return v != null ? `${v} m` : '—'
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.boats.title') }}</h1>
      <button
        v-if="!showForm"
        class="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
        @click="openAdd"
      >
        <Plus class="h-4 w-4" />
        {{ t('portal.boats.addBoat') }}
      </button>
    </div>

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

    <template v-else>
      <form
        v-if="showForm"
        class="mt-6 max-w-lg space-y-4 rounded-lg border border-gray-200 bg-white p-5"
        @submit.prevent="saveBoat()"
      >
        <h2 class="text-lg font-semibold text-gray-900">
          {{ editingId ? t('portal.boats.editBoat') : t('portal.boats.addBoat') }}
        </h2>

        <!-- Model search -->
        <div class="relative">
          <label for="model-search" class="block text-sm font-medium text-gray-700">
            {{ t('portal.boats.searchModel') }}
          </label>
          <div class="relative mt-1">
            <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
            <input
              id="model-search"
              v-model="modelQuery"
              type="text"
              :placeholder="t('portal.boats.searchModelPlaceholder')"
              class="block w-full rounded-md border border-gray-300 py-2 pl-9 pr-3 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              @focus="showModelResults = modelResults.length > 0"
              @blur="delayHideResults()"
            />
          </div>
          <div
            v-if="showModelResults"
            class="absolute z-10 mt-1 max-h-60 w-full overflow-auto rounded-md border border-gray-200 bg-white shadow-lg"
          >
            <button
              v-for="m in modelResults"
              :key="m.id"
              type="button"
              class="flex w-full items-center justify-between px-4 py-2.5 text-left text-sm hover:bg-blue-50"
              @mousedown.prevent="selectModel(m)"
            >
              <div>
                <span class="font-medium text-gray-900">{{ m.manufacturer }} {{ m.model }}</span>
                <span v-if="m.year_from" class="ml-1 text-gray-500">({{ m.year_from }}{{ m.year_to ? `–${m.year_to}` : '+' }})</span>
              </div>
              <span class="text-xs text-gray-400">
                {{ m.length_m }}×{{ m.beam_m }}×{{ m.draft_m }} m
              </span>
            </button>
          </div>
        </div>

        <div class="grid grid-cols-2 gap-3">
          <div>
            <label for="boat-manufacturer" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.manufacturer') }}</label>
            <input
              id="boat-manufacturer"
              v-model="form.manufacturer"
              type="text"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label for="boat-model" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.model') }}</label>
            <input
              id="boat-model"
              v-model="form.model"
              type="text"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
        </div>

        <div>
          <label for="boat-name" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.name') }}</label>
          <input
            id="boat-name"
            v-model="form.name"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <div>
            <label for="boat-length" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.length') }}</label>
            <input
              id="boat-length"
              v-model.number="form.length_m"
              type="number"
              step="0.01"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label for="boat-beam" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.beam') }}</label>
            <input
              id="boat-beam"
              v-model.number="form.beam_m"
              type="number"
              step="0.01"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label for="boat-draft" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.draft') }}</label>
            <input
              id="boat-draft"
              v-model.number="form.draft_m"
              type="number"
              step="0.01"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label for="boat-weight" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.weight') }}</label>
            <input
              id="boat-weight"
              v-model.number="form.weight_kg"
              type="number"
              step="1"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
        </div>

        <div>
          <label for="boat-reg" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.registrationNumber') }}</label>
          <input
            id="boat-reg"
            v-model="form.registration_number"
            type="text"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div class="flex gap-3">
          <button
            type="submit"
            :disabled="isSaving"
            class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
          >
            {{ t('common.save') }}
          </button>
          <button
            type="button"
            class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50"
            @click="closeForm"
          >
            {{ t('common.cancel') }}
          </button>
        </div>
      </form>

      <div v-if="!boats?.length && !showForm" class="mt-6 text-gray-500">
        {{ t('portal.boats.noBoats') }}
      </div>

      <div v-else-if="boats?.length" class="mt-6 space-y-3">
        <div
          v-for="boat in boats"
          :key="boat.id"
          class="rounded-lg border border-gray-200 bg-white p-4"
        >
          <div class="flex items-start justify-between">
            <div>
              <div class="flex items-center gap-2">
                <span class="text-lg font-semibold text-gray-900">{{ boat.name }}</span>
                <span
                  v-if="boat.measurements_confirmed"
                  class="inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-800"
                >
                  <ShieldCheck class="h-3 w-3" />
                  {{ t('portal.boats.confirmed') }}
                </span>
                <span
                  v-else
                  class="inline-flex items-center gap-1 rounded-full bg-yellow-100 px-2 py-0.5 text-xs font-medium text-yellow-800"
                >
                  <AlertTriangle class="h-3 w-3" />
                  {{ t('portal.boats.pendingConfirmation') }}
                </span>
              </div>
              <p v-if="boat.manufacturer || boat.model" class="mt-0.5 text-sm text-gray-500">
                {{ boat.manufacturer }} {{ boat.model }}
              </p>
            </div>
            <div class="flex gap-2">
              <button
                class="text-blue-600 hover:text-blue-800"
                :title="t('common.edit')"
                @click="openEdit(boat)"
              >
                <Pencil class="h-4 w-4" />
              </button>
              <button
                class="text-red-600 hover:text-red-800"
                :title="t('common.delete')"
                @click="confirmDelete(boat.id)"
              >
                <Trash2 class="h-4 w-4" />
              </button>
            </div>
          </div>
          <div class="mt-3 grid grid-cols-2 gap-x-6 gap-y-1 text-sm sm:grid-cols-4">
            <div>
              <span class="text-gray-500">{{ t('portal.boats.length') }}:</span>
              <span class="ml-1 text-gray-900">{{ formatDim(boat.length_m) }}</span>
            </div>
            <div>
              <span class="text-gray-500">{{ t('portal.boats.beam') }}:</span>
              <span class="ml-1 text-gray-900">{{ formatDim(boat.beam_m) }}</span>
            </div>
            <div>
              <span class="text-gray-500">{{ t('portal.boats.draft') }}:</span>
              <span class="ml-1 text-gray-900">{{ formatDim(boat.draft_m) }}</span>
            </div>
            <div v-if="boat.weight_kg">
              <span class="text-gray-500">{{ t('portal.boats.weight') }}:</span>
              <span class="ml-1 text-gray-900">{{ boat.weight_kg }} kg</span>
            </div>
          </div>
          <div v-if="boat.registration_number" class="mt-1 text-sm text-gray-500">
            {{ t('portal.boats.registrationNumber') }}: {{ boat.registration_number }}
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
