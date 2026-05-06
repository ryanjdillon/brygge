<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { X, Plus, Trash2, ListPlus } from 'lucide-vue-next'
import { useAccountsList, useFiscalPeriods } from '@/composables/useAccounting'
import MemberSearch, { type MemberHit } from '@/components/members/MemberSearch.vue'
import LineItemPicker from '@/components/admin/LineItemPicker.vue'

defineProps<{
  open: boolean
}>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created', invoiceId: string): void
}>()

const { t } = useI18n()

const { data: accounts } = useAccountsList()
const revenueAccounts = computed(() =>
  (accounts.value ?? []).filter((a) => a.is_active && (a.account_type === 'revenue' || a.account_type === 'liability')),
)

const { data: periods } = useFiscalPeriods()
const openPeriods = computed(() =>
  (periods.value ?? []).filter((p) => p.status === 'open').sort((a, b) => b.year - a.year),
)
const fiscalPeriodId = ref('')
watch(openPeriods, (list) => {
  if (!fiscalPeriodId.value && list.length > 0) {
    fiscalPeriodId.value = list[0].id
  }
}, { immediate: true })

interface PriceItem {
  id: string
  category: string
  name: string
  amount: number
  unit: string
  is_active: boolean
  pricing_kind?: 'flat' | 'tiered'
  tier_dimension?: 'beam' | 'length' | null
  show_in_single?: boolean
  requires_boat_selection?: boolean
}

interface Boat {
  id: string
  name: string
  manufacturer?: string
  model?: string
  beam_m?: number
  length_m?: number
}

// Each line tracks its origin so the submit step knows whether to send
// it as a custom line (account_id required), a flat price_item_id
// line, or a tier_category line that the server resolves from boat_id.
interface LineDraft {
  kind: 'custom' | 'flat' | 'tier'
  description: string
  sub_description: string
  quantity: number
  unit_price: number
  account_id: string
  price_item_id?: string
  tier_category?: string
  boat_id: string
  requires_boat_selection: boolean
}

const member = ref<MemberHit | null>(null)
const dueDate = ref(defaultDueDate())
const lines = ref<LineDraft[]>([emptyCustomLine()])
const submitting = ref(false)
const error = ref<string | null>(null)

const showPicker = ref(false)
const pickerFlatIds = ref<string[]>([])
const pickerTierCategories = ref<string[]>([])
const knownItems = ref<PriceItem[]>([])
const boats = ref<Boat[]>([])

watch(member, async (m) => {
  boats.value = []
  if (!m) return
  try {
    const res = await fetch(`/api/v1/admin/users/${m.id}/boats`, { credentials: 'include' })
    if (res.ok) {
      const body = await res.json()
      boats.value = (body.boats ?? []) as Boat[]
    }
  } catch {
    // boats list is best-effort; absence just disables boat-tied lines
  }
})

function defaultDueDate(): string {
  const d = new Date()
  d.setDate(d.getDate() + 21)
  return d.toISOString().slice(0, 10)
}

function emptyCustomLine(): LineDraft {
  return {
    kind: 'custom',
    description: '',
    sub_description: '',
    quantity: 1,
    unit_price: 0,
    account_id: '',
    boat_id: '',
    requires_boat_selection: false,
  }
}

function addCustomLine() {
  const last = lines.value[lines.value.length - 1]
  lines.value.push({
    ...emptyCustomLine(),
    description: last?.kind === 'custom' ? last.description : '',
    unit_price: last?.kind === 'custom' ? last.unit_price : 0,
    account_id: last?.kind === 'custom' ? last.account_id : '',
  })
}

function removeLine(i: number) {
  if (lines.value.length === 1) {
    lines.value = [emptyCustomLine()]
    return
  }
  lines.value.splice(i, 1)
}

function applyPickerSelection() {
  // Convert picker selection into draft lines, then drop the trailing
  // empty custom line if the only line was the placeholder.
  const newLines: LineDraft[] = []
  for (const id of pickerFlatIds.value) {
    const item = knownItems.value.find((i) => i.id === id)
    if (!item) continue
    newLines.push({
      kind: 'flat',
      description: item.name,
      sub_description: '',
      quantity: 1,
      unit_price: item.amount,
      account_id: '',
      price_item_id: item.id,
      boat_id: '',
      requires_boat_selection: item.requires_boat_selection === true,
    })
  }
  for (const cat of pickerTierCategories.value) {
    newLines.push({
      kind: 'tier',
      description: cat,
      sub_description: '',
      quantity: 1,
      unit_price: 0,
      account_id: '',
      tier_category: cat,
      boat_id: '',
      requires_boat_selection: true,
    })
  }
  if (newLines.length === 0) return
  // Replace placeholder if it's still empty.
  if (
    lines.value.length === 1 &&
    lines.value[0].kind === 'custom' &&
    !lines.value[0].description &&
    lines.value[0].unit_price === 0
  ) {
    lines.value = newLines
  } else {
    lines.value = [...lines.value, ...newLines]
  }
  pickerFlatIds.value = []
  pickerTierCategories.value = []
  showPicker.value = false
}

const total = computed(() =>
  lines.value.reduce((s, l) => {
    if (l.kind === 'tier') return s
    return s + Number(l.quantity || 0) * Number(l.unit_price || 0)
  }, 0),
)

function formatNOK(n: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(n)
}

function reset() {
  member.value = null
  dueDate.value = defaultDueDate()
  lines.value = [emptyCustomLine()]
  pickerFlatIds.value = []
  pickerTierCategories.value = []
  showPicker.value = false
  error.value = null
}

async function submit() {
  error.value = null
  if (!member.value) {
    error.value = t('admin.singleFaktura.memberRequired')
    return
  }
  for (const l of lines.value) {
    if (!l.description.trim() || l.quantity < 1) {
      error.value = t('admin.singleFaktura.linesInvalid')
      return
    }
    if (l.kind === 'custom') {
      if (l.unit_price <= 0) {
        error.value = t('admin.singleFaktura.linesInvalid')
        return
      }
      if (!l.account_id) {
        error.value = t('admin.singleFaktura.accountRequired')
        return
      }
    }
    if (l.kind === 'tier' && !l.boat_id) {
      error.value = t('admin.singleFaktura.boatRequired')
      return
    }
    if (l.kind === 'flat' && l.requires_boat_selection && !l.boat_id) {
      error.value = t('admin.singleFaktura.boatRequired')
      return
    }
  }

  submitting.value = true
  try {
    const res = await fetch('/api/v1/admin/financials/invoices/full', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        user_id: member.value.id,
        due_date: dueDate.value,
        fiscal_period_id: fiscalPeriodId.value || undefined,
        lines: lines.value.map((l) => ({
          description: l.description.trim(),
          sub_description: l.sub_description.trim() || undefined,
          quantity: Number(l.quantity),
          unit_price: l.kind === 'tier' ? 0 : Number(l.unit_price),
          account_id: l.kind === 'custom' ? l.account_id : undefined,
          price_item_id: l.kind === 'flat' ? l.price_item_id : undefined,
          tier_category: l.kind === 'tier' ? l.tier_category : undefined,
          boat_id: l.boat_id || undefined,
        })),
      }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      error.value = `${res.status} ${txt}`
      return
    }
    const body = await res.json().catch(() => ({}))
    emit('created', body.id ?? '')
    reset()
    emit('close')
  } finally {
    submitting.value = false
  }
}

function boatLabel(b: Boat): string {
  const make = [b.manufacturer, b.model].filter(Boolean).join(' ')
  const dims: string[] = []
  if (b.length_m) dims.push(`${b.length_m}m`)
  if (b.beam_m) dims.push(`${b.beam_m}m b`)
  return [b.name, make].filter(Boolean).join(' — ') + (dims.length ? ` (${dims.join(', ')})` : '')
}
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    @keydown.esc="emit('close')"
  >
    <div class="w-full max-w-2xl rounded-lg bg-white p-5 shadow-xl">
      <div class="flex items-center justify-between border-b border-gray-100 pb-3">
        <h2 class="text-lg font-semibold text-gray-900">{{ t('admin.singleFaktura.title') }}</h2>
        <button type="button" class="text-gray-400 hover:text-gray-600" @click="emit('close')">
          <X class="h-5 w-5" />
        </button>
      </div>

      <form class="mt-4 space-y-4" @submit.prevent="submit">
        <div>
          <label class="block text-xs font-medium text-gray-700">{{ t('admin.singleFaktura.member') }}</label>
          <MemberSearch v-model="member" :placeholder="t('admin.singleFaktura.memberPlaceholder')" class="mt-1" />
        </div>

        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.singleFaktura.fiscalPeriod') }}</label>
            <select v-model="fiscalPeriodId" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm">
              <option value="">{{ t('admin.singleFaktura.fiscalPeriodNone') }}</option>
              <option v-for="p in openPeriods" :key="p.id" :value="p.id">{{ p.year }}</option>
            </select>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.singleFaktura.dueDate') }}</label>
            <input
              v-model="dueDate"
              type="date"
              required
              class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
            />
          </div>
        </div>

        <div>
          <div class="flex items-center justify-between">
            <label class="text-xs font-medium text-gray-700">{{ t('admin.singleFaktura.lines') }}</label>
            <div class="flex gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-2 py-1 text-xs hover:bg-gray-50"
                @click="showPicker = !showPicker"
              >
                <ListPlus class="h-3.5 w-3.5" /> {{ t('admin.singleFaktura.addFromPriceList') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-2 py-1 text-xs hover:bg-gray-50"
                @click="addCustomLine"
              >
                <Plus class="h-3.5 w-3.5" /> {{ t('admin.singleFaktura.addCustomLine') }}
              </button>
            </div>
          </div>

          <div v-if="showPicker" class="mt-2 rounded-md border border-blue-200 bg-blue-50/30 p-3">
            <LineItemPicker
              mode="single"
              :flat-ids="pickerFlatIds"
              :tier-categories="pickerTierCategories"
              @update:flat-ids="(v) => (pickerFlatIds = v)"
              @update:tier-categories="(v) => (pickerTierCategories = v)"
              @loaded="(items) => (knownItems = items)"
            />
            <div class="mt-2 flex justify-end gap-2">
              <button
                type="button"
                class="rounded-md px-2 py-1 text-xs text-gray-600 hover:bg-gray-100"
                @click="showPicker = false"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                type="button"
                :disabled="pickerFlatIds.length === 0 && pickerTierCategories.length === 0"
                class="rounded-md bg-blue-600 px-2 py-1 text-xs font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
                @click="applyPickerSelection"
              >
                {{ t('admin.singleFaktura.applyPickerSelection') }}
              </button>
            </div>
          </div>

          <div class="mt-2 space-y-3">
            <div v-for="(l, i) in lines" :key="i" class="rounded-md border border-gray-200 p-3">
              <div class="grid grid-cols-12 gap-2">
                <input
                  v-model="l.description"
                  type="text"
                  :placeholder="t('admin.singleFaktura.descriptionPlaceholder')"
                  :readonly="l.kind === 'tier'"
                  class="col-span-7 rounded-md border border-gray-300 px-2 py-1 text-sm"
                />
                <input
                  v-model.number="l.quantity"
                  type="number"
                  min="1"
                  class="col-span-2 rounded-md border border-gray-300 px-2 py-1 text-sm tabular-nums"
                  :title="t('admin.singleFaktura.quantity')"
                />
                <input
                  v-model.number="l.unit_price"
                  type="number"
                  min="0"
                  step="0.01"
                  :readonly="l.kind === 'tier'"
                  :placeholder="l.kind === 'tier' ? t('admin.singleFaktura.tierResolved') : ''"
                  class="col-span-2 rounded-md border border-gray-300 px-2 py-1 text-sm tabular-nums"
                  :title="t('admin.singleFaktura.unitPrice')"
                />
                <button
                  type="button"
                  class="col-span-1 text-red-500 hover:text-red-700"
                  :title="t('common.delete')"
                  @click="removeLine(i)"
                >
                  <Trash2 class="h-4 w-4" />
                </button>
              </div>
              <div class="mt-2 grid grid-cols-12 gap-2">
                <input
                  v-model="l.sub_description"
                  type="text"
                  :placeholder="t('admin.singleFaktura.subDescriptionPlaceholder')"
                  class="col-span-7 rounded-md border border-gray-200 px-2 py-1 text-xs text-gray-700"
                />
                <select
                  v-if="l.kind === 'custom'"
                  v-model="l.account_id"
                  required
                  class="col-span-5 rounded-md border border-gray-300 px-2 py-1 text-xs"
                >
                  <option value="">{{ t('admin.singleFaktura.accountPlaceholder') }}</option>
                  <option v-for="a in revenueAccounts" :key="a.id" :value="a.id">
                    {{ a.code }} — {{ a.name }}
                  </option>
                </select>
                <select
                  v-else-if="l.kind === 'tier' || l.requires_boat_selection"
                  v-model="l.boat_id"
                  required
                  class="col-span-5 rounded-md border border-gray-300 px-2 py-1 text-xs"
                >
                  <option value="">{{ t('admin.singleFaktura.boatPlaceholder') }}</option>
                  <option v-for="b in boats" :key="b.id" :value="b.id">
                    {{ boatLabel(b) }}
                  </option>
                </select>
                <span v-else class="col-span-5 self-center text-xs text-gray-400">
                  {{ t('admin.singleFaktura.fromPriceList') }}
                </span>
              </div>
              <p v-if="l.kind === 'tier' && !boats.length && member" class="mt-1 text-xs text-amber-600">
                {{ t('admin.singleFaktura.noBoatsForMember') }}
              </p>
            </div>
          </div>
          <p class="mt-2 text-right text-sm font-semibold text-gray-700">
            {{ t('admin.singleFaktura.total') }}: {{ formatNOK(total) }}
            <span v-if="lines.some((l) => l.kind === 'tier')" class="ml-1 text-xs font-normal text-gray-500">
              ({{ t('admin.singleFaktura.tierExcluded') }})
            </span>
          </p>
        </div>

        <p v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>

        <div class="flex justify-end gap-2 border-t border-gray-100 pt-3">
          <button
            type="button"
            class="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50"
            @click="emit('close')"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            :disabled="submitting"
            class="rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {{ submitting ? t('common.loading') : t('admin.singleFaktura.create') }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>
