<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  ArrowLeft,
  Plus,
  Trash2,
  Check,
  X,
} from 'lucide-vue-next'
import {
  useAccountsList,
  useFiscalPeriods,
  useCreateJournalEntry,
  usePostEntry,
  type Account,
} from '@/composables/useAccounting'

const router = useRouter()

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
  if (lines.value.length > 2) {
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
          errorMessage.value = `Bilag opprettet som kladd, men kunne ikke postere: ${(err as Error).message}`
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
      Tilbake til bilagslisten
    </button>

    <h1 class="text-2xl font-bold text-gray-900">Nytt bilag</h1>

    <div class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
      <div>
        <label class="mb-1 block text-sm font-medium text-gray-700">Periode</label>
        <select
          v-model="selectedPeriodId"
          class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        >
          <option v-for="p in openPeriods" :key="p.id" :value="p.id">{{ p.year }}</option>
        </select>
        <p v-if="!openPeriods.length" class="mt-1 text-xs text-red-600">Ingen åpne perioder</p>
      </div>
      <div>
        <label class="mb-1 block text-sm font-medium text-gray-700">Dato</label>
        <input
          v-model="entryDate"
          type="date"
          class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        />
      </div>
      <div>
        <label class="mb-1 block text-sm font-medium text-gray-700">Beskrivelse</label>
        <input
          v-model="description"
          type="text"
          placeholder="F.eks. Strømregning mars"
          class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        />
      </div>
    </div>

    <div class="mt-6">
      <h2 class="mb-3 text-lg font-semibold text-gray-800">Posteringslinjer</h2>
      <div class="overflow-x-auto">
        <table class="min-w-full">
          <thead>
            <tr class="text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <th class="pb-2 pr-3">Konto</th>
              <th class="pb-2 pr-3 text-right">Debet</th>
              <th class="pb-2 pr-3 text-right">Kredit</th>
              <th class="pb-2 pr-3 text-right">MVA</th>
              <th class="pb-2 pr-3">Beskrivelse</th>
              <th class="pb-2"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(line, idx) in lines" :key="idx" class="border-t border-gray-200">
              <td class="py-2 pr-3">
                <select
                  v-model="line.account_code"
                  class="w-full min-w-[200px] rounded-md border border-gray-300 px-2 py-1.5 text-sm"
                >
                  <option value="">Velg konto...</option>
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
                  class="w-28 rounded-md border border-gray-300 px-2 py-1.5 text-right text-sm"
                />
                <span v-else class="block w-28 text-right text-sm text-gray-400">-</span>
              </td>
              <td class="py-2 pr-3">
                <input
                  v-model="line.description"
                  type="text"
                  placeholder="Valgfri"
                  class="w-full min-w-[150px] rounded-md border border-gray-300 px-2 py-1.5 text-sm"
                />
              </td>
              <td class="py-2">
                <button
                  class="rounded p-1 text-gray-400 hover:bg-red-50 hover:text-red-600 disabled:opacity-30"
                  :disabled="lines.length <= 2"
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
        Legg til linje
      </button>
    </div>

    <div class="mt-6 flex items-center gap-6 rounded-lg border border-gray-200 bg-gray-50 p-4">
      <div class="text-sm">
        <span class="font-medium text-gray-700">Sum debet:</span>
        <span class="ml-1 font-mono">{{ formatNOK(totalDebit) }}</span>
      </div>
      <div class="text-sm">
        <span class="font-medium text-gray-700">Sum kredit:</span>
        <span class="ml-1 font-mono">{{ formatNOK(totalCredit) }}</span>
      </div>
      <div class="text-sm">
        <span class="font-medium text-gray-700">Differanse:</span>
        <span class="ml-1 font-mono">{{ formatNOK(difference) }}</span>
      </div>
      <div class="flex items-center gap-1">
        <Check v-if="isBalanced" class="h-5 w-5 text-green-600" />
        <X v-else class="h-5 w-5 text-red-500" />
        <span :class="isBalanced ? 'text-green-600' : 'text-red-500'" class="text-sm font-medium">
          {{ isBalanced ? 'Balansert' : 'Ikke balansert' }}
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
        Lagre som kladd
      </button>
      <button
        class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
        :disabled="!canSave || !isBalanced || createMutation.isPending.value || postMutation.isPending.value"
        @click="handlePost"
      >
        Poster
      </button>
    </div>
  </div>
</template>
