<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { FilePlus } from 'lucide-vue-next'
import { useConfirm } from '@/stores/confirm'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { useReissueAfterRefund, reissueErrorCode, type ReissuedInvoice } from '@/composables/useReissueAfterRefund'
import { formatNOK } from '@/lib/format'

// "Ny faktura" on a faktura that card refunds credited in full: issues a
// fresh faktura with the same lines to collect again. It takes the same
// step-up as creating a faktura, asked for at click time, before the
// confirm. The caller shows the button only to a treasurer or admin, while
// the server says it is possible (can_reissue).
const props = defineProps<{ invoiceId: string; invoiceNumber: number; amount: number }>()
const emit = defineEmits<{
  (e: 'reissued', result: ReissuedInvoice): void
  (e: 'failed', message: string): void
}>()

const { t } = useI18n()
const confirm = useConfirm()
const { ensureFreshTotp } = useFreshTotp()
const reissue = useReissueAfterRefund()

const busy = ref(false)

async function start() {
  if (busy.value) return
  if (!(await ensureFreshTotp())) return
  const ok = await confirm({
    title: t('admin.refund.reissue.confirmTitle'),
    body: t('admin.refund.reissue.confirmBody', { number: props.invoiceNumber, amount: formatNOK(props.amount) }),
    confirmLabel: t('admin.refund.reissue.confirmAction'),
    tone: 'info',
  })
  if (!ok) return
  busy.value = true
  try {
    emit('reissued', await reissue.mutateAsync(props.invoiceId))
  } catch (e) {
    emit('failed', t(`admin.refund.reissue.errors.${reissueErrorCode(e)}`))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <button
    type="button"
    class="inline-flex min-h-11 items-center justify-center gap-1.5 rounded-md border border-gray-300 bg-white px-3 text-sm font-semibold text-gray-700 hover:bg-gray-50 disabled:opacity-50"
    :disabled="busy"
    :title="t('admin.refund.reissue.buttonAria', { number: invoiceNumber })"
    :aria-label="t('admin.refund.reissue.buttonAria', { number: invoiceNumber })"
    data-testid="reissue-after-refund-button"
    @click="start"
  >
    <FilePlus class="h-4 w-4 shrink-0" />
    <span>{{ t('admin.refund.reissue.button') }}</span>
  </button>
</template>
