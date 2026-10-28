<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDate } from '@/lib/format'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { useToast } from '@/composables/useToast'
import {
  useStripeTxnMutations,
  stripeTxnErrorCode,
  formatStripeAmount,
  type StripeTxnAction,
  type StripeTxnItem,
} from '@/composables/useStripeTransactions'
import StripeTxnInvoiceDialog from '@/components/admin/StripeTxnInvoiceDialog.vue'
import StripeTxnAccountDialog from '@/components/admin/StripeTxnAccountDialog.vue'
import StripeTxnDismissDialog from '@/components/admin/StripeTxnDismissDialog.vue'
import StripeTxnUnassignDialog from '@/components/admin/StripeTxnUnassignDialog.vue'

// One Stripe balance transaction in review, dismissed or booked, with the
// actions the server allows for it (allowed_actions). Every action is
// TOTP-gated on the server, so the step-up is asked for at click time,
// before a dialog opens.
const props = defineProps<{ item: StripeTxnItem }>()

const { t, te, locale } = useI18n()
const { ensureFreshTotp } = useFreshTotp()
const toast = useToast()
const { undismiss, rebook } = useStripeTxnMutations()

const ACTION_ORDER: StripeTxnAction[] = ['rebook', 'assign_invoice', 'assign_account', 'dismiss', 'undismiss', 'unassign']
// Actions that run at once rather than through a dialog.
const DIRECT: StripeTxnAction[] = ['undismiss', 'rebook']
const SECONDARY: StripeTxnAction[] = ['dismiss', 'unassign']
const actions = computed(() => ACTION_ORDER.filter((a) => props.item.allowed_actions.includes(a)))

const money = (amount: number) => formatStripeAmount(amount, props.item.currency)

function reasonLabel(reason: string) {
  const key = `admin.bankImports.stripeReviewReasons.${reason}`
  return te(key) ? t(key) : reason
}

function dismissReasonLabel(reason: string) {
  const key = `admin.bankImports.stripeTxn.dismissReasons.${reason}`
  return te(key) ? t(key) : reason
}

const shortId = computed(() => {
  const id = props.item.balance_transaction_id
  return id.length > 18 ? `${id.slice(0, 10)}…${id.slice(-6)}` : id
})
const copied = ref(false)
async function copyId() {
  try {
    await navigator.clipboard.writeText(props.item.balance_transaction_id)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  } catch {
    // Without clipboard access the full id stays in the title attribute.
  }
}

const dialog = ref<'invoice' | 'account' | 'dismiss' | 'unassign' | null>(null)
const errorText = ref('')

const invoiceNumbers = computed(() => (props.item.invoice_numbers ?? []).join(', '))

function showError(e: unknown) {
  errorText.value = t(`admin.bankImports.stripeTxn.errors.${stripeTxnErrorCode(e) ?? 'generic'}`)
}

// The booking pass ran on the row at once; say what it did.
async function runRebook() {
  try {
    const res = await rebook.mutateAsync({ id: props.item.id })
    if (res.outcome === 'booked') {
      toast.success(t('admin.bankImports.stripeTxn.rebook.booked'))
    } else if (res.outcome === 'needs_review') {
      toast.info(t('admin.bankImports.stripeTxn.rebook.needsReview', { reason: reasonLabel(res.reason ?? '') }))
    } else if (res.outcome === 'deferred') {
      toast.info(t('admin.bankImports.stripeTxn.rebook.deferred'))
    } else {
      toast.error(t('admin.bankImports.stripeTxn.rebook.failed'))
    }
  } catch (e) {
    showError(e)
  }
}

async function run(action: StripeTxnAction) {
  errorText.value = ''
  if (!(await ensureFreshTotp())) return
  switch (action) {
    case 'assign_invoice':
      dialog.value = 'invoice'
      return
    case 'assign_account':
      dialog.value = 'account'
      return
    case 'dismiss':
      dialog.value = 'dismiss'
      return
    case 'unassign':
      dialog.value = 'unassign'
      return
    case 'rebook':
      await runRebook()
      return
    case 'undismiss':
      try {
        await undismiss.mutateAsync({ id: props.item.id })
        toast.success(t('admin.bankImports.stripeTxn.undismissSuccess'))
      } catch (e) {
        showError(e)
      }
  }
}

function pending(a: StripeTxnAction) {
  if (a === 'undismiss') return undismiss.isPending.value
  if (a === 'rebook') return rebook.isPending.value
  return false
}

function closeDialog(open: boolean) {
  if (!open) dialog.value = null
}
</script>

<template>
  <article class="rounded-md border border-gray-200 bg-white p-3 text-sm" :data-testid="`stripe-txn-${item.id}`">
    <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
      <span class="text-base font-semibold tabular-nums">{{ money(item.amount) }}</span>
      <span class="text-gray-600">{{ formatDate(item.date, locale) }}</span>
    </div>
    <p class="mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-xs text-gray-600">
      <span>{{ t('admin.bankImports.stripeTxn.fee', { amount: money(item.fee) }) }}</span>
      <span>{{ t('admin.bankImports.stripeTxn.net', { amount: money(item.net) }) }}</span>
      <span>{{ t('admin.bankImports.stripeTxn.type', { type: item.stripe_type || item.reporting_category }) }}</span>
    </p>
    <p v-if="item.review_reason" class="mt-2 inline-block rounded bg-yellow-50 px-2 py-0.5 text-xs text-yellow-900" data-testid="stripe-txn-reason">
      {{ reasonLabel(item.review_reason) }}
    </p>
    <p v-if="item.dismissed_at" class="mt-2 text-xs text-gray-700" data-testid="stripe-txn-dismissed">
      {{ t('admin.bankImports.stripeTxn.dismissedOn', { date: formatDate(item.dismissed_at, locale) }) }}
      <template v-if="item.dismissed_reason">
        · {{ dismissReasonLabel(item.dismissed_reason) }}
      </template>
      <template v-if="item.dismissed_note"> · {{ item.dismissed_note }}</template>
    </p>
    <p v-if="item.status === 'booked' && item.journal_entry_number" class="mt-2 text-xs text-gray-700" data-testid="stripe-txn-entry">
      {{ t('admin.bankImports.stripeTxn.entry', { number: item.journal_entry_number, description: item.journal_entry_description ?? '' }) }}
      <template v-if="item.journal_entry_status === 'voided'"> · {{ t('admin.bankImports.stripeTxn.entryVoided') }}</template>
    </p>
    <p v-if="invoiceNumbers" class="mt-1 text-xs text-gray-700" data-testid="stripe-txn-invoices">
      {{ t('admin.bankImports.stripeTxn.invoices', { invoices: invoiceNumbers }) }}
    </p>
    <p v-if="item.payout_cleared" class="mt-1 text-xs text-gray-500" data-testid="stripe-txn-payout-cleared">
      {{ t('admin.bankImports.stripeTxn.payoutCleared') }}
    </p>
    <div class="mt-2 flex min-w-0 items-center gap-2 text-xs text-gray-500">
      <code class="min-w-0 truncate font-mono" :title="item.balance_transaction_id">{{ shortId }}</code>
      <button
        type="button"
        class="min-h-11 shrink-0 rounded-md px-2 text-brand-700 hover:bg-gray-50"
        :aria-label="t('admin.bankImports.stripeTxn.copyId')"
        data-testid="stripe-txn-copy"
        @click="copyId"
      >
        {{ copied ? t('admin.bankImports.stripeTxn.copied') : t('admin.bankImports.stripeTxn.copyId') }}
      </button>
    </div>

    <div v-if="actions.length" class="mt-2 flex flex-col gap-2 sm:flex-row sm:flex-wrap">
      <button
        v-for="a in actions"
        :key="a"
        type="button"
        class="min-h-11 rounded-md border px-3 py-2 text-sm font-semibold disabled:opacity-50"
        :class="SECONDARY.includes(a) ? 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50' : 'border-brand-600 bg-brand-600 text-white hover:bg-brand-700'"
        :disabled="DIRECT.includes(a) && pending(a)"
        :data-testid="`stripe-txn-action-${a}`"
        @click="run(a)"
      >
        {{ t(`admin.bankImports.stripeTxn.actions.${a}`) }}
      </button>
    </div>
    <p v-else-if="!item.payout_cleared" class="mt-2 text-xs text-gray-500" data-testid="stripe-txn-no-actions">
      {{ t('admin.bankImports.stripeTxn.noActions') }}
    </p>
    <p v-if="errorText" class="mt-2 rounded-md bg-red-50 px-3 py-2 text-sm text-red-800" role="alert" data-testid="stripe-txn-error">
      {{ errorText }}
    </p>

    <StripeTxnInvoiceDialog :open="dialog === 'invoice'" :item="item" @update:open="closeDialog" />
    <StripeTxnAccountDialog :open="dialog === 'account'" :item="item" @update:open="closeDialog" />
    <StripeTxnDismissDialog :open="dialog === 'dismiss'" :item="item" @update:open="closeDialog" />
    <StripeTxnUnassignDialog :open="dialog === 'unassign'" :item="item" @update:open="closeDialog" />
  </article>
</template>
