<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { Clock } from 'lucide-vue-next'
import { useTotpGateStore } from '@/stores/totpGate'

const { t } = useI18n()
const gate = useTotpGateStore()

function keepWorking() {
  gate.dismissExpiringWarning()
  gate.open()
}

function dismiss() {
  gate.dismissExpiringWarning()
}
</script>

<template>
  <div
    v-if="gate.warnExpiring && !gate.pending"
    class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4"
    role="dialog"
    aria-modal="true"
    @keydown.esc="dismiss"
  >
    <div class="w-full max-w-sm rounded-lg bg-white p-5 shadow-xl">
      <div class="flex items-center gap-2 border-b border-gray-100 pb-3">
        <Clock class="h-5 w-5 text-amber-600" />
        <h2 class="text-base font-semibold text-gray-900">
          {{ t('totpExpiring.title') }}
        </h2>
      </div>
      <p class="mt-3 text-sm text-gray-600">
        {{ t('totpExpiring.body') }}
      </p>
      <div class="mt-4 flex gap-2">
        <button
          type="button"
          class="flex-1 rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50"
          @click="dismiss"
        >
          {{ t('totpExpiring.dismiss') }}
        </button>
        <button
          type="button"
          class="flex-1 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white hover:bg-blue-700"
          @click="keepWorking"
        >
          {{ t('totpExpiring.keepWorking') }}
        </button>
      </div>
    </div>
  </div>
</template>
