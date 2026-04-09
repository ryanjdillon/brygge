<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { RouterLink } from 'vue-router'
import {
  BookOpen,
  CalendarDays,
  FileText,
  RefreshCw,
  Plus,
} from 'lucide-vue-next'
import {
  useAccountsList,
  useFiscalPeriods,
  useJournalEntries,
  useSeedAccounts,
  useCreatePeriod,
  useSyncPayments,
  useSyncInvoices,
} from '@/composables/useAccounting'

const { data: accounts } = useAccountsList()
const { data: periods, isLoading: periodsLoading } = useFiscalPeriods()
const seedMutation = useSeedAccounts()
const createPeriodMutation = useCreatePeriod()
const syncPaymentsMutation = useSyncPayments()
const syncInvoicesMutation = useSyncInvoices()

const selectedPeriodId = ref('')
const syncMessage = ref('')

watch(periods, (val) => {
  if (val && val.length > 0 && !selectedPeriodId.value) {
    const open = val.find(p => p.status === 'open')
    selectedPeriodId.value = open?.id ?? val[0].id
  }
}, { immediate: true })

const { data: entries } = useJournalEntries(selectedPeriodId)

const totalAccounts = computed(() => accounts.value?.length ?? 0)
const totalEntries = computed(() => entries.value?.length ?? 0)
const postedCount = computed(() => entries.value?.filter(e => e.status === 'posted').length ?? 0)
const hasAccounts = computed(() => totalAccounts.value > 0)
const hasPeriods = computed(() => (periods.value?.length ?? 0) > 0)

const newYear = ref(new Date().getFullYear())

function handleSeedAccounts() {
  seedMutation.mutate(undefined)
}

function handleCreatePeriod() {
  createPeriodMutation.mutate({ year: newYear.value })
}

async function handleSyncPayments() {
  syncMessage.value = ''
  syncPaymentsMutation.mutate({ period_id: selectedPeriodId.value }, {
    onSuccess: (data) => {
      syncMessage.value = `Betalinger synkronisert: ${data.synced} nye, ${data.skipped} hoppet over`
    },
    onError: (err) => {
      syncMessage.value = `Feil: ${(err as Error).message}`
    },
  })
}

async function handleSyncInvoices() {
  syncMessage.value = ''
  syncInvoicesMutation.mutate({ period_id: selectedPeriodId.value }, {
    onSuccess: (data) => {
      syncMessage.value = `Fakturaer synkronisert: ${data.synced} nye, ${data.skipped} hoppet over`
    },
    onError: (err) => {
      syncMessage.value = `Feil: ${(err as Error).message}`
    },
  })
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">Regnskap</h1>

    <div v-if="periodsLoading" class="mt-6 text-gray-500">Laster...</div>

    <template v-else>
      <div v-if="!hasPeriods" class="mt-6 rounded-lg border border-dashed border-gray-300 p-8 text-center">
        <CalendarDays class="mx-auto h-12 w-12 text-gray-400" />
        <h3 class="mt-2 text-sm font-semibold text-gray-900">Ingen regnskapsperioder</h3>
        <p class="mt-1 text-sm text-gray-500">Opprett ditt første regnskapsår for å komme i gang.</p>
        <div class="mt-4 flex items-center justify-center gap-2">
          <input
            v-model.number="newYear"
            type="number"
            min="2000"
            max="2100"
            class="w-24 rounded-md border border-gray-300 px-3 py-2 text-sm"
          />
          <button
            class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
            :disabled="createPeriodMutation.isPending.value"
            @click="handleCreatePeriod"
          >
            <Plus class="h-4 w-4" />
            Opprett regnskapsår
          </button>
        </div>
      </div>

      <div v-if="!hasAccounts && hasPeriods" class="mt-4 rounded-lg border border-dashed border-gray-300 p-6 text-center">
        <BookOpen class="mx-auto h-10 w-10 text-gray-400" />
        <h3 class="mt-2 text-sm font-semibold text-gray-900">Ingen kontoplan</h3>
        <p class="mt-1 text-sm text-gray-500">Opprett standard norsk kontoplan for båtforening.</p>
        <button
          class="mt-3 inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
          :disabled="seedMutation.isPending.value"
          @click="handleSeedAccounts"
        >
          Opprett standard kontoplan
        </button>
        <p v-if="seedMutation.isSuccess.value" class="mt-2 text-sm text-green-600">
          {{ seedMutation.data.value?.seeded }} kontoer opprettet
        </p>
      </div>

      <template v-if="hasPeriods">
        <div class="mt-6 flex items-center gap-4">
          <label class="text-sm font-medium text-gray-700">Periode:</label>
          <select
            v-model="selectedPeriodId"
            class="rounded-md border border-gray-300 px-3 py-2 text-sm"
          >
            <option v-for="p in periods" :key="p.id" :value="p.id">
              {{ p.year }} ({{ p.status === 'open' ? 'Åpen' : 'Lukket' }})
            </option>
          </select>
        </div>

        <div class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div class="rounded-lg border border-gray-200 bg-white p-5">
            <p class="text-sm font-medium text-gray-500">Kontoer</p>
            <p class="mt-1 text-2xl font-semibold text-gray-900">{{ totalAccounts }}</p>
          </div>
          <div class="rounded-lg border border-gray-200 bg-white p-5">
            <p class="text-sm font-medium text-gray-500">Bilag totalt</p>
            <p class="mt-1 text-2xl font-semibold text-gray-900">{{ totalEntries }}</p>
          </div>
          <div class="rounded-lg border border-gray-200 bg-white p-5">
            <p class="text-sm font-medium text-gray-500">Posterte bilag</p>
            <p class="mt-1 text-2xl font-semibold text-gray-900">{{ postedCount }}</p>
          </div>
        </div>

        <div class="mt-6 flex flex-wrap gap-3">
          <button
            class="inline-flex items-center gap-1.5 rounded-md bg-green-600 px-4 py-2 text-sm font-medium text-white hover:bg-green-700 disabled:opacity-50"
            :disabled="syncPaymentsMutation.isPending.value || !selectedPeriodId"
            @click="handleSyncPayments"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': syncPaymentsMutation.isPending.value }" />
            Sync betalinger
          </button>
          <button
            class="inline-flex items-center gap-1.5 rounded-md bg-green-600 px-4 py-2 text-sm font-medium text-white hover:bg-green-700 disabled:opacity-50"
            :disabled="syncInvoicesMutation.isPending.value || !selectedPeriodId"
            @click="handleSyncInvoices"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': syncInvoicesMutation.isPending.value }" />
            Sync fakturaer
          </button>
        </div>

        <p v-if="syncMessage" class="mt-3 text-sm" :class="syncMessage.startsWith('Feil') ? 'text-red-600' : 'text-green-600'">
          {{ syncMessage }}
        </p>

        <div class="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <RouterLink
            to="/admin/accounting/accounts"
            class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm"
          >
            <BookOpen class="h-8 w-8 text-blue-600" />
            <div>
              <p class="font-medium text-gray-900">Kontoplan</p>
              <p class="text-sm text-gray-500">Administrer kontoer</p>
            </div>
          </RouterLink>
          <RouterLink
            to="/admin/accounting/journal"
            class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm"
          >
            <FileText class="h-8 w-8 text-blue-600" />
            <div>
              <p class="font-medium text-gray-900">Bilag</p>
              <p class="text-sm text-gray-500">Bilagsjournal</p>
            </div>
          </RouterLink>
          <RouterLink
            to="/admin/accounting/periods"
            class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-5 transition hover:border-blue-300 hover:shadow-sm"
          >
            <CalendarDays class="h-8 w-8 text-blue-600" />
            <div>
              <p class="font-medium text-gray-900">Perioder</p>
              <p class="text-sm text-gray-500">Regnskapsperioder</p>
            </div>
          </RouterLink>
        </div>
      </template>
    </template>
  </div>
</template>
