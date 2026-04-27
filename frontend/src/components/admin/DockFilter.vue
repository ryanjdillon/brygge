<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// Reusable "Dock: <value>" dropdown filter, modeled on the variant
// originally inlined in MembersAdminView. Single source of truth so
// the two admin pages that filter by dock stay visually consistent.

const props = defineProps<{
  modelValue: string
  options: readonly string[]
  id?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const { t } = useI18n()
const inputId = props.id ?? 'dock-filter'
</script>

<template>
  <div class="inline-flex items-center">
    <label class="sr-only" :for="inputId">{{ t('common.dock') }}</label>
    <select
      :id="inputId"
      :value="modelValue"
      class="rounded-md border border-gray-300 bg-white px-2 py-1.5 text-sm"
      :title="t('common.dock')"
      @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)"
    >
      <option value="">{{ t('common.dock') }}: {{ t('common.dockAll') }}</option>
      <option v-for="d in options" :key="d" :value="d">{{ t('common.dock') }}: {{ d }}</option>
    </select>
  </div>
</template>
