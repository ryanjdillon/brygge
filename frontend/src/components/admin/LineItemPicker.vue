<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import MultiSelectList from '@/components/ui/MultiSelectList.vue'

// LineItemPicker is the shared two-pane picker used by both the batch
// faktura flow and the single faktura flow. It loads /admin/pricing
// once per mount, splits items into "flat" and "tiered" panes, and
// filters by `show_in_batch` / `show_in_single` per `mode`.
//
// State is pushed back via v-model:flatIds and v-model:tierCategories
// so each consumer can wire the selection into its own submit shape:
// the batch modal sends them as `price_item_ids` + `beam_categories`;
// a single-faktura form can fan them out into individual line items.
//
// Per-item boat selection (requires_boat_selection) lives outside this
// picker — consumers render their own per-line boat combobox after a
// user has picked an item.

interface PriceItem {
  id: string
  category: string
  name: string
  amount: number
  unit: string
  is_active: boolean
  pricing_kind?: 'flat' | 'tiered'
  tier_dimension?: 'beam' | 'length' | null
  show_in_batch?: boolean
  show_in_single?: boolean
  requires_boat_selection?: boolean
  metadata?: {
    beam_min?: number
    beam_max?: number
    length_min?: number
    length_max?: number
  } | null
}

const props = defineProps<{
  mode: 'batch' | 'single'
  flatIds: string[]
  tierCategories: string[]
}>()

const emit = defineEmits<{
  (e: 'update:flatIds', v: string[]): void
  (e: 'update:tierCategories', v: string[]): void
  (e: 'loaded', items: PriceItem[]): void
}>()

const { t, n } = useI18n()

const items = ref<PriceItem[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

function visible(i: PriceItem): boolean {
  if (!i.is_active) return false
  if (props.mode === 'batch') return i.show_in_batch === true
  return i.show_in_single !== false
}

// Treat any category with at least one tiered item as a tier-category
// in its entirety — keeps a half-configured tiered item from leaking
// into the flat pane.
const tierCategories = computed(() => {
  const set = new Set<string>()
  for (const i of items.value) {
    if (visible(i) && i.pricing_kind === 'tiered') set.add(i.category)
  }
  return set
})

const flatOptions = computed(() =>
  items.value
    .filter((i) => visible(i) && !tierCategories.value.has(i.category))
    .map((i) => ({
      value: i.id,
      label: i.name,
      hint: i.category,
      meta: `${formatNok(i.amount)} / ${i.unit}`,
    })),
)

const tierCategoryOptions = computed(() => {
  const byCat = new Map<string, PriceItem[]>()
  for (const i of items.value) {
    if (!visible(i)) continue
    if (i.pricing_kind !== 'tiered') continue
    const list = byCat.get(i.category) ?? []
    list.push(i)
    byCat.set(i.category, list)
  }
  return [...byCat.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([cat, tiers]) => {
      const dim = (tiers.find((x) => x.tier_dimension)?.tier_dimension ?? 'beam') as 'beam' | 'length'
      const minKey = dim === 'length' ? 'length_min' : 'beam_min'
      const maxKey = dim === 'length' ? 'length_max' : 'beam_max'
      const sorted = tiers
        .slice()
        .sort((a, b) => (a.metadata?.[minKey] ?? 0) - (b.metadata?.[minKey] ?? 0))
      const ranges = sorted
        .map(
          (tier) =>
            `${tier.metadata?.[minKey] ?? '?'}–${tier.metadata?.[maxKey] ?? '?'}m: ${formatNok(tier.amount)}`,
        )
        .join(' · ')
      return {
        value: cat,
        label: cat,
        hint: `[${dim === 'length' ? t('lineItemPicker.byLength') : t('lineItemPicker.byBeam')}] ${ranges}`,
        meta: t('lineItemPicker.tiersCount', { n: tiers.length }),
      }
    })
})

function formatNok(amount: number): string {
  return `${n(amount)} kr`
}

const flatModel = computed({
  get: () => props.flatIds,
  set: (v: string[]) => emit('update:flatIds', v),
})
const tierModel = computed({
  get: () => props.tierCategories,
  set: (v: string[]) => emit('update:tierCategories', v),
})

onMounted(async () => {
  try {
    const res = await fetch('/api/v1/admin/pricing', { credentials: 'include' })
    if (!res.ok) throw new Error(`price items: ${res.status}`)
    const body = await res.json()
    items.value = (body.items ?? body ?? []) as PriceItem[]
    emit('loaded', items.value)
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
})

// If a previously-selected option becomes invisible (e.g. mode flipped
// to 'single' and an item was batch-only), drop it so we don't submit
// a stale id.
watch(
  [() => props.mode, items],
  () => {
    if (loading.value) return
    const validFlat = new Set(flatOptions.value.map((o) => o.value))
    const validTier = new Set(tierCategoryOptions.value.map((o) => o.value))
    const nextFlat = props.flatIds.filter((id) => validFlat.has(id))
    const nextTier = props.tierCategories.filter((c) => validTier.has(c))
    if (nextFlat.length !== props.flatIds.length) emit('update:flatIds', nextFlat)
    if (nextTier.length !== props.tierCategories.length) emit('update:tierCategories', nextTier)
  },
  { immediate: false },
)
</script>

<template>
  <div>
    <p v-if="loading" class="text-xs text-gray-500">{{ t('common.loading') }}…</p>
    <p v-else-if="error" class="rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ error }}</p>
    <div v-else class="grid gap-3 md:grid-cols-2">
      <section class="flex flex-col">
        <header class="mb-1.5 flex items-baseline justify-between">
          <h3 class="text-sm font-semibold text-gray-900">
            {{ t('lineItemPicker.flatTitle') }}
          </h3>
          <span class="text-xs text-gray-500">
            {{ flatModel.length }} / {{ flatOptions.length }}
          </span>
        </header>
        <p class="mb-2 text-xs text-gray-500">{{ t('lineItemPicker.flatSubtitle') }}</p>
        <MultiSelectList
          v-model="flatModel"
          :options="flatOptions"
          :placeholder="t('lineItemPicker.flatSearch')"
          :empty-text="t('lineItemPicker.flatEmpty')"
        />
      </section>

      <section class="flex flex-col">
        <header class="mb-1.5 flex items-baseline justify-between">
          <h3 class="text-sm font-semibold text-gray-900">
            {{ t('lineItemPicker.tierTitle') }}
          </h3>
          <span class="text-xs text-gray-500">
            {{ tierModel.length }} / {{ tierCategoryOptions.length }}
          </span>
        </header>
        <p class="mb-2 text-xs text-gray-500">{{ t('lineItemPicker.tierSubtitle') }}</p>
        <MultiSelectList
          v-model="tierModel"
          :options="tierCategoryOptions"
          :placeholder="t('lineItemPicker.tierSearch')"
          :empty-text="t('lineItemPicker.tierEmpty')"
        />
      </section>
    </div>
  </div>
</template>
