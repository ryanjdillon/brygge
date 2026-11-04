<script setup lang="ts">
import FilterSelect, { type FilterSelectOption } from '@/components/admin/FilterSelect.vue'

export type SpotFilterValue = '' | 'permanent' | 'seasonal' | 'none'

withDefaults(defineProps<{
  modelValue: SpotFilterValue
  id?: string
}>(), { id: 'spot-filter' })

const emit = defineEmits<{
  (e: 'update:modelValue', value: SpotFilterValue): void
}>()

const options: FilterSelectOption<SpotFilterValue>[] = [
  { value: '', labelKey: 'admin.users.spotFilterAll' },
  { value: 'permanent', labelKey: 'admin.users.spotPermanent' },
  { value: 'seasonal', labelKey: 'admin.users.spotSeasonal' },
  { value: 'none', labelKey: 'admin.users.spotNone' },
]
</script>

<template>
  <FilterSelect
    :id="id"
    :model-value="modelValue"
    :options="options"
    label-key="admin.users.spotFilterLabel"
    @update:model-value="emit('update:modelValue', $event)"
  />
</template>
