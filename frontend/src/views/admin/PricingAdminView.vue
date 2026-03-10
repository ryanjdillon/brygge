<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Plus, Pencil, Trash2, X } from 'lucide-vue-next'
import type { PriceItem } from '@/composables/usePricing'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

function meta(item: PriceItem): Record<string, string> {
  return (item.metadata ?? {}) as Record<string, string>
}

const { data: response, isLoading } = useQuery({
  queryKey: ['admin', 'pricing'],
  queryFn: () => fetchApi<{ items: PriceItem[] }>('/api/v1/admin/pricing'),
})

const categories = computed(() => [
  { value: 'moloandel', label: t('admin.pricing.categoryMoloandel') },
  { value: 'slip_fee', label: t('admin.pricing.categorySlipFee') },
  { value: 'seasonal_rental', label: t('admin.pricing.categorySeasonalRental') },
  { value: 'guest', label: t('admin.pricing.categoryGuest') },
  { value: 'bobil', label: t('admin.pricing.categoryBobil') },
  { value: 'room_hire', label: t('admin.pricing.categoryRoomHire') },
  { value: 'service', label: t('admin.pricing.categoryService') },
  { value: 'other', label: t('admin.pricing.categoryOther') },
])

const units = computed(() => [
  { value: 'once', label: t('admin.pricing.unitOnce') },
  { value: 'year', label: t('admin.pricing.unitYear') },
  { value: 'season', label: t('admin.pricing.unitSeason') },
  { value: 'day', label: t('admin.pricing.unitDay') },
  { value: 'night', label: t('admin.pricing.unitNight') },
  { value: 'hour', label: t('admin.pricing.unitHour') },
])

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
  beam_min: string
  beam_max: string
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
  beam_min: '',
  beam_max: '',
}

const showForm = ref(false)
const form = ref<FormData>({ ...emptyForm })
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function openCreate() {
  form.value = { ...emptyForm }
  showForm.value = true
}

function openEdit(item: PriceItem) {
  const meta = (item.metadata ?? {}) as Record<string, string>
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
    season: meta.season ?? '',
    period_start: meta.period_start ?? '',
    period_end: meta.period_end ?? '',
    beam_min: meta.beam_min ?? '',
    beam_max: meta.beam_max ?? '',
  }
  showForm.value = true
}

function buildPayload() {
  const metadata: Record<string, string> = {}
  if (form.value.season) metadata.season = form.value.season
  if (form.value.period_start) metadata.period_start = form.value.period_start
  if (form.value.period_end) metadata.period_end = form.value.period_end
  if (form.value.category === 'slip_fee') {
    if (form.value.beam_min) metadata.beam_min = form.value.beam_min
    if (form.value.beam_max) metadata.beam_max = form.value.beam_max
  }

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
    showToast('success', form.value.id ? t('admin.pricing.priceUpdated') : t('admin.pricing.priceCreated'))
  },
  onError: () => showToast('error', t('admin.pricing.saveError')),
})

const { mutate: deleteItem } = useMutation({
  mutationFn: (id: string) =>
    fetchApi(`/api/v1/admin/pricing/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'pricing'] })
    queryClient.invalidateQueries({ queryKey: ['pricing'] })
    showToast('success', t('admin.pricing.priceDeleted'))
  },
  onError: () => showToast('error', t('admin.pricing.deleteError')),
})

function confirmDelete(id: string) {
  if (confirm(t('admin.pricing.deleteConfirm'))) {
    deleteItem(id)
  }
}

function categoryLabel(value: string): string {
  return categories.value.find((c) => c.value === value)?.label ?? value
}

function unitLabel(value: string): string {
  return units.value.find((u) => u.value === value)?.label ?? value
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
        {{ t('admin.pricing.newPrice') }}
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
          {{ form.id ? t('admin.pricing.editPrice') : t('admin.pricing.newPrice') }}
        </h2>
        <button type="button" class="text-gray-400 hover:text-gray-600" @click="showForm = false">
          <X class="h-5 w-5" />
        </button>
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.category') }}</label>
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
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.name') }}</label>
          <input
            v-model="form.name"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.description') }}</label>
        <input
          v-model="form.description"
          type="text"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <div class="grid grid-cols-3 gap-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.amount') }}</label>
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
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.unit') }}</label>
          <select
            v-model="form.unit"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            <option v-for="u in units" :key="u.value" :value="u.value">{{ u.label }}</option>
          </select>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.sortOrder') }}</label>
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
          {{ t('admin.pricing.installments') }}
        </label>
        <div v-if="form.installments_allowed" class="flex items-center gap-2">
          <label class="text-sm text-gray-700">{{ t('admin.pricing.maxInstallments') }}</label>
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
        <p class="mb-3 text-sm font-medium text-gray-700">{{ t('admin.pricing.seasonPeriod') }}</p>
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.pricing.season') }}</label>
            <select
              v-model="form.season"
              class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
            >
              <option value="">{{ t('admin.pricing.seasonNone') }}</option>
              <option value="summer">{{ t('admin.pricing.seasonSummer') }}</option>
              <option value="winter">{{ t('admin.pricing.seasonWinter') }}</option>
            </select>
          </div>
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.pricing.dateFrom') }}</label>
            <input
              v-model="form.period_start"
              type="text"
              placeholder="05-01"
              class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.pricing.dateTo') }}</label>
            <input
              v-model="form.period_end"
              type="text"
              placeholder="09-30"
              class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
            />
          </div>
        </div>
      </div>

      <!-- Beam range for slip_fee -->
      <div v-if="form.category === 'slip_fee'" class="rounded-md border border-gray-100 bg-gray-50 p-4">
        <p class="mb-3 text-sm font-medium text-gray-700">{{ t('admin.pricing.beamRange') }}</p>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.pricing.beamMin') }}</label>
            <input
              v-model="form.beam_min"
              type="number"
              step="0.1"
              min="0"
              placeholder="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.pricing.beamMax') }}</label>
            <input
              v-model="form.beam_max"
              type="number"
              step="0.1"
              min="0"
              placeholder="99"
              class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
            />
          </div>
        </div>
      </div>

      <div class="flex items-center gap-4">
        <label class="flex items-center gap-2 text-sm text-gray-700">
          <input v-model="form.is_active" type="checkbox" class="rounded border-gray-300" />
          {{ t('admin.pricing.activeCheckbox') }}
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
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.pricing.name') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.pricing.category') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.pricing.amount') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.pricing.unit') }}</th>
            <th scope="col" class="px-4 py-3 text-center text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.status') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-if="!response?.items?.length">
            <td colspan="6" class="px-4 py-8 text-center text-gray-500">{{ t('admin.pricing.noItems') }}</td>
          </tr>
          <tr v-for="item in response?.items" :key="item.id">
            <td class="px-4 py-3 text-sm">
              <div class="font-medium text-gray-900">{{ item.name }}</div>
              <div v-if="item.description" class="text-xs text-gray-500">{{ item.description }}</div>
              <div v-if="item.category === 'slip_fee' && (meta(item).beam_min || meta(item).beam_max)" class="text-xs text-blue-600">
                {{ t('admin.pricing.beam') }}: {{ meta(item).beam_min || '0' }}–{{ meta(item).beam_max || '∞' }} m
              </div>
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
                {{ item.is_active ? t('admin.pricing.active') : t('admin.pricing.inactive') }}
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
