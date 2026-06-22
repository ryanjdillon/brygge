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
  Paperclip,
} from 'lucide-vue-next'
import {
  useAccountsList,
  useFiscalPeriods,
  useCreateJournalEntry,
  usePostEntry,
  useUploadJournalAttachment,
  useParseReceipt,
  type Account,
  type ReceiptData,
} from '@/composables/useAccounting'
import Select from '@/components/ui/form/Select.vue'
import Input from '@/components/ui/form/Input.vue'
import NumberInput from '@/components/ui/form/NumberInput.vue'
import DateInput from '@/components/ui/form/DateInput.vue'

const router = useRouter()
const { t } = useI18n()

const { data: accounts } = useAccountsList()
const { data: periods } = useFiscalPeriods()
const createMutation = useCreateJournalEntry()
const postMutation = usePostEntry()
const uploadMutation = useUploadJournalAttachment()
const parseMutation = useParseReceipt()

const receiptFile = ref<File | null>(null)
const uploadError = ref('')
const parsedReceipt = ref<ReceiptData | null>(null)
const parseStatus = ref<'idle' | 'parsing' | 'ok' | 'failed'>('idle')

function handleFileChange(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0] ?? null
  receiptFile.value = file
  uploadError.value = ''
  parsedReceipt.value = null
  parseStatus.value = 'idle'

  if (!file) return

  parseStatus.value = 'parsing'
  parseMutation.mutate(file, {
    onSuccess: (data) => {
      parsedReceipt.value = data
      parseStatus.value = 'ok'
      if (!description.value.trim() && data.description) {
        description.value = data.description
      }
      if (data.date) {
        entryDate.value = data.date
      }
    },
    onError: () => {
      parseStatus.value = 'failed'
    },
  })
}

async function uploadReceiptIfSelected(entryId: string): Promise<void> {
  if (!receiptFile.value) return
  await new Promise<void>((resolve, reject) => {
    uploadMutation.mutate(
      { entryId, file: receiptFile.value! },
      { onSuccess: () => resolve(), onError: reject },
    )
  })
}

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
  parseStatus.value !== 'parsing' &&
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
  uploadError.value = ''
  createMutation.mutate(buildPayload(), {
    onSuccess: async (entry) => {
      try {
        await uploadReceiptIfSelected(entry.id)
      } catch {
        uploadError.value = t('admin.accounting.journalForm.receiptUploadFailed')
      }
      router.push('/admin/accounting/journal')
    },
    onError: (err) => {
      errorMessage.value = (err as Error).message
    },
  })
}

function handlePost() {
  errorMessage.value = ''
  uploadError.value = ''
  createMutation.mutate(buildPayload(), {
    onSuccess: async (entry) => {
      try {
        await uploadReceiptIfSelected(entry.id)
      } catch {
        uploadError.value = t('admin.accounting.journalForm.receiptUploadFailed')
      }
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

const periodOptions = computed(() =>
  openPeriods.value.map((p) => ({ value: p.id, label: String(p.year) })),
)

const accountOptions = computed(() => [
  { value: '', label: t('admin.accounting.journalForm.selectAccount') },
  ...(accounts.value ?? []).map((a) => ({ value: a.code, label: accountLabel(a) })),
])
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
        <Select v-model="selectedPeriodId" :options="periodOptions" />
        <p v-if="!openPeriods.length" class="mt-1 text-xs text-red-600">{{ t('admin.accounting.periods.noPeriods') }}</p>
      </div>
      <div>
        <label class="mb-1 flex items-center gap-1 text-sm font-medium text-gray-700">
          {{ t('admin.accounting.journalForm.date') }}
          <Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipDate')" />
        </label>
        <DateInput v-model="entryDate" />
      </div>
      <div>
        <label class="mb-1 flex items-center gap-1 text-sm font-medium text-gray-700">
          {{ t('admin.accounting.journalForm.description') }}
          <Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.tooltipDescription')" />
        </label>
        <Input
          v-model="description"
          :placeholder="t('admin.accounting.journalForm.descriptionPlaceholder')"
        />
      </div>
    </div>

    <div class="mt-4">
      <label class="mb-1 flex items-center gap-1 text-sm font-medium text-gray-700">
        <Paperclip class="h-3.5 w-3.5" />
        {{ t('admin.accounting.journalForm.receipt') }}
        <Info class="h-3.5 w-3.5 text-gray-400" :title="t('admin.accounting.journalForm.receiptTooltip')" />
      </label>
      <input
        type="file"
        accept="image/*,application/pdf"
        class="block text-sm text-gray-600 file:mr-3 file:rounded file:border-0 file:bg-gray-100 file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-gray-700 hover:file:bg-gray-200"
        @change="handleFileChange"
      />
      <!-- parsing state feedback -->
      <div v-if="parseStatus === 'parsing'" class="mt-2 flex items-center gap-2 text-sm text-brand-700">
        <svg class="h-4 w-4 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
        </svg>
        {{ t('admin.accounting.journalForm.receiptParsing') }}
      </div>
      <div v-else-if="parseStatus === 'ok' && parsedReceipt" class="mt-2 rounded border border-green-200 bg-green-50 px-3 py-2 text-xs text-green-800">
        <p class="font-medium">{{ t('admin.accounting.journalForm.receiptParsedOk') }}</p>
        <p class="mt-0.5">
          <span class="font-medium">{{ parsedReceipt.vendor || t('admin.accounting.journalForm.receiptUnknownVendor') }}</span>
          <span v-if="parsedReceipt.total_amount"> · {{ formatNOK(parsedReceipt.total_amount) }} kr</span>
          <span v-if="parsedReceipt.mva_amount"> · {{ t('admin.accounting.journalForm.mva') }} {{ formatNOK(parsedReceipt.mva_amount) }} kr</span>
          <span v-if="parsedReceipt.date"> · {{ parsedReceipt.date }}</span>
        </p>
      </div>
      <div v-else-if="parseStatus === 'failed'" class="mt-2 rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800">
        {{ t('admin.accounting.journalForm.receiptParseFailed') }}
      </div>
      <p v-if="uploadError" class="mt-1 text-xs text-red-600">{{ uploadError }}</p>
      <p v-if="uploadMutation.isPending.value" class="mt-1 text-xs text-brand-600">{{ t('admin.accounting.journalForm.receiptUploading') }}</p>
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
                <div class="min-w-[200px]">
                  <Select v-model="line.account_code" :options="accountOptions" />
                </div>
              </td>
              <td class="py-2 pr-3">
                <div class="w-28">
                  <NumberInput v-model="line.debit" :min="0" :step="0.01" placeholder="0.00" />
                </div>
              </td>
              <td class="py-2 pr-3">
                <div class="w-28">
                  <NumberInput v-model="line.credit" :min="0" :step="0.01" placeholder="0.00" />
                </div>
              </td>
              <td class="py-2 pr-3">
                <div v-if="isExpenseAccount(line.account_code)" class="w-24">
                  <NumberInput v-model="line.mva_amount" :min="0" :step="0.01" placeholder="0.00" />
                </div>
                <span v-else class="block w-24 text-right text-sm text-gray-400">-</span>
              </td>
              <td class="py-2 pr-3">
                <div class="min-w-[150px]">
                  <Input
                    v-model="line.description"
                    :placeholder="t('admin.accounting.journalForm.optional')"
                  />
                </div>
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
        class="mt-3 inline-flex items-center gap-1 text-sm font-medium text-brand-600 hover:text-brand-800"
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
        class="rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
        :disabled="!canSave || !isBalanced || createMutation.isPending.value || postMutation.isPending.value"
        @click="handlePost"
      >
        {{ t('admin.accounting.journalForm.postEntry') }}
      </button>
    </div>
  </div>
</template>
