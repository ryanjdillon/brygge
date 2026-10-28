<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Modal from '@/components/ui/Modal.vue'
import { formatNOK, formatDate } from '@/lib/format'
import { useToast } from '@/composables/useToast'
import {
  useStripeAssignableAccounts,
  useStripeTxnMutations,
  stripeTxnErrorCode,
  type StripeTxnItem,
} from '@/composables/useStripeTransactions'

// Books a Stripe row to an account the person picks: money in goes
// DR the Stripe clearing account / CR the account, money out the other way,
// with Stripe's fee. Only the accounts the server accepts are offered. The
// caller asks for the step-up before opening it.
const props = defineProps<{ open: boolean; item: StripeTxnItem }>()
const emit = defineEmits<{ (e: 'update:open', value: boolean): void }>()

const { t, locale } = useI18n()
const toast = useToast()
const { assignAccount } = useStripeTxnMutations()

const enabled = computed(() => props.open)
const { data: accounts, isLoading } = useStripeAssignableAccounts(enabled)

const accountCode = ref('')
const note = ref('')
const errorText = ref('')
watch(
  () => props.open,
  (open) => {
    if (!open) return
    accountCode.value = ''
    note.value = ''
    errorText.value = ''
  },
)

const incoming = computed(() => props.item.amount > 0)

function close() {
  emit('update:open', false)
}

async function confirm() {
  if (!accountCode.value) return
  errorText.value = ''
  try {
    await assignAccount.mutateAsync({ id: props.item.id, accountCode: accountCode.value, note: note.value.trim() })
    toast.success(t('admin.bankImports.stripeTxn.accountDialog.success'))
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
    :title="t('admin.bankImports.stripeTxn.accountDialog.title')"
    @update:open="(v: boolean) => emit('update:open', v)"
  >
    <form data-testid="stripe-txn-account-dialog" @submit.prevent="confirm">
      <p class="text-sm text-gray-700">
        {{ t('admin.bankImports.stripeTxn.intro', { amount: formatNOK(item.amount), date: formatDate(item.date, locale) }) }}
      </p>
      <p class="mt-1 text-xs text-gray-500">
        {{ incoming ? t('admin.bankImports.stripeTxn.accountDialog.postingIn') : t('admin.bankImports.stripeTxn.accountDialog.postingOut') }}
      </p>

      <label class="mt-4 block text-sm font-medium text-gray-700" for="stripe-txn-account">
        {{ t('admin.bankImports.stripeTxn.accountDialog.account') }}
      </label>
      <!-- A native select rather than ui AccountSelect: that listbox has
           sub-44px options and no search, and it is keyed on account ids,
           which the assignable-accounts list does not carry. -->
      <select
        id="stripe-txn-account"
        v-model="accountCode"
        class="mt-1 block min-h-11 w-full rounded-md border border-gray-300 bg-white px-3 text-sm"
        :disabled="isLoading"
        data-testid="stripe-txn-account-select"
      >
        <option value="" disabled>{{ t('admin.bankImports.stripeTxn.accountDialog.accountPlaceholder') }}</option>
        <option v-for="a in accounts ?? []" :key="a.code" :value="a.code">{{ a.code }} – {{ a.name }}</option>
      </select>

      <label class="mt-4 block text-sm font-medium text-gray-700" for="stripe-txn-account-note">
        {{ t('admin.bankImports.stripeTxn.accountDialog.note') }}
      </label>
      <input
        id="stripe-txn-account-note"
        v-model="note"
        type="text"
        maxlength="200"
        class="mt-1 block min-h-11 w-full rounded-md border border-gray-300 px-3 text-sm"
        :placeholder="t('admin.bankImports.stripeTxn.accountDialog.notePlaceholder')"
        data-testid="stripe-txn-account-note"
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
          class="min-h-11 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="!accountCode || assignAccount.isPending.value"
          data-testid="stripe-txn-account-confirm"
        >
          {{ t('admin.bankImports.stripeTxn.accountDialog.confirm') }}
        </button>
      </div>
    </form>
  </Modal>
</template>
