<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Undo2 } from 'lucide-vue-next'
import RefundOrderDialog from '@/components/admin/RefundOrderDialog.vue'
import { useFreshTotp } from '@/composables/useFreshTotp'

// The refund entry point of the payment overview and the faktura list. The
// server gates a refund behind a fresh TOTP step-up, so the prompt comes at
// click time, before the dialog opens and the person fills it in. The caller
// shows the button only to a treasurer or admin, for an order that can be
// refunded now.
defineOptions({ inheritAttrs: false })
defineProps<{ orderId: string; compact?: boolean }>()
const emit = defineEmits<{ (e: 'refunded'): void }>()

const { t } = useI18n()
const { ensureFreshTotp } = useFreshTotp()

const open = ref(false)
// The dialog is only built once asked for: a list holds a button per row.
const built = ref(false)

async function start() {
  if (!(await ensureFreshTotp())) return
  built.value = true
  open.value = true
}
</script>

<template>
  <button
    v-bind="$attrs"
    type="button"
    class="inline-flex min-h-11 items-center justify-center gap-1.5 rounded-md border border-gray-300 bg-white px-3 text-sm font-semibold text-gray-700 hover:bg-gray-50"
    :class="compact ? 'min-w-11' : ''"
    :title="t('admin.refund.buttonAria')"
    :aria-label="t('admin.refund.buttonAria')"
    data-testid="refund-order-button"
    @click="start"
  >
    <Undo2 class="h-4 w-4 shrink-0" />
    <span :class="compact ? 'sr-only' : ''">{{ t('admin.refund.button') }}</span>
  </button>
  <RefundOrderDialog v-if="built" v-model:open="open" :order-id="orderId" @refunded="emit('refunded')" />
</template>
