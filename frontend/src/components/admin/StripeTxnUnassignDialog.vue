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
  type StripeTxnItem,
} from '@/composables/useStripeTransactions'

// Undoes a Stripe row's booking. The entry is reversed, the fakturas it
// settled reopen (keeping their paid-online marker), and the row waits in
// review for the person to book it again or set it aside; the dialog says
// all of it before the person confirms. The caller asks for the step-up
// before opening it.
const props = defineProps<{ open: boolean; item: StripeTxnItem }>()
const emit = defineEmits<{ (e: 'update:open', value: boolean): void }>()

const { t, locale } = useI18n()
const toast = useToast()
const { unassign } = useStripeTxnMutations()

const errorText = ref('')
watch(
  () => props.open,
  (open) => {
    if (open) errorText.value = ''
  },
)

const invoices = computed(() => (props.item.invoice_numbers ?? []).join(', '))

function close() {
  emit('update:open', false)
}

async function confirm() {
  errorText.value = ''
  try {
    await unassign.mutateAsync({ id: props.item.id })
    toast.success(t('admin.bankImports.stripeTxn.unassignDialog.success'))
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
    :title="t('admin.bankImports.stripeTxn.unassignDialog.title')"
    @update:open="(v: boolean) => emit('update:open', v)"
  >
    <form data-testid="stripe-txn-unassign-dialog" @submit.prevent="confirm">
      <p class="text-sm text-gray-700">
        {{ t('admin.bankImports.stripeTxn.intro', { amount: formatStripeAmount(item.amount, item.currency), date: formatDate(item.date, locale) }) }}
      </p>
      <ul class="mt-3 list-disc space-y-1 pl-5 text-sm text-gray-700" data-testid="stripe-txn-unassign-effects">
        <li>
          {{
            item.journal_entry_number
              ? t('admin.bankImports.stripeTxn.unassignDialog.reverseNumbered', { number: item.journal_entry_number })
              : t('admin.bankImports.stripeTxn.unassignDialog.reverse')
          }}
        </li>
        <li v-if="invoices" data-testid="stripe-txn-unassign-invoices">
          {{ t('admin.bankImports.stripeTxn.unassignDialog.reopen', { invoices }) }}
        </li>
        <li>{{ t('admin.bankImports.stripeTxn.unassignDialog.hold') }}</li>
        <li v-if="invoices" data-testid="stripe-txn-unassign-online-paid">
          {{ t('admin.bankImports.stripeTxn.unassignDialog.onlinePaid') }}
        </li>
      </ul>

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
          :disabled="unassign.isPending.value"
          data-testid="stripe-txn-unassign-confirm"
        >
          {{ t('admin.bankImports.stripeTxn.unassignDialog.confirm') }}
        </button>
      </div>
    </form>
  </Modal>
</template>
