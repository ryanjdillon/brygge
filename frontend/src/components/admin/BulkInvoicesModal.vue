<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { X } from 'lucide-vue-next'
import LineItemPicker from '@/components/admin/LineItemPicker.vue'

interface PriceItem {
  id: string
  category: string
  name: string
  amount: number
  unit: string
  is_active: boolean
}

interface FiscalPeriod {
  id: string
  year: number
  status: string
}

interface BulkResult {
  created: {
    user_id: string
    invoice_id: string
    invoice_number: number
    amount: number
    line_count: number
    dropped_lines?: string[]
  }[]
  skipped: { user_id: string; reason: string }[]
}

const props = defineProps<{
  userIds: string[]
  userNamesById: Record<string, string>
}>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'completed'): void
}>()

const { t, n } = useI18n()

const periods = ref<FiscalPeriod[]>([])
const items = ref<PriceItem[]>([])
const loading = ref(true)
const submitting = ref(false)
const error = ref<string | null>(null)
const result = ref<BulkResult | null>(null)

const fiscalPeriodId = ref('')
const selectedFlatIds = ref<string[]>([])
const selectedBeamCategories = ref<string[]>([])
const dueDate = ref(defaultDueDate())

function defaultDueDate(): string {
  const d = new Date()
  d.setDate(d.getDate() + 21)
  return d.toISOString().slice(0, 10)
}

function formatNok(amount: number): string {
  return `${n(amount)} kr`
}

function onPickerLoaded(loaded: PriceItem[]) {
  items.value = loaded
}

const selectionTotal = computed(() => {
  let sum = 0
  for (const id of selectedFlatIds.value) {
    const i = items.value.find((x) => x.id === id)
    if (i) sum += i.amount
  }
  return sum
})

const hasSelection = computed(
  () => selectedFlatIds.value.length > 0 || selectedBeamCategories.value.length > 0,
)

onMounted(async () => {
  try {
    const periodsRes = await fetch('/api/v1/admin/accounting/periods', { credentials: 'include' })
    if (!periodsRes.ok) throw new Error(`fiscal periods: ${periodsRes.status}`)
    const periodsBody = await periodsRes.json()
    periods.value = (periodsBody.periods ?? periodsBody ?? []) as FiscalPeriod[]
    const open = periods.value
      .filter((p) => p.status === 'open')
      .sort((a, b) => b.year - a.year)
    const fallback = periods.value.slice().sort((a, b) => b.year - a.year)
    const pick = open[0] ?? fallback[0]
    if (pick) fiscalPeriodId.value = pick.id
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
})

async function submit() {
  if (!fiscalPeriodId.value) {
    error.value = t('bulkInvoices.errors.fiscalPeriodRequired')
    return
  }
  if (!hasSelection.value) {
    error.value = t('bulkInvoices.errors.noSelection')
    return
  }
  submitting.value = true
  error.value = null
  result.value = null
  try {
    const body = {
      user_ids: props.userIds,
      fiscal_period_id: fiscalPeriodId.value,
      price_item_ids: selectedFlatIds.value,
      beam_categories: selectedBeamCategories.value,
      due_date: dueDate.value,
    }
    const res = await fetch('/api/v1/admin/financials/invoices/bulk', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${res.statusText} ${txt}`)
    }
    result.value = (await res.json()) as BulkResult
    emit('completed')
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    submitting.value = false
  }
}

function nameFor(id: string): string {
  return props.userNamesById[id] ?? id.slice(0, 8)
}
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    v-backdrop-close="() => emit('close')"
    @keydown.esc="emit('close')"
  >
    <div class="w-full max-w-3xl rounded-lg bg-white p-5 shadow-xl">
      <div class="mb-3 flex items-center justify-between">
        <h2 class="text-lg font-semibold text-gray-900">
          {{ t('bulkInvoices.title', { n: userIds.length }) }}
        </h2>
        <button class="text-gray-400 hover:text-gray-600" @click="emit('close')">
          <X class="h-5 w-5" />
        </button>
      </div>

      <p v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}…</p>

      <template v-else-if="!result">
        <form class="space-y-4" @submit.prevent="submit">
          <div class="grid gap-3 sm:grid-cols-2">
            <div>
              <label class="block text-xs font-medium text-gray-700">
                {{ t('bulkInvoices.fiscalPeriod') }}
              </label>
              <select
                v-model="fiscalPeriodId"
                required
                class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
              >
                <option value="" disabled>{{ t('common.select') }}</option>
                <option v-for="p in periods" :key="p.id" :value="p.id">
                  {{ p.year }} ({{ p.status }})
                </option>
              </select>
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-700">
                {{ t('bulkInvoices.dueDate') }}
              </label>
              <input
                v-model="dueDate"
                type="date"
                required
                class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
              />
            </div>
          </div>

          <LineItemPicker
            mode="batch"
            :flat-ids="selectedFlatIds"
            :tier-categories="selectedBeamCategories"
            @update:flat-ids="(v) => (selectedFlatIds = v)"
            @update:tier-categories="(v) => (selectedBeamCategories = v)"
            @loaded="onPickerLoaded"
          />

          <div
            class="flex flex-wrap items-center justify-between gap-2 rounded-md bg-gray-50 px-3 py-2 text-xs text-gray-600"
          >
            <span>
              {{
                t('bulkInvoices.selectionSummary', {
                  flat: selectedFlatIds.length,
                  beam: selectedBeamCategories.length,
                })
              }}
            </span>
            <span v-if="selectedFlatIds.length" class="font-medium text-gray-800">
              {{ formatNok(selectionTotal) }} × {{ userIds.length }}
              <span v-if="selectedBeamCategories.length" class="text-gray-500">
                + {{ t('bulkInvoices.beamPaneTitle').toLowerCase() }}
              </span>
            </span>
          </div>

          <p v-if="error" class="rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ error }}</p>

          <div class="flex justify-end gap-2 pt-1">
            <button
              type="button"
              class="rounded-md px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-100"
              @click="emit('close')"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              :disabled="submitting || !hasSelection"
              class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {{ submitting ? t('common.loading') : t('bulkInvoices.submit') }}
            </button>
          </div>
        </form>
      </template>

      <template v-else>
        <div class="space-y-3">
          <p class="text-sm text-gray-700">
            {{
              t('bulkInvoices.resultSummary', {
                created: result.created.length,
                skipped: result.skipped.length,
              })
            }}
          </p>

          <div
            v-if="result.created.length"
            class="max-h-48 overflow-auto rounded-md border border-gray-200"
          >
            <table class="w-full text-xs">
              <thead class="bg-gray-50 text-left text-gray-500">
                <tr>
                  <th class="px-2 py-1">{{ t('bulkInvoices.member') }}</th>
                  <th class="px-2 py-1">#</th>
                  <th class="px-2 py-1">{{ t('bulkInvoices.lines') }}</th>
                  <th class="px-2 py-1">{{ t('bulkInvoices.amount') }}</th>
                </tr>
              </thead>
              <tbody>
                <template v-for="r in result.created" :key="r.invoice_id">
                  <tr>
                    <td class="px-2 py-1">{{ nameFor(r.user_id) }}</td>
                    <td class="px-2 py-1 font-mono">{{ r.invoice_number }}</td>
                    <td class="px-2 py-1">{{ r.line_count }}</td>
                    <td class="px-2 py-1">{{ formatNok(r.amount) }}</td>
                  </tr>
                  <tr v-if="r.dropped_lines && r.dropped_lines.length">
                    <td colspan="4" class="bg-amber-50 px-2 py-1 text-[11px] text-amber-800">
                      ↳ {{ r.dropped_lines.join('; ') }}
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>

          <div
            v-if="result.skipped.length"
            class="max-h-48 overflow-auto rounded-md border border-amber-200 bg-amber-50"
          >
            <table class="w-full text-xs">
              <thead class="bg-amber-100 text-left text-amber-800">
                <tr>
                  <th class="px-2 py-1">{{ t('bulkInvoices.member') }}</th>
                  <th class="px-2 py-1">{{ t('bulkInvoices.skipReason') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="s in result.skipped" :key="s.user_id">
                  <td class="px-2 py-1">{{ nameFor(s.user_id) }}</td>
                  <td class="px-2 py-1">{{ s.reason }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div class="flex justify-end pt-2">
            <button
              class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700"
              @click="emit('close')"
            >
              {{ t('common.close') }}
            </button>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
