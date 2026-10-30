<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Modal from '@/components/ui/Modal.vue'
import SuggestionsLoadError from '@/components/admin/SuggestionsLoadError.vue'
import { formatNOK, formatDate } from '@/lib/format'
import { useToast } from '@/composables/useToast'
import {
  useStripeTxnPotentialInvoices,
  useStripeTxnMutations,
  stripeTxnErrorCode,
  type StripeTxnItem,
} from '@/composables/useStripeTransactions'

// Books a card charge against one faktura (DR the Stripe clearing account /
// CR receivables, with Stripe's fee). Offers the open fakturas whose total
// equals the charge to the øre, the same exact match the server requires
// when it re-checks the faktura under its lock. The caller asks for the step-up before opening it.
const props = defineProps<{ open: boolean; item: StripeTxnItem }>()
const emit = defineEmits<{ (e: 'update:open', value: boolean): void }>()

const { t, locale } = useI18n()
const toast = useToast()
const { assignInvoice } = useStripeTxnMutations()

const search = ref('')
const q = ref('')
let debounce: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => {
  clearTimeout(debounce)
  debounce = setTimeout(() => (q.value = v.trim()), 250)
})

const enabled = computed(() => props.open)
const txnId = computed<string | null>(() => props.item.id)
const { data: invoices, isLoading, isError, refetch } = useStripeTxnPotentialInvoices(txnId, q, enabled)

const selectedId = ref<string | null>(null)
const errorText = ref('')
watch(
  () => props.open,
  (open) => {
    if (!open) return
    selectedId.value = null
    errorText.value = ''
    search.value = ''
    q.value = ''
  },
)

function close() {
  emit('update:open', false)
}

async function confirm() {
  if (!selectedId.value) return
  errorText.value = ''
  try {
    await assignInvoice.mutateAsync({ id: props.item.id, invoiceId: selectedId.value })
    toast.success(t('admin.bankImports.stripeTxn.invoiceDialog.success'))
    close()
  } catch (e) {
    const code = stripeTxnErrorCode(e)
    errorText.value = t(`admin.bankImports.stripeTxn.errors.${code ?? 'generic'}`)
    if (code === 'invoice_credited') {
      // The faktura was credited since the list loaded: drop it from the list.
      selectedId.value = null
      void refetch()
    }
  }
}
</script>

<template>
  <Modal
    :open="open"
    size="lg"
    full-screen-on-mobile
    :title="t('admin.bankImports.stripeTxn.invoiceDialog.title')"
    @update:open="(v: boolean) => emit('update:open', v)"
  >
    <div data-testid="stripe-txn-invoice-dialog">
      <p class="text-sm text-gray-700">
        {{ t('admin.bankImports.stripeTxn.intro', { amount: formatNOK(item.amount), date: formatDate(item.date, locale) }) }}
      </p>
      <p class="mt-1 text-xs text-gray-500">{{ t('admin.bankImports.stripeTxn.invoiceDialog.posting') }}</p>

      <label class="mt-4 block text-sm font-medium text-gray-700" for="stripe-txn-invoice-search">
        {{ t('admin.bankImports.stripeTxn.invoiceDialog.search') }}
      </label>
      <input
        id="stripe-txn-invoice-search"
        v-model="search"
        type="search"
        inputmode="search"
        class="mt-1 block min-h-11 w-full rounded-md border border-gray-300 px-3 text-sm"
        :placeholder="t('admin.bankImports.stripeTxn.invoiceDialog.searchPlaceholder')"
        data-testid="stripe-txn-invoice-search"
      />

      <p v-if="isLoading" class="mt-4 text-sm text-gray-500">{{ t('common.loading') }}…</p>
      <SuggestionsLoadError v-else-if="isError" @retry="refetch()" />
      <p
        v-else-if="!invoices?.length"
        class="mt-4 rounded-md border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500"
        data-testid="stripe-txn-invoice-empty"
      >
        {{ t('admin.bankImports.stripeTxn.invoiceDialog.empty') }}
      </p>
      <ul v-else class="mt-4 space-y-2" role="radiogroup" :aria-label="t('admin.bankImports.stripeTxn.invoiceDialog.title')">
        <li v-for="inv in invoices" :key="inv.invoice_id">
          <button
            type="button"
            role="radio"
            :aria-checked="selectedId === inv.invoice_id"
            class="flex min-h-11 w-full flex-col items-start gap-0.5 rounded-md border px-3 py-2 text-left text-sm"
            :class="selectedId === inv.invoice_id ? 'border-brand-500 bg-brand-50 ring-1 ring-brand-400' : 'border-gray-200 bg-white'"
            :data-testid="`stripe-txn-invoice-${inv.invoice_number}`"
            @click="selectedId = inv.invoice_id"
          >
            <span class="flex w-full flex-wrap items-baseline gap-x-3">
              <span class="font-semibold">{{ t('admin.bankImports.stripeTxn.invoiceDialog.invoice', { number: inv.invoice_number }) }}</span>
              <span class="tabular-nums">{{ formatNOK(inv.total_amount) }}</span>
              <span class="text-gray-600">{{ formatDate(inv.due_date, locale) }}</span>
            </span>
            <span class="break-words text-xs text-gray-600">{{ inv.member_name || inv.price_item_name }}</span>
          </button>
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
          type="button"
          class="min-h-11 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="!selectedId || assignInvoice.isPending.value"
          data-testid="stripe-txn-invoice-confirm"
          @click="confirm"
        >
          {{ t('admin.bankImports.stripeTxn.invoiceDialog.confirm') }}
        </button>
      </div>
    </div>
  </Modal>
</template>
