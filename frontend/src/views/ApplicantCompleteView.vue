<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'
import { UserPlus, Loader2, Clock, CheckCircle, Copy, Check } from 'lucide-vue-next'
import { useApiClient, unwrap, ApiError } from '@/lib/apiClient'
import { useAuthStore } from '@/stores/auth'
import { formatNOK, formatDate } from '@/lib/format'
import { openPayLink } from '@/lib/joinFlow'

// "Fullfør innmeldinga": where an applicant who confirmed but has not paid
// lands from every page they may not open yet. GET /join/me says what is
// left; opening the page never issues a faktura, only the bank button does.

const { t, locale } = useI18n()
const router = useRouter()
const client = useApiClient()
const auth = useAuthStore()

type BankInvoice = {
  invoice_id: string
  invoice_number: number
  kid_number: string
  account_number: string
  amount: number
  currency: string
  due_date: string
  emailed: boolean
}

const state = ref<'loading' | 'awaiting_payment' | 'bank_invoice' | 'member' | 'none' | 'error'>('loading')
const checkoutUrl = ref('')
const invoice = ref<BankInvoice | null>(null)
const issuing = ref(false)
const paymentInProgress = ref(false)
const bankError = ref(false)
const justEmailed = ref(false)
const copied = ref('')

async function load() {
  state.value = 'loading'
  try {
    const res = unwrap(await client.GET('/api/v1/join/me'))
    checkoutUrl.value = res.checkout_url ?? ''
    invoice.value = (res.bank_invoice as BankInvoice | undefined) ?? null
    if (res.state === 'member') {
      // Paid since this session loaded: reload it so the portal opens.
      await auth.checkSession()
    }
    state.value = res.state
  } catch {
    state.value = 'error'
  }
}

function payNow() {
  if (checkoutUrl.value) void openPayLink(router, checkoutUrl.value)
}

async function payByBank() {
  issuing.value = true
  paymentInProgress.value = false
  bankError.value = false
  try {
    const res = unwrap(await client.POST('/api/v1/join/bank-invoice'))
    invoice.value = res as BankInvoice
    justEmailed.value = res.emailed
    state.value = 'bank_invoice'
  } catch (err) {
    if (err instanceof ApiError && err.status === 409 && err.code === 'payment_in_progress') {
      paymentInProgress.value = true
    } else if (err instanceof ApiError && err.status === 409) {
      // Paid or ended meanwhile: show what the server says now.
      await load()
    } else {
      bankError.value = true
    }
  } finally {
    issuing.value = false
  }
}

async function copy(key: string, value: string) {
  try {
    await navigator.clipboard.writeText(value)
    copied.value = key
    setTimeout(() => {
      if (copied.value === key) copied.value = ''
    }, 2000)
  } catch {
    // Clipboard blocked: the value stays on screen to copy by hand.
  }
}

// The amount as a Norwegian online bank's payment form takes it: decimal
// comma, øre always shown, no thousands separator (650,00).
function bankAmount(n: number): string {
  return n.toFixed(2).replace('.', ',')
}

// Norwegian account numbers read as 1234.56.78903.
function formatAccount(n: string): string {
  return /^\d{11}$/.test(n) ? `${n.slice(0, 4)}.${n.slice(4, 6)}.${n.slice(6)}` : n
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-2xl">
    <div class="rounded-lg border border-gray-200 bg-white p-4 sm:p-8">
      <div v-if="state === 'loading'" class="py-12 text-center" role="status">
        <Loader2 class="mx-auto h-8 w-8 animate-spin text-gray-400" aria-hidden="true" />
        <span class="sr-only">{{ t('common.loading') }}</span>
      </div>

      <div v-else-if="state === 'member'" class="space-y-3 py-4 text-center" data-testid="complete-member">
        <CheckCircle class="mx-auto h-12 w-12 text-green-600" aria-hidden="true" />
        <h1 class="text-2xl font-bold text-gray-900">{{ t('join.complete.welcomeTitle') }}</h1>
        <p class="text-gray-700">{{ t('join.complete.welcomeBody') }}</p>
        <RouterLink
          to="/portal"
          class="inline-flex min-h-11 w-full items-center justify-center rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 sm:w-auto"
          data-testid="complete-portal"
        >
          {{ t('join.complete.toPortal') }}
        </RouterLink>
      </div>

      <template v-else>
        <div class="flex items-center gap-3">
          <UserPlus class="h-6 w-6 flex-none text-brand-600" aria-hidden="true" />
          <h1 class="text-xl font-bold text-gray-900 sm:text-2xl">{{ t('join.complete.title') }}</h1>
        </div>

        <div v-if="state === 'error'" class="mt-4 rounded-md bg-red-50 p-4 text-sm text-red-800" role="alert" data-testid="complete-error">
          {{ t('join.loadFailed') }}
          <button type="button" class="ml-1 min-h-11 font-semibold underline" @click="load">{{ t('join.retry') }}</button>
        </div>

        <div v-else-if="state === 'none'" class="mt-4 space-y-3" data-testid="complete-none">
          <p class="text-gray-700">{{ t('join.complete.noneBody') }}</p>
          <RouterLink
            to="/contact"
            class="inline-flex min-h-11 w-full items-center justify-center rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 sm:w-auto"
          >
            {{ t('join.complete.contact') }}
          </RouterLink>
        </div>

        <div v-else-if="state === 'awaiting_payment'" class="mt-4 space-y-4" data-testid="complete-awaiting">
          <p class="text-gray-700">{{ t('join.complete.awaitingBody') }}</p>
          <div class="flex flex-col gap-3 sm:flex-row">
            <button
              type="button"
              class="inline-flex min-h-11 items-center justify-center rounded-md bg-brand-600 px-6 py-2 text-base font-semibold text-white hover:bg-brand-700 disabled:opacity-60"
              :disabled="!checkoutUrl"
              data-testid="complete-pay-now"
              @click="payNow"
            >
              {{ t('join.complete.payNow') }}
            </button>
            <button
              type="button"
              class="inline-flex min-h-11 items-center justify-center gap-2 rounded-md border border-gray-300 px-6 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60"
              :disabled="issuing"
              data-testid="complete-pay-bank"
              @click="payByBank"
            >
              <Loader2 v-if="issuing" class="h-4 w-4 animate-spin" aria-hidden="true" />
              {{ t('join.complete.payByBank') }}
            </button>
          </div>
          <p class="text-xs text-gray-500">{{ t('join.complete.payByBankHelp') }}</p>
          <p v-if="paymentInProgress" class="rounded-md bg-amber-50 p-3 text-sm text-amber-900" role="alert" data-testid="complete-in-progress">
            {{ t('join.complete.paymentInProgress') }}
          </p>
          <p v-if="bankError" class="rounded-md bg-red-50 p-3 text-sm text-red-800" role="alert" data-testid="complete-bank-error">
            {{ t('join.complete.bankFailed') }}
          </p>
        </div>

        <div v-else-if="state === 'bank_invoice' && invoice" class="mt-4 space-y-4" data-testid="complete-bank-invoice">
          <p class="flex items-center gap-2 font-medium text-gray-900">
            <Clock class="h-5 w-5 flex-none text-amber-500" aria-hidden="true" />
            {{ t('join.complete.waitingForPayment') }}
          </p>
          <p class="text-gray-700">{{ t('join.complete.bankBody') }}</p>
          <dl class="divide-y divide-gray-100 rounded-md border border-gray-200">
            <div
              v-for="row in [
                { key: 'kid', label: t('join.complete.kid'), shown: invoice.kid_number, value: invoice.kid_number },
                { key: 'account', label: t('join.complete.account'), shown: formatAccount(invoice.account_number), value: invoice.account_number },
                { key: 'amount', label: t('join.complete.amount'), shown: formatNOK(invoice.amount), value: bankAmount(invoice.amount) },
              ]"
              :key="row.key"
              class="flex items-center justify-between gap-3 px-4 py-2"
            >
              <div class="min-w-0">
                <dt class="text-xs text-gray-500">{{ row.label }}</dt>
                <dd class="break-all font-mono text-base font-semibold text-gray-900" :data-testid="`complete-${row.key}`">{{ row.shown || '—' }}</dd>
              </div>
              <button
                v-if="row.value"
                type="button"
                class="inline-flex min-h-11 min-w-11 flex-none items-center justify-center gap-1 rounded-md border border-gray-300 px-3 text-sm text-gray-700 hover:bg-gray-50"
                :aria-label="t('join.complete.copy', { field: row.label })"
                :data-testid="`complete-copy-${row.key}`"
                @click="copy(row.key, row.value)"
              >
                <Check v-if="copied === row.key" class="h-4 w-4 text-green-600" aria-hidden="true" />
                <Copy v-else class="h-4 w-4" aria-hidden="true" />
                <span class="hidden sm:inline">{{ copied === row.key ? t('join.complete.copied') : t('join.complete.copyShort') }}</span>
              </button>
            </div>
            <div class="px-4 py-2">
              <dt class="text-xs text-gray-500">{{ t('join.complete.dueDate') }}</dt>
              <dd class="text-base font-semibold text-gray-900" data-testid="complete-due">{{ formatDate(invoice.due_date, locale) }}</dd>
            </div>
          </dl>
          <p v-if="justEmailed" class="text-sm text-gray-600" data-testid="complete-emailed">{{ t('join.complete.emailed') }}</p>
          <p class="text-sm text-gray-600">{{ t('join.complete.bankNote') }}</p>
          <button
            v-if="checkoutUrl"
            type="button"
            class="inline-flex min-h-11 w-full items-center justify-center rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 sm:w-auto"
            data-testid="complete-pay-online-instead"
            @click="payNow"
          >
            {{ t('join.complete.payOnlineInstead') }}
          </button>
        </div>

        <div class="mt-6 flex flex-col gap-2 border-t border-gray-100 pt-4 text-sm sm:flex-row sm:gap-4">
          <RouterLink to="/portal/invoices" class="inline-flex min-h-11 items-center text-brand-700 underline">
            {{ t('join.complete.invoices') }}
          </RouterLink>
          <RouterLink to="/portal/profile" class="inline-flex min-h-11 items-center text-brand-700 underline">
            {{ t('join.complete.profile') }}
          </RouterLink>
        </div>
      </template>
    </div>
  </div>
</template>
