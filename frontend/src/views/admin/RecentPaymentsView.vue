<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useApi } from '@/composables/useApi'
import { formatNOK as nok } from '@/lib/format'
import { RefreshCw, Inbox, Search, Anchor, Caravan, FileText, ShoppingBag, CircleDollarSign } from 'lucide-vue-next'
import PaymentMethodChip, { type PaymentMethod } from '@/components/admin/PaymentMethodChip.vue'

const { t } = useI18n()
const { fetchApi } = useApi()

interface Payment {
  id: string
  paid_at: string
  amount: number
  payer: string
  summary: string
  kind: 'guest' | 'motorhome' | 'faktura' | 'merch' | 'other'
  channel: string
  method: PaymentMethod
  card_brand: string | null
}

const payments = ref<Payment[]>([])
const loading = ref(false)
const kind = ref('')
const since = ref('')
const until = ref('')
const search = ref('')

const kinds = ['', 'guest', 'motorhome', 'faktura', 'merch', 'other'] as const

const kindMeta: Record<string, { icon: any; classes: string }> = {
  guest: { icon: Anchor, classes: 'bg-blue-50 text-blue-700 ring-blue-200' },
  motorhome: { icon: Caravan, classes: 'bg-teal-50 text-teal-700 ring-teal-200' },
  faktura: { icon: FileText, classes: 'bg-slate-100 text-slate-700 ring-slate-200' },
  merch: { icon: ShoppingBag, classes: 'bg-amber-50 text-amber-700 ring-amber-200' },
  other: { icon: CircleDollarSign, classes: 'bg-gray-50 text-gray-600 ring-gray-200' },
}

let searchTimer: ReturnType<typeof setTimeout> | undefined

async function load() {
  loading.value = true
  try {
    const p = new URLSearchParams()
    if (kind.value) p.set('kind', kind.value)
    if (since.value) p.set('since', since.value)
    if (until.value) p.set('until', until.value)
    if (search.value.trim()) p.set('q', search.value.trim())
    const res = await fetchApi<{ payments: Payment[] }>(`/api/v1/harbor/recent-payments?${p.toString()}`)
    payments.value = res?.payments ?? []
  } finally {
    loading.value = false
  }
}

function onSearchInput() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(load, 300)
}

function fmtWhen(s: string): string {
  if (!s) return '—'
  return new Date(s).toLocaleString('nb-NO', {
    day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit',
  })
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-3xl px-3 py-6 sm:px-6">
    <div class="flex items-center justify-between gap-3">
      <div>
        <h1 class="text-xl font-bold text-slate-900 sm:text-2xl">{{ t('recentPayments.title') }}</h1>
        <p class="mt-1 text-sm text-slate-500">{{ t('recentPayments.subtitle') }}</p>
      </div>
      <button
        class="flex shrink-0 items-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
        :disabled="loading"
        @click="load"
      >
        <RefreshCw class="h-4 w-4" :class="loading ? 'animate-spin' : ''" />
        <span class="hidden sm:inline">{{ t('recentPayments.refresh') }}</span>
      </button>
    </div>

    <!-- Filters -->
    <div class="mt-4 space-y-3">
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="k in kinds"
          :key="k || 'all'"
          class="rounded-full px-3 py-1 text-xs font-medium ring-1 ring-inset"
          :class="kind === k ? 'bg-slate-900 text-white ring-slate-900' : 'bg-white text-slate-600 ring-slate-200 hover:bg-slate-50'"
          @click="kind = k; load()"
        >
          {{ t('recentPayments.kind.' + (k || 'all')) }}
        </button>
      </div>

      <div class="relative">
        <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
        <input
          v-model="search"
          type="search"
          :placeholder="t('recentPayments.searchPlaceholder')"
          class="w-full rounded-md border border-slate-300 py-2 pl-9 pr-3 text-sm"
          @input="onSearchInput"
        />
      </div>

      <div class="flex gap-2">
        <label class="flex-1 text-xs text-slate-500">
          {{ t('recentPayments.since') }}
          <input v-model="since" type="date" class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" @change="load" />
        </label>
        <label class="flex-1 text-xs text-slate-500">
          {{ t('recentPayments.until') }}
          <input v-model="until" type="date" class="mt-1 w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" @change="load" />
        </label>
      </div>
    </div>

    <!-- Empty -->
    <div v-if="!loading && payments.length === 0" class="mt-10 flex flex-col items-center py-10 text-center">
      <Inbox class="h-12 w-12 text-slate-300" />
      <p class="mt-3 text-sm text-slate-500">{{ t('recentPayments.empty') }}</p>
    </div>

    <!-- Payment cards (mobile-first, work on desktop too) -->
    <ul v-else class="mt-4 space-y-2">
      <li
        v-for="p in payments"
        :key="p.id"
        class="flex items-center gap-3 rounded-lg border border-slate-200 bg-white p-3"
      >
        <span
          class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full ring-1 ring-inset"
          :class="(kindMeta[p.kind] || kindMeta.other).classes"
        >
          <component :is="(kindMeta[p.kind] || kindMeta.other).icon" class="h-4 w-4" />
        </span>
        <div class="min-w-0 flex-1">
          <div class="flex items-baseline justify-between gap-2">
            <p class="truncate font-medium text-slate-900">{{ p.payer || t('recentPayments.anonymous') }}</p>
            <p class="shrink-0 font-semibold text-slate-900">{{ nok(p.amount) }}</p>
          </div>
          <div class="mt-0.5 flex items-center justify-between gap-2 text-xs text-slate-500">
            <span class="truncate">{{ p.summary || t('recentPayments.kind.' + p.kind) }}</span>
            <span class="shrink-0">{{ fmtWhen(p.paid_at) }}</span>
          </div>
          <div class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1">
            <PaymentMethodChip :method="p.method" :card-brand="p.card_brand" />
          </div>
        </div>
      </li>
    </ul>
  </div>
</template>
