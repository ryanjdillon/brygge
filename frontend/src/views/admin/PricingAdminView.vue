<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { Plus, Pencil, Trash2, X } from 'lucide-vue-next'
import type { PriceItem } from '@/composables/usePricing'

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()

function meta(item: PriceItem): Record<string, string> {
  return (item.metadata ?? {}) as Record<string, string>
}

const { data: response, isLoading } = useQuery({
  queryKey: ['admin', 'pricing'],
  queryFn: async () => unwrap(await client.GET('/api/v1/admin/pricing')),
})

const categories = computed(() => [
  { value: 'membership', label: t('admin.pricing.categoryMembership') },
  { value: 'harbor_membership', label: t('admin.pricing.categoryHarborMembership') },
  { value: 'slip_fee', label: t('admin.pricing.categorySlipFee') },
  { value: 'electricity', label: t('admin.pricing.categoryElectricity') },
  { value: 'seasonal_rental', label: t('admin.pricing.categorySeasonalRental') },
  { value: 'guest', label: t('admin.pricing.categoryGuest') },
  { value: 'motorhome', label: t('admin.pricing.categoryMotorhome') },
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
  { value: 'kwh', label: t('admin.pricing.unitKwh') },
])

const audiences = computed(() => [
  { value: 'all', label: t('admin.pricing.audienceAll') },
  { value: 'member', label: t('admin.pricing.audienceMember') },
  { value: 'non_member', label: t('admin.pricing.audienceNonMember') },
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
  pricing_kind: 'flat' | 'tiered'
  tier_dimension: 'beam' | 'length'
  tier_min: string
  tier_max: string
  show_in_batch: boolean
  show_in_single: boolean
  requires_boat_selection: boolean
  audience: 'all' | 'member' | 'non_member'
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
  pricing_kind: 'flat',
  tier_dimension: 'beam',
  tier_min: '',
  tier_max: '',
  show_in_batch: false,
  show_in_single: true,
  requires_boat_selection: true,
  audience: 'all',
}

const showForm = ref(false)
const form = ref<FormData>({ ...emptyForm })

// show_in_batch and requires_boat_selection are mutually exclusive: the
// batch flow auto-resolves the boat from the user's slip assignment, so
// it can't co-exist with "admin must pick a boat".
watch(
  () => form.value.show_in_batch,
  (v) => {
    if (v) form.value.requires_boat_selection = false
  },
)
watch(
  () => form.value.requires_boat_selection,
  (v) => {
    if (v) form.value.show_in_batch = false
  },
)
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function openCreate() {
  form.value = { ...emptyForm }
  showForm.value = true
}

function openEdit(item: PriceItem) {
  const meta = (item.metadata ?? {}) as Record<string, string>
  const it = item as PriceItem & {
    pricing_kind?: 'flat' | 'tiered'
    tier_dimension?: 'beam' | 'length' | null
    show_in_batch?: boolean
    show_in_single?: boolean
    requires_boat_selection?: boolean
    audience?: 'all' | 'member' | 'non_member'
  }
  const dim: 'beam' | 'length' = it.tier_dimension === 'length' ? 'length' : 'beam'
  const tierMin =
    dim === 'length' ? (meta.length_min ?? '') : (meta.beam_min ?? '')
  const tierMax =
    dim === 'length' ? (meta.length_max ?? '') : (meta.beam_max ?? '')
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
    pricing_kind: it.pricing_kind ?? 'flat',
    tier_dimension: dim,
    tier_min: tierMin,
    tier_max: tierMax,
    show_in_batch: it.show_in_batch ?? false,
    show_in_single: it.show_in_single ?? true,
    requires_boat_selection: it.requires_boat_selection ?? true,
    audience: it.audience ?? 'all',
  }
  showForm.value = true
}

function buildPayload() {
  const metadata: Record<string, string | number> = {}
  if (form.value.season) metadata.season = form.value.season
  if (form.value.period_start) metadata.period_start = form.value.period_start
  if (form.value.period_end) metadata.period_end = form.value.period_end
  if (form.value.pricing_kind === 'tiered') {
    const minKey = form.value.tier_dimension === 'length' ? 'length_min' : 'beam_min'
    const maxKey = form.value.tier_dimension === 'length' ? 'length_max' : 'beam_max'
    if (form.value.tier_min !== '') metadata[minKey] = parseFloat(form.value.tier_min)
    if (form.value.tier_max !== '') metadata[maxKey] = parseFloat(form.value.tier_max)
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
    pricing_kind: form.value.pricing_kind,
    tier_dimension: form.value.pricing_kind === 'tiered' ? form.value.tier_dimension : null,
    show_in_batch: form.value.show_in_batch,
    show_in_single: form.value.show_in_single,
    requires_boat_selection: form.value.requires_boat_selection,
    audience: form.value.audience,
  }
}

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const { mutate: saveItem, isPending: isSaving } = useMutation({
  mutationFn: async () => {
    const payload = buildPayload()
    if (form.value.id) {
      return unwrap(await client.PUT('/api/v1/admin/pricing/{itemID}', {
        params: { path: { itemID: form.value.id } },
        body: payload as any,
      }))
    }
    return unwrap(await client.POST('/api/v1/admin/pricing', {
      body: payload as any,
    }))
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
  mutationFn: async (id: string) =>
    unwrap(await client.DELETE('/api/v1/admin/pricing/{itemID}', { params: { path: { itemID: id } } })),
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

      <!-- Pricing kind + tier range -->
      <div class="rounded-md border border-gray-100 bg-gray-50 p-4">
        <p class="mb-2 text-sm font-medium text-gray-700">{{ t('admin.pricing.pricingKind') }}</p>
        <div class="flex gap-4 text-sm">
          <label class="flex items-center gap-1.5">
            <input v-model="form.pricing_kind" type="radio" value="flat" />
            {{ t('admin.pricing.kindFlat') }}
          </label>
          <label class="flex items-center gap-1.5">
            <input v-model="form.pricing_kind" type="radio" value="tiered" />
            {{ t('admin.pricing.kindTiered') }}
          </label>
        </div>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.pricing.kindHint') }}</p>

        <div v-if="form.pricing_kind === 'tiered'" class="mt-3 space-y-3">
          <div>
            <p class="mb-1.5 text-xs font-medium text-gray-600">
              {{ t('admin.pricing.tierDimension') }}
            </p>
            <div class="flex gap-4 text-sm">
              <label class="flex items-center gap-1.5">
                <input v-model="form.tier_dimension" type="radio" value="beam" />
                {{ t('admin.pricing.dimensionBeam') }}
              </label>
              <label class="flex items-center gap-1.5">
                <input v-model="form.tier_dimension" type="radio" value="length" />
                {{ t('admin.pricing.dimensionLength') }}
              </label>
            </div>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="block text-xs text-gray-500">{{ t('admin.pricing.tierMin') }}</label>
              <input
                v-model="form.tier_min"
                type="number"
                step="0.1"
                min="0"
                placeholder="0"
                class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
              />
            </div>
            <div>
              <label class="block text-xs text-gray-500">{{ t('admin.pricing.tierMax') }}</label>
              <input
                v-model="form.tier_max"
                type="number"
                step="0.1"
                min="0"
                placeholder="99"
                class="mt-1 block w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm"
              />
            </div>
          </div>
        </div>
      </div>

      <!-- Applicability -->
      <div class="rounded-md border border-gray-100 bg-gray-50 p-4 space-y-2">
        <p class="text-sm font-medium text-gray-700">{{ t('admin.pricing.applicability') }}</p>
        <div class="flex flex-wrap gap-x-6 gap-y-2 text-sm text-gray-700">
          <label class="flex items-center gap-2">
            <input v-model="form.show_in_single" type="checkbox" class="rounded border-gray-300" />
            {{ t('admin.pricing.showInSingle') }}
          </label>
          <label class="flex items-center gap-2">
            <input v-model="form.show_in_batch" type="checkbox" class="rounded border-gray-300" />
            {{ t('admin.pricing.showInBatch') }}
          </label>
          <label class="flex items-center gap-2">
            <input
              v-model="form.requires_boat_selection"
              type="checkbox"
              class="rounded border-gray-300"
            />
            {{ t('admin.pricing.requiresBoatSelection') }}
          </label>
        </div>
        <p class="text-xs text-gray-500">{{ t('admin.pricing.applicabilityHint') }}</p>
      </div>

      <!-- Audience -->
      <div class="rounded-md border border-gray-100 bg-gray-50 p-4 space-y-2">
        <p class="text-sm font-medium text-gray-700">{{ t('admin.pricing.audience') }}</p>
        <div class="flex flex-wrap gap-4 text-sm">
          <label v-for="a in audiences" :key="a.value" class="flex items-center gap-1.5">
            <input v-model="form.audience" type="radio" :value="a.value" />
            {{ a.label }}
          </label>
        </div>
        <p class="text-xs text-gray-500">{{ t('admin.pricing.audienceHint') }}</p>
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
              <div
                v-if="meta(item).beam_min || meta(item).beam_max"
                class="text-xs text-blue-600"
              >
                {{ t('admin.pricing.beam') }}: {{ meta(item).beam_min || '0' }}–{{ meta(item).beam_max || '∞' }} m
              </div>
              <div
                v-if="meta(item).length_min || meta(item).length_max"
                class="text-xs text-blue-600"
              >
                {{ t('admin.pricing.length') }}: {{ meta(item).length_min || '0' }}–{{ meta(item).length_max || '∞' }} m
              </div>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <span class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700">
                {{ categoryLabel(item.category) }}
              </span>
              <span
                v-if="(item as any).audience && (item as any).audience !== 'all'"
                class="ml-1 rounded-full bg-blue-50 px-2 py-0.5 text-[10px] font-medium text-blue-700"
              >
                {{
                  (item as any).audience === 'member'
                    ? t('admin.pricing.audienceMember')
                    : t('admin.pricing.audienceNonMember')
                }}
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
