<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// "Hugs betalaren" for Tildel's faktura assigns (BRY-383): remember the
// payer's name for the faktura's member so auto-assign matches their next
// payment. The warning never names the member the name already belongs to.
defineProps<{ payer: string; taken: boolean }>()
const remember = defineModel<boolean>({ required: true })

const { t } = useI18n()
</script>

<template>
  <div class="rounded-md border border-gray-200 bg-white px-3 py-2 text-xs" data-testid="payer-alias">
    <label class="flex items-center gap-2 text-gray-800">
      <input v-model="remember" type="checkbox" data-testid="payer-alias-checkbox" />
      <span>
        {{ t('admin.bankReconcile.payerAlias.remember') }}
        <span class="font-semibold">{{ payer }}</span>
      </span>
    </label>
    <p v-if="taken" class="mt-1 text-amber-800" data-testid="payer-alias-conflict">
      {{ t('admin.bankReconcile.payerAlias.conflictWarning') }}
    </p>
    <p v-else class="mt-1 text-gray-500">{{ t('admin.bankReconcile.payerAlias.hint') }}</p>
  </div>
</template>
