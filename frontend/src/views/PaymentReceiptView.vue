<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Loader2, CheckCircle, XCircle, Clock } from 'lucide-vue-next'

const { t } = useI18n()
const route = useRoute()
const token = route.params.token as string

type Line = { name: string; quantity: number; total: number }
type Checkout = {
  status: string
  invoice: { number: number; lines: Line[] } | null
  lines: Line[]
  already_paid: boolean
  paid_amount: number
}

// The payer lands here from the payment provider before its webhook has
// necessarily arrived, and Vipps returns here on abort too, so the page
// asks the server for the order's state rather than assuming success.
// Backoff keeps a slow webhook within the public endpoint's rate limit.
const DELAYS_MS = [2000, 3000, 5000, 8000, 10000]
const MAX_POLLS = 15
const MAX_WAIT_MS = 90_000

const state = ref<'loading' | 'pending' | 'paid' | 'failed' | 'confirming' | 'notFound'>('loading')
const checkout = ref<Checkout | null>(null)
let polls = 0
let waited = 0
let timer: ReturnType<typeof setTimeout> | undefined
let stopped = false

function fmt(n: number): string {
  return n.toLocaleString('nb-NO', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function schedule() {
  if (stopped) return
  const delay = DELAYS_MS[Math.min(polls, DELAYS_MS.length - 1)]
  if (polls >= MAX_POLLS || waited + delay > MAX_WAIT_MS) {
    // A failed order stays failed; one still pending is most likely a late
    // webhook, so the payer is told the receipt will follow.
    if (state.value !== 'failed') state.value = 'confirming'
    return
  }
  polls++
  waited += delay
  timer = setTimeout(check, delay)
}

async function check() {
  let data: Checkout
  try {
    const res = await fetch(`/api/v1/checkout/${token}`)
    if (res.status === 404) {
      state.value = 'notFound'
      return
    }
    if (!res.ok) throw new Error(String(res.status))
    data = await res.json()
  } catch {
    if (state.value === 'loading') state.value = 'pending'
    schedule()
    return
  }
  checkout.value = data
  if (data.already_paid) {
    state.value = 'paid'
    return
  }
  // A failed order is shown at once, but polling continues: a payer who
  // retried may have a newer payment whose capture has not landed yet.
  state.value = data.status === 'failed' ? 'failed' : 'pending'
  schedule()
}

onMounted(check)
onBeforeUnmount(() => {
  stopped = true
  if (timer) clearTimeout(timer)
})
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-12 sm:px-6" role="status" aria-live="polite">
    <div v-if="state === 'loading'" class="space-y-4 py-16 text-center" data-testid="receipt-loading">
      <Loader2 class="mx-auto h-10 w-10 animate-spin text-slate-400" />
      <p class="text-slate-500">{{ t('pay.receipt.loading') }}</p>
    </div>

    <div v-else-if="state === 'notFound'" class="space-y-3 py-16 text-center" data-testid="receipt-not-found">
      <h1 class="text-xl font-semibold text-slate-900">{{ t('pay.receipt.notFoundTitle') }}</h1>
      <p class="text-slate-500">{{ t('pay.receipt.notFoundBody') }}</p>
    </div>

    <div v-else-if="state === 'paid'" class="space-y-6" data-testid="receipt-paid">
      <div class="space-y-3 py-6 text-center">
        <CheckCircle class="mx-auto h-14 w-14 text-green-600" />
        <h1 class="text-2xl font-bold text-slate-900">{{ t('pay.receipt.paidTitle') }}</h1>
        <p class="text-slate-500">{{ t('pay.receipt.paidBody') }}</p>
      </div>
      <div v-if="checkout" class="rounded-lg border border-slate-200 bg-white">
        <div v-if="checkout.invoice" class="px-4 py-2 text-sm text-slate-700" data-testid="receipt-invoice">
          {{ t('pay.receipt.invoice', { n: checkout.invoice.number }) }}
        </div>
        <div
          v-for="(line, i) in checkout.lines"
          :key="'line' + i"
          class="flex justify-between px-4 py-2 text-sm"
        >
          <span class="text-slate-700">{{ line.name }}<span v-if="line.quantity > 1" class="text-slate-400"> × {{ line.quantity }}</span></span>
          <span class="font-medium text-slate-900">{{ fmt(line.total) }} kr</span>
        </div>
        <div class="flex justify-between border-t border-slate-100 bg-slate-50 px-4 py-3 font-bold text-slate-900">
          <span>{{ t('pay.receipt.amountPaid') }}</span>
          <span data-testid="receipt-amount">{{ fmt(checkout.paid_amount) }} kr</span>
        </div>
      </div>
    </div>

    <div v-else-if="state === 'failed'" class="space-y-4 py-12 text-center" data-testid="receipt-failed">
      <XCircle class="mx-auto h-14 w-14 text-red-500" />
      <h1 class="text-2xl font-bold text-slate-900">{{ t('pay.receipt.failedTitle') }}</h1>
      <p class="text-slate-500">{{ t('pay.receipt.failedBody') }}</p>
      <RouterLink
        :to="`/betal/${token}`"
        class="inline-flex items-center justify-center rounded-md bg-orange-500 px-6 py-3 text-sm font-semibold text-white shadow-sm hover:bg-orange-600"
        data-testid="receipt-retry"
      >
        {{ t('pay.receipt.tryAgain') }}
      </RouterLink>
    </div>

    <div v-else-if="state === 'confirming'" class="space-y-3 py-16 text-center" data-testid="receipt-confirming">
      <Clock class="mx-auto h-12 w-12 text-slate-400" />
      <h1 class="text-xl font-semibold text-slate-900">{{ t('pay.receipt.confirmingTitle') }}</h1>
      <p class="text-slate-500">{{ t('pay.receipt.confirmingBody') }}</p>
    </div>

    <div v-else class="space-y-4 py-16 text-center" data-testid="receipt-pending">
      <Loader2 class="mx-auto h-10 w-10 animate-spin text-slate-400" />
      <h1 class="text-xl font-semibold text-slate-900">{{ t('pay.receipt.pendingTitle') }}</h1>
      <p class="text-slate-500">{{ t('pay.receipt.pendingBody') }}</p>
    </div>
  </div>
</template>
