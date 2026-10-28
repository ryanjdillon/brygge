<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Loader2, CheckCircle } from 'lucide-vue-next'
import PayMethodButtons from '@/components/pay/PayMethodButtons.vue'

const { t } = useI18n()
const route = useRoute()
const token = route.params.token as string

type Line = { id?: string; name: string; quantity: number; unit_amount: number; total: number }
type Checkout = {
  status: string
  currency: string
  invoice: { number: number; amount: number; lines: Line[] } | null
  lines: Line[]
  total: number
  already_paid: boolean
  // Not payable online (over the club's cap, BRY-283): no pay buttons; pay by
  // bank transfer using the KID + konto shown instead.
  online_blocked: boolean
  kid_number: string
  bank_account: string
  // Configured payment providers in display order; empty when blocked.
  providers: string[]
}

const state = ref<'loading' | 'ready' | 'error'>('loading')
const checkout = ref<Checkout | null>(null)
const error = ref('')
const busy = ref(false)

function fmt(n: number): string {
  return n.toLocaleString('nb-NO', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

async function load() {
  try {
    const res = await fetch(`/api/v1/checkout/${token}`)
    if (!res.ok) throw new Error(String(res.status))
    checkout.value = await res.json()
    state.value = 'ready'
  } catch {
    state.value = 'error'
  }
}

async function pay(provider: string) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const res = await fetch(`/api/v1/checkout/${token}/pay`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ provider }),
    })
    if (res.status === 409) {
      const body = await res.json().catch(() => null)
      if (body?.error === 'online_payment_over_limit' || body?.error === 'amount_below_minimum') {
        // The order stopped being payable online, or by the chosen provider,
        // since the page loaded: reload so the server's provider list (or the
        // bank-transfer details) replaces the buttons.
        busy.value = false
        await load()
        // The provider's own minimum can be above the one the server lists
        // providers by, so the reload may offer the same button again.
        if (body.error === 'amount_below_minimum' && checkout.value?.providers?.includes(provider)) {
          error.value = t('pay.belowMinimum')
        }
        return
      }
    }
    if (!res.ok) throw new Error(String(res.status))
    const data = await res.json()
    if (data.redirect_url) {
      window.location.href = data.redirect_url
      return
    }
    throw new Error('no redirect')
  } catch {
    error.value = t('pay.payError')
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-12 sm:px-6">
    <div v-if="state === 'loading'" class="space-y-4 py-16 text-center">
      <Loader2 class="mx-auto h-10 w-10 animate-spin text-slate-400" />
      <p class="text-slate-500">{{ t('pay.loading') }}</p>
    </div>

    <div v-else-if="state === 'error'" class="space-y-3 py-16 text-center">
      <h1 class="text-xl font-semibold text-slate-900">{{ t('pay.notFoundTitle') }}</h1>
      <p class="text-slate-500">{{ t('pay.notFoundBody') }}</p>
    </div>

    <div v-else-if="checkout" class="space-y-6">
      <div v-if="checkout.already_paid" class="space-y-3 py-12 text-center">
        <CheckCircle class="mx-auto h-14 w-14 text-green-600" />
        <h1 class="text-2xl font-bold text-slate-900">{{ t('pay.alreadyPaidTitle') }}</h1>
        <p class="text-slate-500">{{ t('pay.alreadyPaidBody') }}</p>
      </div>

      <template v-else>
        <h1 class="text-2xl font-bold text-slate-900">
          <template v-if="checkout.invoice">{{ t('pay.title', { n: checkout.invoice.number }) }}</template>
          <template v-else>{{ t('pay.titleGeneric') }}</template>
        </h1>

        <!-- Faktura (read-only) -->
        <div v-if="checkout.invoice" class="rounded-lg border border-slate-200 bg-white">
          <div class="border-b border-slate-100 px-4 py-2 text-xs font-medium uppercase tracking-wide text-slate-500">
            {{ t('pay.invoiceSection', { n: checkout.invoice.number }) }}
          </div>
          <div
            v-for="(line, i) in checkout.invoice.lines"
            :key="'inv' + i"
            class="flex justify-between px-4 py-2 text-sm"
          >
            <span class="text-slate-700">{{ line.name }}<span v-if="line.quantity > 1" class="text-slate-400"> × {{ line.quantity }}</span></span>
            <span class="font-medium text-slate-900">{{ fmt(line.total) }} kr</span>
          </div>
        </div>

        <!-- Order lines (services, overnatting) — read-only -->
        <div v-if="checkout.lines.length" class="rounded-lg border border-slate-200 bg-white">
          <div class="border-b border-slate-100 px-4 py-2 text-xs font-medium uppercase tracking-wide text-slate-500">
            {{ t('pay.itemsSection') }}
          </div>
          <div
            v-for="line in checkout.lines"
            :key="line.id"
            class="flex items-center justify-between px-4 py-2 text-sm"
          >
            <span class="text-slate-700">{{ line.name }}<span v-if="line.quantity > 1" class="text-slate-400"> × {{ line.quantity }}</span></span>
            <span class="font-medium text-slate-900">{{ fmt(line.total) }} kr</span>
          </div>
        </div>

        <!-- Total -->
        <div class="rounded-lg border border-slate-200 bg-slate-50 p-4">
          <div class="flex items-center justify-between text-lg font-bold text-slate-900">
            <span>{{ t('pay.total') }}</span>
            <span>{{ fmt(checkout.total) }} kr</span>
          </div>
        </div>

        <p class="text-xs text-slate-500">{{ t('pay.preferBankNote') }}</p>

        <!-- Not payable online, or no provider configured → bank transfer only. -->
        <div
          v-if="checkout.online_blocked || !checkout.providers?.length"
          class="space-y-2 rounded-lg border border-slate-200 bg-slate-50 p-4 text-sm"
          data-testid="bank-only"
        >
          <p class="font-semibold text-slate-900">{{ t('pay.bankOnlyTitle') }}</p>
          <p class="text-slate-600">
            {{ checkout.online_blocked ? t('pay.bankOnlyBody') : t('pay.bankOnlyBodyNoProvider') }}
          </p>
          <dl class="space-y-1 pt-1">
            <div v-if="checkout.bank_account" class="flex justify-between">
              <dt class="text-slate-500">{{ t('pay.account') }}</dt>
              <dd class="font-medium text-slate-900">{{ checkout.bank_account }}</dd>
            </div>
            <div v-if="checkout.kid_number" class="flex justify-between">
              <dt class="text-slate-500">KID</dt>
              <dd class="font-medium text-slate-900">{{ checkout.kid_number }}</dd>
            </div>
          </dl>
        </div>

        <template v-else>
          <div role="alert">
            <div v-if="error" class="rounded-md bg-red-50 p-3 text-sm text-red-800">{{ error }}</div>
          </div>

          <PayMethodButtons
            :providers="checkout.providers"
            :busy="busy"
            :disabled="checkout.total <= 0"
            @pay="pay"
          />
        </template>
      </template>
    </div>
  </div>
</template>
