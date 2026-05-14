<script setup lang="ts" generic="T extends string | number">
import Radio from './Radio.vue'

export interface RadioOption<V> {
  value: V
  label: string
  disabled?: boolean
}

withDefaults(
  defineProps<{
    modelValue: T | null
    options: RadioOption<T>[]
    name?: string
    disabled?: boolean
    layout?: 'row' | 'column'
  }>(),
  { layout: 'row' },
)

defineEmits<{
  (e: 'update:modelValue', value: T): void
}>()
</script>

<template>
  <div :class="layout === 'row' ? 'flex flex-wrap gap-4' : 'flex flex-col gap-2'">
    <Radio
      v-for="o in options"
      :key="String(o.value)"
      :model-value="modelValue"
      :value="o.value"
      :name="name"
      :disabled="disabled || o.disabled"
      @update:model-value="$emit('update:modelValue', $event as T)"
    >
      {{ o.label }}
    </Radio>
  </div>
</template>
