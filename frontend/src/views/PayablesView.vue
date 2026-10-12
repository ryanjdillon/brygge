<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Loader2, CreditCard } from 'lucide-vue-next'
import InvoiceList from '@/components/portal/InvoiceList.vue'
import type { MemberInvoice } from '@/composables/useMyInvoices'

// Public per-member payables page (BRY-275): a durable personal link lists the
// member's open fakturas with no login. Reuses the portal InvoiceList row
// presentation; the "Pay" action hands off to the existing per-invoice redirect
// (/api/v1/betal/{token}), which mints a fresh Vipps payment on click.
const { t } = useI18n()
const route = useRoute()
const token = route.params.token as string

type PayableInvoice = MemberInvoice & { pay_token: string }
type Payables = {
  club_name: string
  member_name: string
  bank_account: string
  invoices: PayableInvoice[]
}

const state = ref<'loading' | 'ready' | 'error'>('loading')
const data = ref<Payables | null>(null)

async function load() {
  try {
    const res = await fetch(`/api/v1/betal/bruker/${token}`)
    if (!res.ok) throw new Error(String(res.status))
    data.value = await res.json()
    state.value = 'ready'
  } catch {
    state.value = 'error'
  }
}

function payTokenFor(id: string): string {
  return data.value?.invoices.find((i) => i.id === id)?.pay_token ?? ''
}

function pay(id: string) {
  const tok = payTokenFor(id)
  if (tok) window.location.href = `/api/v1/betal/${tok}`
}

// True when any listed invoice is over the club's Vipps cap (no pay token) and
// must be paid by bank transfer (BRY-283) — gates the konto hint below the list.
const anyBankOnly = computed(() => (data.value?.invoices ?? []).some((i) => !i.pay_token))

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
      <p class="text-slate-500">{{ t('portal.payables.notFound') }}</p>
    </div>

    <div v-else-if="data" class="space-y-6">
      <div class="space-y-1">
        <h1 class="text-2xl font-bold text-slate-900">{{ t('portal.payables.title') }}</h1>
        <p class="text-sm text-slate-500">
          {{ t('portal.payables.intro', { name: data.member_name }) }}
          <span v-if="data.club_name"> · {{ data.club_name }}</span>
        </p>
      </div>

      <div class="rounded-lg border border-slate-200 bg-white px-4">
        <InvoiceList :invoices="data.invoices" :empty-text="t('portal.payables.none')">
          <template #actions="{ invoice }">
            <button
              v-if="payTokenFor(invoice.id)"
              class="inline-flex items-center gap-1.5 rounded-md bg-orange-500 px-3 py-1.5 text-xs font-semibold text-white shadow-sm hover:bg-orange-600"
              @click="pay(invoice.id)"
            >
              <CreditCard class="h-3.5 w-3.5" />
              {{ t('portal.payables.pay') }}
            </button>
            <span v-else class="text-xs font-medium text-slate-500">
              {{ t('portal.payables.bankOnly') }}
            </span>
          </template>
        </InvoiceList>
      </div>

      <p v-if="anyBankOnly && data.bank_account" class="text-xs text-slate-500">
        {{ t('portal.payables.bankTransferHint', { account: data.bank_account }) }}
      </p>
      <p class="text-xs text-slate-500">{{ t('pay.preferBankNote') }}</p>
    </div>
  </div>
</template>
