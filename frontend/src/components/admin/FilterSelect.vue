<script setup lang="ts" generic="T extends string">
import { useI18n } from 'vue-i18n'

// One labelled filter dropdown for admin list toolbars. Each option reads
// "<label>: <option>" so the active filter stays legible once a value is
// picked, without a visible label taking toolbar width on phones.
export interface FilterSelectOption<V extends string> {
  value: V
  labelKey: string
}

const props = defineProps<{
  modelValue: T
  options: FilterSelectOption<T>[]
  labelKey: string
  id: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: T): void
}>()

const { t } = useI18n()
</script>

<template>
  <div class="inline-flex items-center">
    <label class="sr-only" :for="props.id">{{ t(labelKey) }}</label>
    <select
      :id="props.id"
      :value="modelValue"
      class="min-h-11 rounded-md border border-gray-300 bg-white px-2 py-1.5 text-sm md:min-h-0"
      :title="t(labelKey)"
      @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value as T)"
    >
      <option v-for="o in options" :key="o.value" :value="o.value">
        {{ t(labelKey) }}: {{ t(o.labelKey) }}
      </option>
    </select>
  </div>
</template>
