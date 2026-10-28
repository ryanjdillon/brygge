<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Modal from '@/components/ui/Modal.vue'
import { formatDate } from '@/lib/format'
import { useToast } from '@/composables/useToast'
import {
  useStripeTxnMutations,
  stripeTxnErrorCode,
  formatStripeAmount,
  STRIPE_DISMISS_REASONS,
  type StripeDismissReason,
  type StripeTxnItem,
} from '@/composables/useStripeTransactions'

// Sets a Stripe row aside without booking it. Nothing is posted, yet the
// payout that carried the row still takes its net off the Stripe clearing
// account, so the dialog says so before the person confirms. The caller
// asks for the step-up before opening it.
const props = defineProps<{ open: boolean; item: StripeTxnItem }>()
const emit = defineEmits<{ (e: 'update:open', value: boolean): void }>()

const { t, locale } = useI18n()
const toast = useToast()
const { dismiss } = useStripeTxnMutations()

const reason = ref<StripeDismissReason | ''>('')
const note = ref('')
const errorText = ref('')
watch(
  () => props.open,
  (open) => {
    if (!open) return
    reason.value = ''
    note.value = ''
    errorText.value = ''
  },
)

const isRefund = computed(() => props.item.review_reason === 'refund_pending_bry446')

function close() {
  emit('update:open', false)
}

async function confirm() {
  if (!reason.value) return
  errorText.value = ''
  try {
    await dismiss.mutateAsync({ id: props.item.id, reason: reason.value, note: note.value.trim() })
    toast.success(t('admin.bankImports.stripeTxn.dismissDialog.success'))
    close()
  } catch (e) {
    errorText.value = t(`admin.bankImports.stripeTxn.errors.${stripeTxnErrorCode(e) ?? 'generic'}`)
  }
}
</script>

<template>
  <Modal
    :open="open"
    size="lg"
    full-screen-on-mobile
    :title="t('admin.bankImports.stripeTxn.dismissDialog.title')"
    @update:open="(v: boolean) => emit('update:open', v)"
  >
    <form data-testid="stripe-txn-dismiss-dialog" @submit.prevent="confirm">
      <p class="text-sm text-gray-700">
        {{ t('admin.bankImports.stripeTxn.intro', { amount: formatStripeAmount(item.amount, item.currency), date: formatDate(item.date, locale) }) }}
      </p>
      <p class="mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900" role="note" data-testid="stripe-txn-dismiss-warning">
        {{ t('admin.bankImports.stripeTxn.dismissDialog.warning', { amount: formatStripeAmount(item.net, item.currency) }) }}
      </p>
      <p
        v-if="isRefund"
        class="mt-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900"
        role="note"
        data-testid="stripe-txn-dismiss-refund-warning"
      >
        {{ t('admin.bankImports.stripeTxn.dismissDialog.refundWarning') }}
      </p>

      <label class="mt-4 block text-sm font-medium text-gray-700" for="stripe-txn-dismiss-reason">
        {{ t('admin.bankImports.stripeTxn.dismissDialog.reason') }}
      </label>
      <select
        id="stripe-txn-dismiss-reason"
        v-model="reason"
        class="mt-1 block min-h-11 w-full rounded-md border border-gray-300 bg-white px-3 text-sm"
        data-testid="stripe-txn-dismiss-reason"
      >
        <option value="" disabled>{{ t('admin.bankImports.stripeTxn.dismissDialog.reasonPlaceholder') }}</option>
        <option v-for="r in STRIPE_DISMISS_REASONS" :key="r" :value="r">
          {{ t(`admin.bankImports.stripeTxn.dismissReasons.${r}`) }}
        </option>
      </select>

      <label class="mt-4 block text-sm font-medium text-gray-700" for="stripe-txn-dismiss-note">
        {{ t('admin.bankImports.stripeTxn.dismissDialog.note') }}
      </label>
      <input
        id="stripe-txn-dismiss-note"
        v-model="note"
        type="text"
        maxlength="500"
        class="mt-1 block min-h-11 w-full rounded-md border border-gray-300 px-3 text-sm"
        data-testid="stripe-txn-dismiss-note"
      />

      <p v-if="errorText" class="mt-3 rounded-md bg-red-50 px-3 py-2 text-sm text-red-800" role="alert" data-testid="stripe-txn-dialog-error">
        {{ errorText }}
      </p>

      <div class="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
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
          :disabled="!reason || dismiss.isPending.value"
          data-testid="stripe-txn-dismiss-confirm"
        >
          {{ t('admin.bankImports.stripeTxn.dismissDialog.confirm') }}
        </button>
      </div>
    </form>
  </Modal>
</template>
