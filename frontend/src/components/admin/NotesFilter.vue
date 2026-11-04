<script setup lang="ts">
import FilterSelect, { type FilterSelectOption } from '@/components/admin/FilterSelect.vue'

export type NotesFilterValue = '' | 'with' | 'without'

withDefaults(defineProps<{
  modelValue: NotesFilterValue
  id?: string
}>(), { id: 'notes-filter' })

const emit = defineEmits<{
  (e: 'update:modelValue', value: NotesFilterValue): void
}>()

const options: FilterSelectOption<NotesFilterValue>[] = [
  { value: '', labelKey: 'admin.users.notesFilterAny' },
  { value: 'with', labelKey: 'admin.users.notesFilterWith' },
  { value: 'without', labelKey: 'admin.users.notesFilterWithout' },
]
</script>

<template>
  <FilterSelect
    :id="id"
    :model-value="modelValue"
    :options="options"
    label-key="admin.users.notesFilterLabel"
    @update:model-value="emit('update:modelValue', $event)"
  />
</template>
