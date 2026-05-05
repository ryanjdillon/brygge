<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AlertTriangle, AlertCircle, Info } from 'lucide-vue-next'
import { useConfirmStore } from '@/stores/confirm'

const { t } = useI18n()
const store = useConfirmStore()

const tone = computed(() => store.current?.tone ?? 'warning')
const Icon = computed(() => {
  switch (tone.value) {
    case 'danger':
      return AlertCircle
    case 'info':
      return Info
    default:
      return AlertTriangle
  }
})
const iconColor = computed(() => {
  switch (tone.value) {
    case 'danger':
      return 'text-red-600'
    case 'info':
      return 'text-blue-600'
    default:
      return 'text-amber-600'
  }
})
const confirmBtnClass = computed(() => {
  switch (tone.value) {
    case 'danger':
      return 'bg-red-600 hover:bg-red-700 focus:ring-red-500'
    case 'info':
      return 'bg-blue-600 hover:bg-blue-700 focus:ring-blue-500'
    default:
      return 'bg-amber-600 hover:bg-amber-700 focus:ring-amber-500'
  }
})

function onConfirm() {
  store.settle(true)
}
function onCancel() {
  store.settle(false)
}
</script>

<template>
  <div
    v-if="store.current"
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    @keydown.esc="onCancel"
  >
    <div class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl">
      <div class="flex items-start gap-3 border-b border-gray-100 pb-3">
        <component :is="Icon" :class="['mt-0.5 h-5 w-5', iconColor]" />
        <h2 class="text-base font-semibold text-gray-900">{{ store.current.title }}</h2>
      </div>
      <p class="mt-3 whitespace-pre-line text-sm text-gray-700">{{ store.current.body }}</p>
      <ul
        v-if="store.current.details && store.current.details.length"
        class="mt-3 max-h-40 overflow-y-auto rounded-md bg-gray-50 p-2 text-xs text-gray-700"
      >
        <li v-for="(d, i) in store.current.details" :key="i" class="truncate">{{ d }}</li>
      </ul>
      <div class="mt-5 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50"
          @click="onCancel"
        >
          {{ store.current.cancelLabel ?? t('common.cancel') }}
        </button>
        <button
          type="button"
          :class="['rounded-md px-3 py-2 text-sm font-semibold text-white focus:outline-none focus:ring-2 focus:ring-offset-2', confirmBtnClass]"
          @click="onConfirm"
        >
          {{ store.current.confirmLabel ?? t('common.confirm') }}
        </button>
      </div>
    </div>
  </div>
</template>
