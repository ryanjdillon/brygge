<script setup lang="ts">
import { useI18n } from 'vue-i18n'

export type NotesFilterValue = '' | 'with' | 'without'

const props = defineProps<{
  modelValue: NotesFilterValue
  id?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: NotesFilterValue): void
}>()

const { t } = useI18n()
const inputId = props.id ?? 'notes-filter'
</script>

<template>
  <div class="inline-flex items-center">
    <label class="sr-only" :for="inputId">{{ t('admin.users.notesFilterLabel') }}</label>
    <select
      :id="inputId"
      :value="modelValue"
      class="rounded-md border border-gray-300 bg-white px-2 py-1.5 text-sm"
      :title="t('admin.users.notesFilterLabel')"
      @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value as NotesFilterValue)"
    >
      <option value="">{{ t('admin.users.notesFilterLabel') }}: {{ t('admin.users.notesFilterAny') }}</option>
      <option value="with">{{ t('admin.users.notesFilterLabel') }}: {{ t('admin.users.notesFilterWith') }}</option>
      <option value="without">{{ t('admin.users.notesFilterLabel') }}: {{ t('admin.users.notesFilterWithout') }}</option>
    </select>
  </div>
</template>
