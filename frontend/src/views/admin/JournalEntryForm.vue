<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  ArrowLeft,
  Plus,
  Trash2,
  Check,
  X,
  Info,
} from 'lucide-vue-next'
import {
  useAccountsList,
  useFiscalPeriods,
  useCreateJournalEntry,
  usePostEntry,
  type Account,
} from '@/composables/useAccounting'

const router = useRouter()
const { t } = useI18n()

const { data: accounts } = useAccountsList()
const { data: periods } = useFiscalPeriods()
const createMutation = useCreateJournalEntry()
const postMutation = usePostEntry()

const selectedPeriodId = ref('')
const entryDate = ref(new Date().toISOString().slice(0, 10))
const description = ref('')
const errorMessage = ref('')

interface FormLine {
  account_code: string
  debit: number | null
  credit: number | null
  mva_amount: number | null
  description: string
}

const lines = ref<FormLine[]>([
  { account_code: '', debit: null, credit: null, mva_amount: null, description: '' },
])

const openPeriods = computed(() => periods.value?.filter(p => p.status === 'open') ?? [])

watch(periods, (val) => {
  if (val && val.length > 0 && !selectedPeriodId.value) {
    const open = val.find(p => p.status === 'open')
    if (open) selectedPeriodId.value = open.id
  }
}, { immediate: true })

const expenseAccounts = computed(() => {
  if (!accounts.value) return new Set<string>()
  return new Set(accounts.value.filter(a => a.account_type === 'expense').map(a => a.code))
})

function isExpenseAccount(code: string): boolean {
  return expenseAccounts.value.has(code)
}

function addLine() {
  lines.value.push({ account_code: '', debit: null, credit: null, mva_amount: null, description: '' })
}

function removeLine(index: number) {
  if (lines.value.length > 1) {
    lines.value.splice(index, 1)
  }
}

const totalDebit = computed(() =>
  lines.value.reduce((sum, l) => sum + (l.debit ?? 0), 0),
)

const totalCredit = computed(() =>
  lines.value.reduce((sum, l) => sum + (l.credit ?? 0), 0),
)

const difference = computed(() => Math.abs(totalDebit.value - totalCredit.value))
const isBalanced = computed(() => difference.value < 0.01 && totalDebit.value > 0)

const canSave = computed(() =>
  selectedPeriodId.value &&
  entryDate.value &&
  description.value.trim() &&
  lines.value.every(l => l.account_code && ((l.debit ?? 0) > 0 || (l.credit ?? 0) > 0)),
)

function buildPayload() {
  return {
    fiscal_period_id: selectedPeriodId.value,
    entry_date: entryDate.value,
    description: description.value.trim(),
    lines: lines.value.map(l => ({
      account_code: l.account_code,
      debit: l.debit ?? 0,
      credit: l.credit ?? 0,
      description: l.description,
      mva_amount: l.mva_amount ?? 0,
    })),
  }
}

function handleSaveDraft() {
  errorMessage.value = ''
  createMutation.mutate(buildPayload(), {
    onSuccess: () => {
      router.push('/admin/accounting/journal')
    },
    onError: (err) => {
      errorMessage.value = (err as Error).message
    },
  })
}

function handlePost() {
  errorMessage.value = ''
  createMutation.mutate(buildPayload(), {
    onSuccess: (entry) => {
      postMutation.mutate(entry.id, {
        onSuccess: () => {
          router.push('/admin/accounting/journal')
        },
        onError: (err) => {
          errorMessage.value = `${t('common.error')}: ${(err as Error).message}`
        },
      })
    },
    onError: (err) => {
      errorMessage.value = (err as Error).message
    },
  })
}

function formatNOK(amount: number): string {
  return new Intl.NumberFormat('nb-NO', { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(amount)
}

function accountLabel(a: Account): string {
  return `${a.code} – ${a.name}`
}
</script>

<template>
  <div>
    <button
      class="mb-4 inline-flex items-center gap-1 text-sm text-gray-500 hover:text-gray-700"
      @click="router.push('/admin/accounting/journal')"
    >
      <ArrowLeft class="h-4 w-4" />
      {{ t('admin.accounting.journalForm.back') }}
    </button>

    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.accounting.journalForm.title') }}</h1>

    <div class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
      <div>
        <label class="mb-1 flex items-center gap-1 text-sm font-medium text-gray-700">
          {{ t('admin.accounting.journalForm.period') }}
          <Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipPeriod')" />
        </label>
        <select
          v-model="selectedPeriodId"
          class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        >
          <option v-for="p in openPeriods" :key="p.id" :value="p.id">{{ p.year }}</option>
        </select>
        <p v-if="!openPeriods.length" class="mt-1 text-xs text-red-600">{{ t('admin.accounting.periods.noPeriods') }}</p>
      </div>
      <div>
        <label class="mb-1 flex items-center gap-1 text-sm font-medium text-gray-700">
          {{ t('admin.accounting.journalForm.date') }}
          <Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipDate')" />
        </label>
        <input
          v-model="entryDate"
          type="date"
          class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        />
      </div>
      <div>
        <label class="mb-1 flex items-center gap-1 text-sm font-medium text-gray-700">
          {{ t('admin.accounting.journalForm.description') }}
          <Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipDescription')" />
        </label>
        <input
          v-model="description"
          type="text"
          :placeholder="t('admin.accounting.journalForm.descriptionPlaceholder')"
          class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        />
      </div>
    </div>

    <div class="mt-6">
      <h2 class="mb-3 text-lg font-semibold text-gray-800">{{ t('admin.accounting.journalForm.lines') }}</h2>
      <div class="overflow-x-auto">
        <table class="min-w-full">
          <thead>
            <tr class="text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <th class="min-w-[200px] pb-2 pr-3">
                <span class="inline-flex items-center gap-1">{{ t('admin.accounting.journalForm.account') }}<Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipAccount')" /></span>
              </th>
              <th class="w-28 pb-2 pr-3 text-right">
                <span class="inline-flex items-center justify-end gap-1">{{ t('admin.accounting.journalForm.debit') }}<Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipDebit')" /></span>
              </th>
              <th class="w-28 pb-2 pr-3 text-right">
                <span class="inline-flex items-center justify-end gap-1">{{ t('admin.accounting.journalForm.credit') }}<Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipCredit')" /></span>
              </th>
              <th class="w-24 pb-2 pr-3 text-right">
                <span class="inline-flex items-center justify-end gap-1">{{ t('admin.accounting.journalForm.mva') }}<Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipMva')" /></span>
              </th>
              <th class="pb-2 pr-3">
                <span class="inline-flex items-center gap-1">{{ t('admin.accounting.journalForm.lineDescription') }}<Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipLineDesc')" /></span>
              </th>
              <th class="w-10 pb-2"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(line, idx) in lines" :key="idx" class="border-t border-gray-200">
              <td class="py-2 pr-3">
                <select
                  v-model="line.account_code"
                  class="w-full min-w-[200px] rounded-md border border-gray-300 px-2 py-1.5 text-sm"
                >
                  <option value="">{{ t('admin.accounting.journalForm.selectAccount') }}</option>
                  <option v-for="a in accounts" :key="a.id" :value="a.code">
                    {{ accountLabel(a) }}
                  </option>
                </select>
              </td>
              <td class="py-2 pr-3">
                <input
                  v-model.number="line.debit"
                  type="number"
                  step="0.01"
                  min="0"
                  placeholder="0.00"
                  class="w-28 rounded-md border border-gray-300 px-2 py-1.5 text-right text-sm"
                />
              </td>
              <td class="py-2 pr-3">
                <input
                  v-model.number="line.credit"
                  type="number"
                  step="0.01"
                  min="0"
                  placeholder="0.00"
                  class="w-28 rounded-md border border-gray-300 px-2 py-1.5 text-right text-sm"
                />
              </td>
              <td class="py-2 pr-3">
                <input
                  v-if="isExpenseAccount(line.account_code)"
                  v-model.number="line.mva_amount"
                  type="number"
                  step="0.01"
                  min="0"
                  placeholder="0.00"
                  class="w-24 rounded-md border border-gray-300 px-2 py-1.5 text-right text-sm"
                />
                <span v-else class="block w-24 text-right text-sm text-gray-400">-</span>
              </td>
              <td class="py-2 pr-3">
                <input
                  v-model="line.description"
                  type="text"
                  :placeholder="t('admin.accounting.journalForm.optional')"
                  class="w-full min-w-[150px] rounded-md border border-gray-300 px-2 py-1.5 text-sm"
                />
              </td>
              <td class="py-2">
                <button
                  class="rounded p-1 text-gray-400 hover:bg-red-50 hover:text-red-600 disabled:opacity-30"
                  :disabled="lines.length <= 1"
                  @click="removeLine(idx)"
                >
                  <Trash2 class="h-4 w-4" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <button
        class="mt-3 inline-flex items-center gap-1 text-sm font-medium text-blue-600 hover:text-blue-800"
        @click="addLine"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.accounting.journalForm.addLine') }}
      </button>
    </div>

    <div class="mt-6 flex items-center gap-6 rounded-lg border border-gray-200 bg-gray-50 p-4">
      <div class="text-sm">
        <span class="font-medium text-gray-700">{{ t('admin.accounting.journalForm.totalDebit') }}:</span>
        <span class="ml-1 font-mono">{{ formatNOK(totalDebit) }}</span>
      </div>
      <div class="text-sm">
        <span class="font-medium text-gray-700">{{ t('admin.accounting.journalForm.totalCredit') }}:</span>
        <span class="ml-1 font-mono">{{ formatNOK(totalCredit) }}</span>
      </div>
      <div class="text-sm">
        <span class="font-medium text-gray-700">{{ t('admin.accounting.journalForm.difference') }}:</span>
        <span class="ml-1 font-mono">{{ formatNOK(difference) }}</span>
      </div>
      <div class="flex items-center gap-1">
        <Check v-if="isBalanced" class="h-5 w-5 text-green-600" />
        <X v-else class="h-5 w-5 text-red-500" />
        <span :class="isBalanced ? 'text-green-600' : 'text-red-500'" class="text-sm font-medium">
          {{ isBalanced ? t('admin.accounting.journalForm.balanced') : t('admin.accounting.journalForm.notBalanced') }}
        </span>
      </div>
    </div>

    <p v-if="errorMessage" class="mt-3 text-sm text-red-600">{{ errorMessage }}</p>

    <div class="mt-6 flex gap-3">
      <button
        class="rounded-md bg-gray-600 px-4 py-2 text-sm font-medium text-white hover:bg-gray-700 disabled:opacity-50"
        :disabled="!canSave || createMutation.isPending.value"
        @click="handleSaveDraft"
      >
        {{ t('admin.accounting.journalForm.saveDraft') }}
      </button>
      <button
        class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
        :disabled="!canSave || !isBalanced || createMutation.isPending.value || postMutation.isPending.value"
        @click="handlePost"
      >
        {{ t('admin.accounting.journalForm.postEntry') }}
      </button>
    </div>
  </div>
</template>
