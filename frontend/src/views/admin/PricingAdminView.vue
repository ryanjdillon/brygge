<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Plus, Pencil, Trash2, X } from 'lucide-vue-next'
import type { PriceItem } from '@/composables/usePricing'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

const { data: response, isLoading } = useQuery({
  queryKey: ['admin', 'pricing'],
  queryFn: () => fetchApi<{ items: PriceItem[] }>('/api/v1/admin/pricing'),
})

const categories = [
  { value: 'moloandel', label: 'Moloandel' },
  { value: 'slip_fee', label: 'Plassleie' },
  { value: 'seasonal_rental', label: 'Sesongplass' },
  { value: 'guest', label: 'Gjesteplasser' },
  { value: 'bobil', label: 'Bobilparkering' },
  { value: 'room_hire', label: 'Romutleie' },
  { value: 'service', label: 'Tjenester' },
  { value: 'other', label: 'Annet' },
]

const units = [
  { value: 'once', label: 'Engangs' },
  { value: 'year', label: 'Per år' },
  { value: 'season', label: 'Per sesong' },
  { value: 'day', label: 'Per døgn' },
  { value: 'night', label: 'Per natt' },
  { value: 'hour', label: 'Per time' },
]

interface FormData {
  id?: string
  category: string
  name: string
  description: string
  amount: string
  unit: string
  installments_allowed: boolean
  max_installments: string
  sort_order: string
  is_active: boolean
  season: string
  period_start: string
  period_end: string
}

const emptyForm: FormData = {
  category: 'service',
  name: '',
  description: '',
  amount: '',
  unit: 'once',
  installments_allowed: false,
  max_installments: '1',
  sort_order: '0',
  is_active: true,
  season: '',
  period_start: '',
  period_end: '',
}

const showForm = ref(false)
const form = ref<FormData>({ ...emptyForm })
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function openCreate() {
  form.value = { ...emptyForm }
  showForm.value = true
}

function openEdit(item: PriceItem) {
  form.value = {
    id: item.id,
    category: item.category,
    name: item.name,
    description: item.description,
    amount: String(item.amount),
    unit: item.unit,
    installments_allowed: item.installments_allowed,
    max_installments: String(item.max_installments),
    sort_order: String(item.sort_order),
    is_active: item.is_active,
    season: item.metadata?.season ?? '',
    period_start: item.metadata?.period_start ?? '',
    period_end: item.metadata?.period_end ?? '',
  }
  showForm.value = true
}

function buildPayload() {
  const metadata: Record<string, string> = {}
  if (form.value.season) metadata.season = form.value.season
  if (form.value.period_start) metadata.period_start = form.value.period_start
  if (form.value.period_end) metadata.period_end = form.value.period_end

  return {
    category: form.value.category,
    name: form.value.name,
    description: form.value.description,
    amount: parseFloat(form.value.amount) || 0,
    unit: form.value.unit,
    installments_allowed: form.value.installments_allowed,
    max_installments: parseInt(form.value.max_installments) || 1,
    sort_order: parseInt(form.value.sort_order) || 0,
    is_active: form.value.is_active,
    metadata,
  }
}

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const { mutate: saveItem, isPending: isSaving } = useMutation({
  mutationFn: () => {
    const payload = buildPayload()
    if (form.value.id) {
      return fetchApi(`/api/v1/admin/pricing/${form.value.id}`, {
        method: 'PUT',
        body: JSON.stringify(payload),
      })
    }
    return fetchApi('/api/v1/admin/pricing', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'pricing'] })
    queryClient.invalidateQueries({ queryKey: ['pricing'] })
    showForm.value = false
    showToast('success', form.value.id ? 'Pris oppdatert' : 'Pris opprettet')
  },
  onError: () => showToast('error', 'Kunne ikke lagre'),
})

const { mutate: deleteItem } = useMutation({
  mutationFn: (id: string) =>
    fetchApi(`/api/v1/admin/pricing/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'pricing'] })
    queryClient.invalidateQueries({ queryKey: ['pricing'] })
    showToast('success', 'Pris slettet')
  },
  onError: () => showToast('error', 'Kunne ikke slette'),
})

function confirmDelete(id: string) {
  if (confirm('Er du sikker på at du vil slette denne prisen?')) {
    deleteItem(id)
  }
}

function categoryLabel(value: string): string {
  return categories.find((c) => c.value === value)?.label ?? value
}

function unitLabel(value: string): string {
  return units.find((u) => u.value === value)?.label ?? value
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.pricing') }}</h1>
      <button
        v-if="!showForm"
        class="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
        @click="openCreate"
      >
        <Plus class="h-4 w-4" />
        Ny pris
      </button>
    </div>

    <div
      v-if="toast"
      :class="['mt-4 rounded-md p-3 text-sm', toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800']"
    >
      {{ toast.message }}
    </div>

    <!-- Form -->
    <form
      v-if="showForm"
      class="mt-6 max-w-2xl space-y-4 rounded-lg border border-gray-200 bg-white p-5"
      @submit.prevent="saveItem()"
    >
      <div class="flex items-center justify-between">
        <h2 class="text-lg font-semibold text-gray-900">
          {{ form.id ? 'Rediger pris' : 'Ny pris' }}
        </h2>
        <button type="button" class="text-gray-400 hover:text-gray-600" @click="showForm = false">
          <X class="h-5 w-5" />
        </button>
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">Kategori</label>
          <select
            v-model="form.category"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            <option v-for="cat in categories" :key="cat.value" :value="cat.value">
              {{ cat.label }}
            </option>
          </select>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">Navn</label>
          <input
            v-model="form.name"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">Beskrivelse</label>
        <input
          v-model="form.description"
          type="text"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <div class="grid grid-cols-3 gap-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">Beløp (kr)</label>
          <input
            v-model="form.amount"
            type="number"
            step="1"
            min="0"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">Enhet</label>
          <select
            v-model="form.unit"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            <option v-for="u in units" :key="u.value" :value="u.value">{{ u.label }}</option>
          </select>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">Sortering</label>
          <input
            v-model="form.sort_order"
            type="number"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
      </div>

      <!-- Installments -->
      <div class="flex items-center gap-4">
        <label class="flex items-center gap-2 text-sm text-gray-700">
          <input v-model="form.installments_allowed" type="checkbox" class="rounded border-gray-300" />
          Kan betales i avdrag
        </label>
        <div v-if="form.installments_allowed" class="flex items-center gap-2">
          <label class="text-sm text-gray-700">Maks avdrag:</label>
          <input
            v-model="form.max_installments"
            type="number"
            min="2"
            max="24"
            class="w-20 rounded-md border border-gray-300 px-2 py-1 text-sm"
          />
        </div>
      </div>

      <!-- Season metadata (for seasonal items) -->
      <div v-if="form.unit === 'season'" class="rounded-md border border-gray-100 bg-gray-50 p-4">
        <p class="mb-3 text-sm font-medium text-gray-700">Sesongperiode</p>
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="block text-xs text-gray-500">Sesong</label>
            <select
              v-model="form.season"
              class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
            >
              <option value="">Ingen</option>
              <option value="summer">Sommer</option>
              <option value="winter">Vinter</option>
            </select>
          </div>
          <div>
            <label class="block text-xs text-gray-500">Fra (MM-DD)</label>
            <input
              v-model="form.period_start"
              type="text"
              placeholder="05-01"
              class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label class="block text-xs text-gray-500">Til (MM-DD)</label>
            <input
              v-model="form.period_end"
              type="text"
              placeholder="09-30"
              class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
            />
          </div>
        </div>
      </div>

      <div class="flex items-center gap-4">
        <label class="flex items-center gap-2 text-sm text-gray-700">
          <input v-model="form.is_active" type="checkbox" class="rounded border-gray-300" />
          Aktiv (synlig på prissiden)
        </label>
      </div>

      <div class="flex gap-3 pt-2">
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
          @click="showForm = false"
        >
          {{ t('common.cancel') }}
        </button>
      </div>
    </form>

    <!-- Table -->
    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Navn</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Kategori</th>
            <th class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">Beløp</th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Enhet</th>
            <th class="px-4 py-3 text-center text-xs font-medium uppercase tracking-wider text-gray-500">Status</th>
            <th class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">Handlinger</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-if="!response?.items?.length">
            <td colspan="6" class="px-4 py-8 text-center text-gray-500">Ingen priser lagt inn</td>
          </tr>
          <tr v-for="item in response?.items" :key="item.id">
            <td class="px-4 py-3 text-sm">
              <div class="font-medium text-gray-900">{{ item.name }}</div>
              <div v-if="item.description" class="text-xs text-gray-500">{{ item.description }}</div>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <span class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700">
                {{ categoryLabel(item.category) }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm font-medium text-gray-900">
              {{ item.amount.toLocaleString('nb-NO') }} kr
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
              {{ unitLabel(item.unit) }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-center text-sm">
              <span
                :class="[
                  'rounded-full px-2 py-0.5 text-xs font-medium',
                  item.is_active ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-500',
                ]"
              >
                {{ item.is_active ? 'Aktiv' : 'Inaktiv' }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm">
              <button class="mr-2 text-gray-500 hover:text-blue-600" @click="openEdit(item)">
                <Pencil class="h-4 w-4" />
              </button>
              <button class="text-gray-500 hover:text-red-600" @click="confirmDelete(item.id)">
                <Trash2 class="h-4 w-4" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
