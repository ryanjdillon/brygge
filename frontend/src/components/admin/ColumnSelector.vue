<script setup lang="ts">
import { ref, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Columns3 } from 'lucide-vue-next'

export interface ColumnDef {
  key: string
  /** Label key under `admin.columns.*` in the i18n files. */
  labelKey: string
}

const props = defineProps<{
  columns: ColumnDef[]
  /** Currently visible column keys. Drives both this component's checkbox
   *  state and the parent's table rendering. */
  modelValue: string[]
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string[]): void
}>()

const { t } = useI18n()
const open = ref(false)
const wrapper = ref<HTMLElement | null>(null)

function toggle(key: string) {
  const set = new Set(props.modelValue)
  if (set.has(key)) set.delete(key)
  else set.add(key)
  emit('update:modelValue', [...set])
}

function isOn(key: string): boolean {
  return props.modelValue.includes(key)
}

function onDocumentClick(event: MouseEvent) {
  if (!open.value) return
  if (!wrapper.value) return
  if (!wrapper.value.contains(event.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('mousedown', onDocumentClick))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocumentClick))
</script>

<template>
  <div ref="wrapper" class="relative inline-flex">
    <button
      type="button"
      class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 bg-white px-2 py-1.5 text-sm text-gray-700 hover:bg-gray-50"
      :title="t('admin.columns.menuTitle')"
      @click="open = !open"
    >
      <Columns3 class="h-4 w-4" />
      <span>{{ t('admin.columns.menuTitle') }}</span>
    </button>
    <div
      v-if="open"
      class="absolute right-0 top-full z-20 mt-1 w-56 rounded-md border border-gray-200 bg-white py-1 shadow-lg"
    >
      <label
        v-for="c in columns"
        :key="c.key"
        class="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm hover:bg-gray-50"
      >
        <input
          type="checkbox"
          class="h-3.5 w-3.5 rounded border-gray-300"
          :checked="isOn(c.key)"
          @change="toggle(c.key)"
        />
        <span>{{ t(c.labelKey) }}</span>
      </label>
    </div>
  </div>
</template>
