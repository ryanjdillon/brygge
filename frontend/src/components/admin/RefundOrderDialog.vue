<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ExternalLink } from 'lucide-vue-next'
import Modal from '@/components/ui/Modal.vue'
import { formatNOK, formatDate, formatDateTime } from '@/lib/format'
import { useToast } from '@/composables/useToast'
import {
  MAX_REFUND_REASON,
  isFullRefund,
  newRequestKey,
  offersOutcome,
  parseKr,
  preselectAttempt,
  refundErrorCode,
  useOrderRefunds,
  useRefundOrder,
  validateRefund,
  type RefundOutcome,
} from '@/composables/useOrderRefund'

// Refund a card payment of an order from the payment overview or the faktura
// list. The caller asks for the TOTP step-up before opening it (the server
// gates the refund behind RequireFreshTOTP), see RefundOrderButton.
//
// The request key is made when the dialog opens and sent on every attempt,
// so a retry after a lost answer is the same request to the server and never
// refunds twice. A refund Stripe refused is dead under its key, so that one
// case makes a new key. Shared component by design: both entry points open
// the same dialog; nothing in the Stripe transaction dialogs fits (they act
// on a balance row, this acts on an order's payment).
const props = defineProps<{ open: boolean; orderId: string }>()
const emit = defineEmits<{ (e: 'update:open', value: boolean): void; (e: 'refunded'): void }>()

const { t, locale } = useI18n()
const toast = useToast()
const refund = useRefundOrder()

const orderIdRef = computed(() => props.orderId)
const enabled = computed(() => props.open)
const { data, isLoading, isError, isFetching } = useOrderRefunds(orderIdRef, enabled)

const requestKey = ref('')
const attemptId = ref('')
const amountText = ref('')
const reason = ref('')
const outcome = ref<RefundOutcome>('credit')
const submitted = ref(false)
const errorText = ref('')
const prepared = ref(false)

const attempts = computed(() => (data.value?.attempts ?? []).filter((a) => a.can_refund))
const selected = computed(() => attempts.value.find((a) => a.id === attemptId.value))
const refundable = computed(() => selected.value?.refundable ?? 0)
const full = computed(() => isFullRefund(amountText.value, refundable.value))
const showOutcome = computed(() => offersOutcome(!!data.value?.has_fakturas, selected.value, full.value))
const showRecollect = computed(() => showOutcome.value && !!selected.value?.recollect_allowed)
const history = computed(() => data.value?.refunds ?? [])
const disputed = computed(() => (data.value?.attempts ?? []).some((a) => a.disputed))
const problem = computed(() => (submitted.value ? validateRefund(amountText.value, reason.value, refundable.value) : null))
const recollectChosen = computed(() => showRecollect.value && outcome.value === 'recollect')

function money(amount: number) {
  return formatNOK(amount, { locale: locale.value })
}

function attemptLabel(a: { refundable: number; amount: number; created_at: string; duplicate: boolean }) {
  const label = t('admin.refund.attemptOption', { amount: money(a.amount), date: formatDate(a.created_at, locale.value) })
  return a.duplicate ? `${label} (${t('admin.refund.duplicate')})` : label
}

function selectAttempt(id: string) {
  attemptId.value = id
  const a = attempts.value.find((x) => x.id === id)
  amountText.value = a ? a.refundable.toFixed(2) : ''
  outcome.value = 'credit'
}

// A fresh dialog gets a fresh request key and an empty form; the payment
// preselected and the amount prefilled come once the order has been read.
watch(
  () => props.open,
  (open) => {
    if (!open) return
    requestKey.value = newRequestKey()
    attemptId.value = ''
    amountText.value = ''
    reason.value = ''
    outcome.value = 'credit'
    submitted.value = false
    errorText.value = ''
    prepared.value = false
  },
  { immediate: true },
)
watch(
  [() => props.open, data, isFetching],
  () => {
    if (!props.open || prepared.value || isFetching.value || !data.value) return
    prepared.value = true
    const first = preselectAttempt(attempts.value)
    if (first) selectAttempt(first.id)
  },
  { immediate: true },
)
// Collecting again stops being offered when the amount no longer is the whole payment.
watch(showRecollect, (ok) => {
  if (!ok) outcome.value = 'credit'
})

function close() {
  emit('update:open', false)
}

async function confirm() {
  submitted.value = true
  errorText.value = ''
  const amount = parseKr(amountText.value)
  if (!selected.value || amount === null || validateRefund(amountText.value, reason.value, refundable.value)) return
  try {
    const res = await refund.mutateAsync({
      orderId: props.orderId,
      attemptId: selected.value.id,
      amount,
      reason: reason.value,
      outcome: showOutcome.value ? outcome.value : undefined,
      requestKey: requestKey.value,
    })
    if (res.awaiting_confirmation) toast.info(t('admin.refund.toast.awaiting'))
    else toast.success(t('admin.refund.toast.done'))
    emit('refunded')
    close()
  } catch (e) {
    const code = refundErrorCode(e)
    errorText.value = t(`admin.refund.errors.${code}`)
    // A refund Stripe refused is a spent request; a lost answer is not.
    if (code === 'refund_failed') requestKey.value = newRequestKey()
  }
}

function problemText() {
  const p = problem.value
  if (!p) return ''
  if (p.key === 'errors.amountTooHigh') return t(`admin.refund.${p.key}`, { max: money(p.max) })
  if (p.key === 'errors.reasonTooLong') return t(`admin.refund.${p.key}`, { max: p.max })
  return t(`admin.refund.${p.key}`)
}
const amountProblem = computed(() => (problem.value?.key.startsWith('errors.amount') ? problemText() : ''))
const reasonProblem = computed(() => (problem.value?.key.startsWith('errors.reason') ? problemText() : ''))

function statusLabel(status: string) {
  return t(`admin.refund.status.${status}`)
}
</script>

<template>
  <Modal
    :open="open"
    size="lg"
    full-screen-on-mobile
    :title="t('admin.refund.title')"
    @update:open="(v: boolean) => emit('update:open', v)"
  >
    <div data-testid="refund-dialog">
      <p v-if="isLoading" class="text-sm text-gray-500" data-testid="refund-loading">{{ t('admin.refund.loading') }}</p>
      <p v-else-if="isError || !data" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-800" role="alert" data-testid="refund-load-error">
        {{ t('admin.refund.loadFailed') }}
      </p>

      <template v-else>
        <p
          v-if="attempts.length === 0"
          class="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900"
          role="note"
          data-testid="refund-nothing"
        >
          {{ disputed ? t('admin.refund.errors.disputed') : t('admin.refund.nothingToRefund') }}
        </p>

        <form v-else class="space-y-4" novalidate @submit.prevent="confirm">
          <div v-if="attempts.length > 1">
            <label class="block text-sm font-medium text-gray-700" for="refund-attempt">{{ t('admin.refund.attempt') }}</label>
            <select
              id="refund-attempt"
              :value="attemptId"
              class="mt-1 block min-h-11 w-full rounded-md border border-gray-300 bg-white px-3 text-sm"
              data-testid="refund-attempt"
              @change="selectAttempt(($event.target as HTMLSelectElement).value)"
            >
              <option v-for="a in attempts" :key="a.id" :value="a.id">{{ attemptLabel(a) }}</option>
            </select>
          </div>
          <p v-else-if="selected" class="text-sm text-gray-700" data-testid="refund-attempt-label">
            {{ attemptLabel(selected) }}
          </p>

          <p v-if="selected" class="text-sm text-gray-600" data-testid="refund-refundable">
            {{ t('admin.refund.refundable', { refundable: money(selected.refundable), amount: money(selected.amount) }) }}
          </p>

          <div>
            <label class="block text-sm font-medium text-gray-700" for="refund-amount">{{ t('admin.refund.amount') }}</label>
            <input
              id="refund-amount"
              v-model="amountText"
              type="text"
              inputmode="decimal"
              autocomplete="off"
              class="mt-1 block min-h-11 w-full rounded-md border border-gray-300 px-3 text-sm tabular-nums"
              :aria-invalid="!!amountProblem"
              data-testid="refund-amount"
            />
            <p class="mt-1 text-xs text-gray-500">{{ t('admin.refund.amountHelp') }}</p>
            <p v-if="amountProblem" class="mt-1 text-sm text-red-700" role="alert" data-testid="refund-amount-error">{{ amountProblem }}</p>
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700" for="refund-reason">{{ t('admin.refund.reason') }}</label>
            <textarea
              id="refund-reason"
              v-model="reason"
              rows="2"
              :maxlength="MAX_REFUND_REASON"
              class="mt-1 block min-h-11 w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
              :aria-invalid="!!reasonProblem"
              data-testid="refund-reason"
            />
            <p class="mt-1 text-xs text-gray-500">{{ t('admin.refund.reasonHelp') }}</p>
            <p v-if="reasonProblem" class="mt-1 text-sm text-red-700" role="alert" data-testid="refund-reason-error">{{ reasonProblem }}</p>
          </div>

          <fieldset v-if="showRecollect" class="space-y-2" data-testid="refund-outcome">
            <legend class="text-sm font-medium text-gray-700">{{ t('admin.refund.outcome') }}</legend>
            <label class="flex min-h-11 cursor-pointer items-start gap-3 rounded-md border border-gray-200 p-3">
              <input v-model="outcome" type="radio" name="refund-outcome" value="credit" class="mt-1 h-4 w-4" data-testid="refund-outcome-credit" />
              <span class="text-sm">
                <span class="block font-medium text-gray-900">{{ t('admin.refund.outcomeCredit') }}</span>
                <span class="block text-gray-600">{{ t('admin.refund.outcomeCreditHelp') }}</span>
              </span>
            </label>
            <label class="flex min-h-11 cursor-pointer items-start gap-3 rounded-md border border-gray-200 p-3">
              <input v-model="outcome" type="radio" name="refund-outcome" value="recollect" class="mt-1 h-4 w-4" data-testid="refund-outcome-recollect" />
              <span class="text-sm">
                <span class="block font-medium text-gray-900">{{ t('admin.refund.outcomeRecollect') }}</span>
                <span class="block text-gray-600">{{ t('admin.refund.outcomeRecollectHelp') }}</span>
              </span>
            </label>
          </fieldset>

          <ul class="list-disc space-y-1 pl-5 text-sm text-gray-600" data-testid="refund-explain">
            <li>{{ t('admin.refund.explainMoney') }}</li>
            <li v-if="!recollectChosen && !selected?.duplicate">{{ t('admin.refund.explainCredit') }}</li>
            <li>{{ t('admin.refund.explainBooked') }}</li>
          </ul>

          <a
            v-if="selected?.payment_url"
            :href="selected.payment_url"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-h-11 items-center gap-1.5 text-sm font-medium text-brand-700 underline"
            data-testid="refund-dashboard-link"
          >
            <ExternalLink class="h-4 w-4" />
            {{ t('admin.refund.dashboard') }}
          </a>

          <section v-if="history.length" data-testid="refund-history">
            <h3 class="text-sm font-medium text-gray-700">{{ t('admin.refund.history') }}</h3>
            <ul class="mt-1 divide-y divide-gray-100 text-sm">
              <li v-for="r in history" :key="r.id" class="flex flex-wrap items-baseline justify-between gap-x-3 py-1.5">
                <span class="tabular-nums">{{ money(r.amount) }} · {{ statusLabel(r.status) }}</span>
                <span class="text-xs text-gray-500">
                  {{ r.created_by ? r.reason : t('admin.refund.historyDashboard') }} · {{ formatDateTime(r.created_at, locale) }}
                </span>
              </li>
            </ul>
          </section>

          <p v-if="errorText" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-800" role="alert" data-testid="refund-error">
            {{ errorText }}
          </p>

          <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <button
              type="button"
              class="min-h-11 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50"
              @click="close"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              class="min-h-11 rounded-md bg-red-600 px-4 py-2 text-sm font-semibold text-white hover:bg-red-700 disabled:opacity-50"
              :disabled="!selected || refund.isPending.value"
              data-testid="refund-confirm"
            >
              {{
                refund.isPending.value
                  ? t('admin.refund.submitting')
                  : t('admin.refund.submit', { amount: money(parseKr(amountText) ?? 0) })
              }}
            </button>
          </div>
        </form>
      </template>
    </div>
  </Modal>
</template>
