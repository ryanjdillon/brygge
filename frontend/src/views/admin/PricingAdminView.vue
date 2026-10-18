<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { ApiError } from '@/lib/errors'
import { useConfirm } from '@/stores/confirm'
import { useCan } from '@/composables/useCan'
import { Plus, Pencil, Trash2, X, ChevronDown, ChevronRight } from 'lucide-vue-next'
import type { PriceItem } from '@/composables/usePricing'
import { PRICE_CATEGORIES, PRICE_CATEGORY_KEYS, PRICE_UNITS, PRICE_UNIT_KEYS } from '@/lib/priceCategories'
import Input from '@/components/ui/form/Input.vue'
import NumberInput from '@/components/ui/form/NumberInput.vue'
import Select from '@/components/ui/form/Select.vue'
import Checkbox from '@/components/ui/form/Checkbox.vue'
import RadioGroup from '@/components/ui/form/RadioGroup.vue'

const { t } = useI18n()
// Prices drive what members are billed: board members read them, the
// money role edits them (BRY-372).
const { can } = useCan()
const canEdit = can('money')
const client = useApiClient()
const queryClient = useQueryClient()
const askConfirm = useConfirm()

function meta(item: PriceItem): Record<string, string> {
  return (item.metadata ?? {}) as Record<string, string>
}

const { data: response, isLoading } = useQuery({
  queryKey: ['admin', 'pricing'],
  queryFn: async () => unwrap(await client.GET('/api/v1/admin/pricing')),
})

// Archived (inactive) items stay listed so they can be reactivated, but
// below the active ones in a section that starts collapsed.
const showArchived = ref(false)
const activeItems = computed(() => (response.value?.items ?? []).filter((i) => i.is_active))
const archivedItems = computed(() => (response.value?.items ?? []).filter((i) => !i.is_active))

type TableRow =
  | { kind: 'item'; key: string; item: PriceItem }
  | { kind: 'archivedToggle'; key: string }

const tableRows = computed<TableRow[]>(() => {
  const rows: TableRow[] = activeItems.value.map((item) => ({ kind: 'item', key: item.id, item }))
  if (archivedItems.value.length) {
    rows.push({ kind: 'archivedToggle', key: 'archived-toggle' })
    if (showArchived.value) {
      rows.push(...archivedItems.value.map((item) => ({ kind: 'item' as const, key: item.id, item })))
    }
  }
  return rows
})

const categories = computed(() =>
  PRICE_CATEGORIES.map((value) => ({ value, label: t(PRICE_CATEGORY_KEYS[value]) })),
)

const units = computed(() => PRICE_UNITS.map((value) => ({ value, label: t(PRICE_UNIT_KEYS[value]) })))

const audiences = computed(() => [
  { value: 'all' as const, label: t('admin.pricing.audienceAll') },
  { value: 'member' as const, label: t('admin.pricing.audienceMember') },
  { value: 'non_member' as const, label: t('admin.pricing.audienceNonMember') },
])

interface FormData {
  id?: string
  category: string
  name: string
  description: string
  amount: number | null
  unit: string
  installments_allowed: boolean
  max_installments: number | null
  sort_order: number | null
  is_active: boolean
  season: string
  period_start: string
  period_end: string
  pricing_kind: 'flat' | 'tiered'
  tier_dimension: 'beam' | 'length'
  tier_min: number | null
  tier_max: number | null
  show_in_batch: boolean
  show_in_single: boolean
  requires_boat_selection: boolean
  audience: 'all' | 'member' | 'non_member'
}

const emptyForm: FormData = {
  category: 'service',
  name: '',
  description: '',
  amount: null,
  unit: 'once',
  installments_allowed: false,
  max_installments: 1,
  sort_order: 0,
  is_active: true,
  season: '',
  period_start: '',
  period_end: '',
  pricing_kind: 'flat',
  tier_dimension: 'beam',
  tier_min: null,
  tier_max: null,
  show_in_batch: false,
  show_in_single: true,
  requires_boat_selection: false,
  audience: 'all',
}

const seasonOptions = computed(() => [
  { value: '', label: t('admin.pricing.seasonNone') },
  { value: 'summer', label: t('admin.pricing.seasonSummer') },
  { value: 'winter', label: t('admin.pricing.seasonWinter') },
])

const pricingKindOptions = computed(() => [
  { value: 'flat' as const, label: t('admin.pricing.kindFlat') },
  { value: 'tiered' as const, label: t('admin.pricing.kindTiered') },
])

const tierDimensionOptions = computed(() => [
  { value: 'beam' as const, label: t('admin.pricing.dimensionBeam') },
  { value: 'length' as const, label: t('admin.pricing.dimensionLength') },
])

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
  form.value = formFromItem(item)
  showForm.value = true
}

function formFromItem(item: PriceItem): FormData {
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
  const tierMinRaw = dim === 'length' ? meta.length_min : meta.beam_min
  const tierMaxRaw = dim === 'length' ? meta.length_max : meta.beam_max
  const tierMin = tierMinRaw != null && tierMinRaw !== '' ? parseFloat(tierMinRaw) : null
  const tierMax = tierMaxRaw != null && tierMaxRaw !== '' ? parseFloat(tierMaxRaw) : null
  return {
    id: item.id,
    category: item.category,
    name: item.name,
    description: item.description,
    amount: item.amount,
    unit: item.unit,
    installments_allowed: item.installments_allowed,
    max_installments: item.max_installments,
    sort_order: item.sort_order,
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
    requires_boat_selection: it.requires_boat_selection ?? false,
    audience: it.audience ?? 'all',
  }
}

function buildPayload(f: FormData) {
  const metadata: Record<string, string | number> = {}
  if (f.season) metadata.season = f.season
  if (f.period_start) metadata.period_start = f.period_start
  if (f.period_end) metadata.period_end = f.period_end
  if (f.pricing_kind === 'tiered') {
    const minKey = f.tier_dimension === 'length' ? 'length_min' : 'beam_min'
    const maxKey = f.tier_dimension === 'length' ? 'length_max' : 'beam_max'
    if (f.tier_min != null) metadata[minKey] = f.tier_min
    if (f.tier_max != null) metadata[maxKey] = f.tier_max
  }

  return {
    category: f.category,
    name: f.name,
    description: f.description,
    amount: f.amount ?? 0,
    unit: f.unit,
    installments_allowed: f.installments_allowed,
    max_installments: f.max_installments ?? 1,
    sort_order: f.sort_order ?? 0,
    is_active: f.is_active,
    metadata,
    pricing_kind: f.pricing_kind,
    tier_dimension: f.pricing_kind === 'tiered' ? f.tier_dimension : null,
    show_in_batch: f.show_in_batch,
    show_in_single: f.show_in_single,
    requires_boat_selection: f.requires_boat_selection,
    audience: f.audience,
  }
}

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const { mutate: saveItem, isPending: isSaving } = useMutation({
  mutationFn: async () => {
    const payload = buildPayload(form.value)
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

function invalidatePricing() {
  queryClient.invalidateQueries({ queryKey: ['admin', 'pricing'] })
  queryClient.invalidateQueries({ queryKey: ['pricing'] })
}

// The server refuses to hard-delete an item that invoices, orders or QR
// targets reference (deleting would strip their revenue mapping), so the
// fallback is to archive it instead.
async function confirmDelete(item: PriceItem) {
  const ok = await askConfirm({
    title: t('admin.pricing.deleteConfirmTitle'),
    body: t('admin.pricing.deleteConfirm'),
    confirmLabel: t('admin.pricing.delete'),
    tone: 'danger',
  })
  if (!ok) return
  try {
    unwrap(await client.DELETE('/api/v1/admin/pricing/{itemID}', { params: { path: { itemID: item.id } } }))
    invalidatePricing()
    showToast('success', t('admin.pricing.priceDeleted'))
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.code === 'price_item_in_use') {
      await offerArchive(item)
      return
    }
    showToast('error', t('admin.pricing.deleteError'))
  }
}

async function offerArchive(item: PriceItem) {
  const ok = await askConfirm({
    title: t('admin.pricing.inUseTitle'),
    body: t('admin.pricing.inUseBody', { name: item.name }),
    confirmLabel: t('admin.pricing.archive'),
    tone: 'warning',
  })
  if (!ok) return
  try {
    unwrap(await client.PUT('/api/v1/admin/pricing/{itemID}', {
      params: { path: { itemID: item.id } },
      body: buildPayload({ ...formFromItem(item), is_active: false }) as any,
    }))
    invalidatePricing()
    showToast('success', t('admin.pricing.priceArchived'))
  } catch {
    showToast('error', t('admin.pricing.archiveError'))
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
        v-if="canEdit && !showForm"
        class="flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-brand-700"
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
          <Select v-model="form.category" :options="categories" class="mt-1" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.name') }}</label>
          <Input v-model="form.name" class="mt-1" />
        </div>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.description') }}</label>
        <Input v-model="form.description" class="mt-1" />
      </div>

      <div class="grid grid-cols-3 gap-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.amount') }}</label>
          <NumberInput v-model="form.amount" :step="1" :min="0" class="mt-1" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.unit') }}</label>
          <Select v-model="form.unit" :options="units" class="mt-1" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.pricing.sortOrder') }}</label>
          <NumberInput v-model="form.sort_order" class="mt-1" />
        </div>
      </div>

      <!-- Installments -->
      <div class="flex items-center gap-4">
        <Checkbox v-model="form.installments_allowed">
          {{ t('admin.pricing.installments') }}
        </Checkbox>
        <div v-if="form.installments_allowed" class="flex items-center gap-2">
          <label class="text-sm text-gray-700">{{ t('admin.pricing.maxInstallments') }}</label>
          <NumberInput v-model="form.max_installments" :min="2" :max="24" class="w-20" />
        </div>
      </div>

      <!-- Season metadata (for seasonal items) -->
      <div v-if="form.unit === 'season'" class="rounded-md border border-gray-100 bg-gray-50 p-4">
        <p class="mb-3 text-sm font-medium text-gray-700">{{ t('admin.pricing.seasonPeriod') }}</p>
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.pricing.season') }}</label>
            <Select v-model="form.season" :options="seasonOptions" class="mt-1" />
          </div>
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.pricing.dateFrom') }}</label>
            <Input v-model="form.period_start" placeholder="05-01" class="mt-1" />
          </div>
          <div>
            <label class="block text-xs text-gray-500">{{ t('admin.pricing.dateTo') }}</label>
            <Input v-model="form.period_end" placeholder="09-30" class="mt-1" />
          </div>
        </div>
      </div>

      <!-- Pricing kind + tier range -->
      <div class="rounded-md border border-gray-100 bg-gray-50 p-4">
        <p class="mb-2 text-sm font-medium text-gray-700">{{ t('admin.pricing.pricingKind') }}</p>
        <RadioGroup v-model="form.pricing_kind" :options="pricingKindOptions" name="pricing_kind" />
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.pricing.kindHint') }}</p>

        <div v-if="form.pricing_kind === 'tiered'" class="mt-3 space-y-3">
          <div>
            <p class="mb-1.5 text-xs font-medium text-gray-600">
              {{ t('admin.pricing.tierDimension') }}
            </p>
            <RadioGroup v-model="form.tier_dimension" :options="tierDimensionOptions" name="tier_dimension" />
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="block text-xs text-gray-500">{{ t('admin.pricing.tierMin') }}</label>
              <NumberInput v-model="form.tier_min" :step="0.1" :min="0" placeholder="0" class="mt-1" />
            </div>
            <div>
              <label class="block text-xs text-gray-500">{{ t('admin.pricing.tierMax') }}</label>
              <NumberInput v-model="form.tier_max" :step="0.1" :min="0" placeholder="99" class="mt-1" />
            </div>
          </div>
        </div>
      </div>

      <!-- Applicability -->
      <div class="rounded-md border border-gray-100 bg-gray-50 p-4 space-y-2">
        <p class="text-sm font-medium text-gray-700">{{ t('admin.pricing.applicability') }}</p>
        <div class="flex flex-wrap gap-x-6 gap-y-2 text-sm text-gray-700">
          <Checkbox v-model="form.show_in_single">
            {{ t('admin.pricing.showInSingle') }}
          </Checkbox>
          <Checkbox v-model="form.show_in_batch">
            {{ t('admin.pricing.showInBatch') }}
          </Checkbox>
          <Checkbox v-model="form.requires_boat_selection">
            {{ t('admin.pricing.requiresBoatSelection') }}
          </Checkbox>
        </div>
        <p class="text-xs text-gray-500">{{ t('admin.pricing.applicabilityHint') }}</p>
      </div>

      <!-- Audience -->
      <div class="rounded-md border border-gray-100 bg-gray-50 p-4 space-y-2">
        <p class="text-sm font-medium text-gray-700">{{ t('admin.pricing.audience') }}</p>
        <RadioGroup v-model="form.audience" :options="audiences" name="audience" />
        <p class="text-xs text-gray-500">{{ t('admin.pricing.audienceHint') }}</p>
      </div>

      <div class="flex items-center gap-4">
        <Checkbox v-model="form.is_active">
          {{ t('admin.pricing.activeCheckbox') }}
        </Checkbox>
      </div>

      <div class="flex gap-3 pt-2">
        <button
          type="submit"
          :disabled="isSaving"
          class="rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-brand-700 disabled:opacity-50"
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
          <template v-for="row in tableRows" :key="row.key">
          <tr v-if="row.kind === 'archivedToggle'" class="bg-gray-50">
            <td colspan="6" class="px-4 py-2">
              <button
                type="button"
                class="flex items-center gap-1 text-sm font-medium text-gray-600 hover:text-gray-900"
                :aria-expanded="showArchived"
                @click="showArchived = !showArchived"
              >
                <component :is="showArchived ? ChevronDown : ChevronRight" class="h-4 w-4" />
                {{ t('admin.pricing.archivedSection', { count: archivedItems.length }) }}
              </button>
            </td>
          </tr>
          <tr v-else-if="row.kind === 'item'" :class="{ 'opacity-70': !row.item.is_active }">
            <td class="px-4 py-3 text-sm">
              <div class="font-medium text-gray-900">{{ row.item.name }}</div>
              <div v-if="row.item.description" class="text-xs text-gray-500">{{ row.item.description }}</div>
              <div
                v-if="meta(row.item).beam_min || meta(row.item).beam_max"
                class="text-xs text-brand-600"
              >
                {{ t('admin.pricing.beam') }}: {{ meta(row.item).beam_min || '0' }}–{{ meta(row.item).beam_max || '∞' }} m
              </div>
              <div
                v-if="meta(row.item).length_min || meta(row.item).length_max"
                class="text-xs text-brand-600"
              >
                {{ t('admin.pricing.length') }}: {{ meta(row.item).length_min || '0' }}–{{ meta(row.item).length_max || '∞' }} m
              </div>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <span class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700">
                {{ categoryLabel(row.item.category) }}
              </span>
              <span
                v-if="(row.item as any).audience && (row.item as any).audience !== 'all'"
                class="ml-1 rounded-full bg-brand-50 px-2 py-0.5 text-[10px] font-medium text-brand-700"
              >
                {{
                  (row.item as any).audience === 'member'
                    ? t('admin.pricing.audienceMember')
                    : t('admin.pricing.audienceNonMember')
                }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm font-medium text-gray-900">
              {{ row.item.amount.toLocaleString('nb-NO') }} kr
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
              {{ unitLabel(row.item.unit) }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-center text-sm">
              <span
                :class="[
                  'rounded-full px-2 py-0.5 text-xs font-medium',
                  row.item.is_active ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-500',
                ]"
              >
                {{ row.item.is_active ? t('admin.pricing.active') : t('admin.pricing.inactive') }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm">
              <template v-if="canEdit">
              <button class="mr-2 text-gray-500 hover:text-brand-600" @click="openEdit(row.item)">
                <Pencil class="h-4 w-4" />
              </button>
              <button class="text-gray-500 hover:text-red-600" @click="confirmDelete(row.item)">
                <Trash2 class="h-4 w-4" />
              </button>
              </template>
            </td>
          </tr>
          </template>
        </tbody>
      </table>
    </div>
  </div>
</template>
