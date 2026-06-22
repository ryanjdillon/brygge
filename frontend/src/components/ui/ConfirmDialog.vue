<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AlertTriangle, AlertCircle, Info } from 'lucide-vue-next'
import { useConfirmStore } from '@/stores/confirm'
import Modal from '@/components/ui/Modal.vue'

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
      return 'text-brand-600'
    default:
      return 'text-amber-600'
  }
})
const confirmBtnClass = computed(() => {
  switch (tone.value) {
    case 'danger':
      return 'bg-red-600 hover:bg-red-700 focus:ring-red-500'
    case 'info':
      return 'bg-brand-600 hover:bg-brand-700 focus:ring-brand-500'
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
  <Modal
    :open="!!store.current"
    size="md"
    :close-on-backdrop="false"
    :show-close-button="false"
    @close="onCancel"
  >
    <template #header>
      <div class="flex items-start gap-3">
        <component :is="Icon" :class="['mt-0.5 h-5 w-5', iconColor]" />
        <h2 class="text-base font-semibold text-gray-900">{{ store.current?.title }}</h2>
      </div>
    </template>

    <p class="whitespace-pre-line text-sm text-gray-700">{{ store.current?.body }}</p>
    <ul
      v-if="store.current?.details && store.current.details.length"
      class="mt-3 max-h-40 overflow-y-auto rounded-md bg-gray-50 p-2 text-xs text-gray-700"
    >
      <li v-for="(d, i) in store.current.details" :key="i" class="truncate">{{ d }}</li>
    </ul>

    <template #footer>
      <div class="flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50"
          @click="onCancel"
        >
          {{ store.current?.cancelLabel ?? t('common.cancel') }}
        </button>
        <button
          type="button"
          :class="['rounded-md px-3 py-2 text-sm font-semibold text-white focus:outline-none focus:ring-2 focus:ring-offset-2', confirmBtnClass]"
          @click="onConfirm"
        >
          {{ store.current?.confirmLabel ?? t('common.confirm') }}
        </button>
      </div>
    </template>
  </Modal>
</template>
