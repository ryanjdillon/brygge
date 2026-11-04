<script setup lang="ts">
import FilterSelect, { type FilterSelectOption } from '@/components/admin/FilterSelect.vue'

export type HarborShareFilterValue = '' | 'owner' | 'not_owner'

withDefaults(defineProps<{
  modelValue: HarborShareFilterValue
  id?: string
}>(), { id: 'harbor-share-filter' })

const emit = defineEmits<{
  (e: 'update:modelValue', value: HarborShareFilterValue): void
}>()

const options: FilterSelectOption<HarborShareFilterValue>[] = [
  { value: '', labelKey: 'admin.users.harborShareFilterAll' },
  { value: 'owner', labelKey: 'admin.users.harborShareFilterOwner' },
  { value: 'not_owner', labelKey: 'admin.users.harborShareFilterNotOwner' },
]
</script>

<template>
  <FilterSelect
    :id="id"
    :model-value="modelValue"
    :options="options"
    label-key="admin.users.harborShareFilterLabel"
    @update:model-value="emit('update:modelValue', $event)"
  />
</template>
