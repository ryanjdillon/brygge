<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { useConfirm } from '@/stores/confirm'
import { unassignBankRow } from '@/composables/useBankImports'
import type { ClearedSettlementRefusal } from '@/composables/useClearedSettlement'

// Shown wherever a charge undo (or a ledger void of its bilag) is refused
// because the charge's Vipps settlement is already cleared (BRY-555). Offers
// to undo the clearing, which is the deposit's Angre: the payout bilag is
// voided, the deposit returns to Tildel and the fee stays booked. The parent
// repeats what was refused when it gets `undone`.
const props = defineProps<{ refusal: ClearedSettlementRefusal }>()
const emit = defineEmits<{ undone: [] }>()

const { t } = useI18n()
const queryClient = useQueryClient()
const { ensureFreshTotp } = useFreshTotp()
const askConfirm = useConfirm()

const busy = ref(false)
const error = ref<string | null>(null)

async function undo() {
  // Step-up at click time, then the confirmation, then the call.
  if (!(await ensureFreshTotp())) return
  const params = { settlement: props.refusal.settlementNumber, msn: props.refusal.msn }
  const ok = await askConfirm({
    title: t('admin.accounting.clearedSettlement.confirmTitle', params),
    body: t('admin.accounting.clearedSettlement.confirmBody'),
    details: [
      t('admin.accounting.clearedSettlement.detailPayout'),
      t('admin.accounting.clearedSettlement.detailDeposit', params),
      t('admin.accounting.clearedSettlement.detailFee'),
    ],
    confirmLabel: t('admin.accounting.clearedSettlement.undoButton'),
    cancelLabel: t('common.cancel'),
    tone: 'danger',
  })
  if (!ok) return
  busy.value = true
  error.value = null
  try {
    await unassignBankRow(props.refusal.bankRowId)
    queryClient.invalidateQueries({ queryKey: ['accounting'] })
    queryClient.invalidateQueries({ queryKey: ['bank-rows'] })
    emit('undone')
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mt-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-900" role="alert" data-testid="cleared-settlement-refusal">
    <p>
      {{
        t('admin.accounting.clearedSettlement.refused', {
          settlement: props.refusal.settlementNumber,
          msn: props.refusal.msn,
        })
      }}
    </p>
    <button
      v-if="props.refusal.bankRowId"
      type="button"
      class="mt-1 inline-flex min-h-11 items-center font-semibold text-brand-700 underline disabled:opacity-50"
      :disabled="busy"
      data-testid="cleared-settlement-undo"
      @click="undo"
    >
      {{ busy ? t('admin.accounting.clearedSettlement.undoing') : t('admin.accounting.clearedSettlement.undoButton') }}
    </button>
    <p v-else class="mt-1">{{ t('admin.accounting.clearedSettlement.noDeposit') }}</p>
    <p v-if="error" class="mt-1 text-red-700" data-testid="cleared-settlement-error">{{ error }}</p>
  </div>
</template>
