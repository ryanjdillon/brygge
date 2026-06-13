<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { X } from 'lucide-vue-next'

interface Props {
  open: boolean
  title?: string
  size?: 'sm' | 'md' | 'lg' | 'xl' | '2xl' | '3xl' | '4xl'
  closeOnBackdrop?: boolean
  closeOnEsc?: boolean
  showCloseButton?: boolean
  padding?: boolean
  /** Override z-index. Defaults to 50; pass `40` to render under elements that already live at z-50. */
  zIndex?: 40 | 50
}

const props = withDefaults(defineProps<Props>(), {
  size: 'md',
  closeOnBackdrop: true,
  closeOnEsc: true,
  showCloseButton: true,
  padding: true,
  zIndex: 50,
})

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'close'): void
}>()

const { t } = useI18n()

const sizeClass = computed(() => {
  switch (props.size) {
    case 'sm': return 'max-w-sm'
    case 'lg': return 'max-w-lg'
    case 'xl': return 'max-w-xl'
    case '2xl': return 'max-w-2xl'
    case '3xl': return 'max-w-3xl'
    case '4xl': return 'max-w-4xl'
    default: return 'max-w-md'
  }
})

const zClass = computed(() => (props.zIndex === 40 ? 'z-40' : 'z-50'))

function close() {
  emit('update:open', false)
  emit('close')
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      :class="['fixed inset-0 flex items-center justify-center bg-black/50 p-4', zClass]"
      role="dialog"
      aria-modal="true"
      v-backdrop-close="closeOnBackdrop ? close : () => {}"
      @keydown.esc="closeOnEsc && close()"
    >
      <div
        :class="['w-full rounded-lg bg-white shadow-xl', sizeClass, padding ? 'p-5' : '']"
        @click.stop
      >
        <div
          v-if="$slots.header || title || showCloseButton"
          :class="['flex items-start justify-between gap-3', padding ? 'mb-3 border-b border-gray-100 pb-3' : 'border-b border-gray-100 px-5 py-3']"
        >
          <slot name="header">
            <h2 class="text-base font-semibold text-gray-900">{{ title }}</h2>
          </slot>
          <button
            v-if="showCloseButton"
            type="button"
            class="text-gray-400 hover:text-gray-700"
            :aria-label="t('common.close')"
            @click="close"
          >
            <X class="h-5 w-5" />
          </button>
        </div>

        <slot />

        <div v-if="$slots.footer" :class="padding ? 'mt-5' : 'border-t border-gray-100 px-5 py-3'">
          <slot name="footer" />
        </div>
      </div>
    </div>
  </Teleport>
</template>
