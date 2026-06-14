<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Play, AlertTriangle, Copy, Check } from 'lucide-vue-next'
import { useFreshTotp } from '@/composables/useFreshTotp'

const { t } = useI18n()
const { totpAwareFetch } = useFreshTotp()

const sql = ref('')
const running = ref(false)
const error = ref<string | null>(null)
const result = ref<{
  columns: string[]
  rows: unknown[][]
  row_count: number
  elapsed_ms: number
  truncated: boolean
} | null>(null)
const copiedTsv = ref(false)

const examples = computed(() => [
  {
    label: t('admin.devQuery.examples.invoiceByNumber'),
    sql: 'SELECT id, invoice_number, kid_number, total_amount, sent_at, payment_id\nFROM invoices\nWHERE invoice_number = 123\nLIMIT 5',
  },
  {
    label: t('admin.devQuery.examples.kidMatch'),
    sql: 'SELECT bir.row_date, bir.amount, bir.kid_number, bir.journal_entry_id, i.invoice_number, i.payment_id\nFROM bank_import_rows bir\nLEFT JOIN invoices i ON i.kid_number = bir.kid_number\nWHERE bir.kid_number = \'00000000123\'\nORDER BY bir.row_date DESC',
  },
  {
    label: t('admin.devQuery.examples.unpaidWithBankRow'),
    sql: 'SELECT i.invoice_number, i.kid_number, i.total_amount, i.sent_at, bir.row_date AS bank_date, bir.amount AS bank_amount\nFROM invoices i\nJOIN bank_import_rows bir ON bir.kid_number = i.kid_number\nWHERE i.payment_id IS NULL AND i.status = \'open\' AND bir.amount > 0\nORDER BY bir.row_date DESC',
  },
])

async function run() {
  if (!sql.value.trim()) return
  running.value = true
  error.value = null
  result.value = null
  try {
    const res = await totpAwareFetch('/api/v1/admin/dev/query', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sql: sql.value }),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(body.error ?? res.statusText)
    }
    result.value = await res.json()
  } catch (e: any) {
    error.value = e?.message ?? 'Failed'
  } finally {
    running.value = false
  }
}

function fillExample(s: string) {
  sql.value = s
  result.value = null
  error.value = null
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return '∅'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

async function copyAsTSV() {
  if (!result.value) return
  const header = result.value.columns.join('\t')
  const body = result.value.rows
    .map((row) => row.map((v) => formatValue(v).replace(/\t/g, ' ')).join('\t'))
    .join('\n')
  await navigator.clipboard.writeText(`${header}\n${body}`)
  copiedTsv.value = true
  setTimeout(() => (copiedTsv.value = false), 2000)
}

function onKeyDown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    e.preventDefault()
    run()
  }
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.devQuery.title') }}</h1>
    <p class="mt-1 text-sm text-gray-600">{{ t('admin.devQuery.subtitle') }}</p>

    <div class="mt-4 flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900">
      <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" />
      <p>{{ t('admin.devQuery.warning') }}</p>
    </div>

    <div class="mt-5">
      <label class="block text-sm font-medium text-gray-700">{{ t('admin.devQuery.sqlLabel') }}</label>
      <textarea
        v-model="sql"
        rows="8"
        class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        :placeholder="t('admin.devQuery.placeholder')"
        @keydown="onKeyDown"
      ></textarea>
      <div class="mt-2 flex items-center justify-between">
        <div class="flex flex-wrap gap-2">
          <button
            v-for="(ex, i) in examples"
            :key="i"
            type="button"
            class="rounded border border-gray-200 bg-white px-2 py-1 text-xs text-gray-700 hover:bg-gray-50"
            @click="fillExample(ex.sql)"
          >
            {{ ex.label }}
          </button>
        </div>
        <button
          type="button"
          :disabled="running || !sql.trim()"
          class="inline-flex items-center gap-1 rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
          @click="run"
        >
          <Play class="h-4 w-4" />
          {{ running ? t('common.loading') : t('admin.devQuery.run') }}
          <span class="ml-1 text-xs opacity-70">⌘↵</span>
        </button>
      </div>
    </div>

    <div v-if="error" class="mt-5 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</div>

    <div v-if="result" class="mt-5">
      <div class="flex items-center justify-between text-xs text-gray-500">
        <span>
          {{ t('admin.devQuery.resultMeta', { rows: result.row_count, ms: result.elapsed_ms }) }}
          <span v-if="result.truncated" class="ml-1 font-semibold text-amber-700">{{ t('admin.devQuery.truncated') }}</span>
        </span>
        <button
          type="button"
          class="inline-flex items-center gap-1 rounded border border-gray-200 bg-white px-2 py-1 text-xs text-gray-700 hover:bg-gray-50"
          :disabled="result.row_count === 0"
          @click="copyAsTSV"
        >
          <component :is="copiedTsv ? Check : Copy" class="h-3 w-3" />
          {{ copiedTsv ? t('admin.devQuery.copied') : t('admin.devQuery.copyTsv') }}
        </button>
      </div>

      <div v-if="result.row_count === 0" class="mt-3 rounded-md border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500">
        {{ t('admin.devQuery.empty') }}
      </div>

      <div v-else class="mt-3 overflow-x-auto rounded-md border border-gray-200">
        <table class="min-w-full divide-y divide-gray-200">
          <thead class="bg-gray-50">
            <tr>
              <th
                v-for="col in result.columns"
                :key="col"
                class="whitespace-nowrap px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500"
              >
                {{ col }}
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 bg-white">
            <tr v-for="(row, ri) in result.rows" :key="ri" class="hover:bg-gray-50">
              <td
                v-for="(cell, ci) in row"
                :key="ci"
                class="whitespace-nowrap px-3 py-2 font-mono text-xs text-gray-700"
                :class="{ 'text-gray-300 italic': cell === null }"
              >
                {{ formatValue(cell) }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
