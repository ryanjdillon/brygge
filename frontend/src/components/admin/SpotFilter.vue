<script setup lang="ts">
import { useI18n } from 'vue-i18n'

export type SpotFilterValue = '' | 'permanent' | 'seasonal' | 'none'

const props = defineProps<{
  modelValue: SpotFilterValue
  id?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: SpotFilterValue): void
}>()

const { t } = useI18n()
const inputId = props.id ?? 'spot-filter'
</script>

<template>
  <div class="inline-flex items-center">
    <label class="sr-only" :for="inputId">{{ t('admin.users.spotFilterLabel') }}</label>
    <select
      :id="inputId"
      :value="modelValue"
      class="rounded-md border border-gray-300 bg-white px-2 py-1.5 text-sm"
      :title="t('admin.users.spotFilterLabel')"
      @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value as SpotFilterValue)"
    >
      <option value="">{{ t('admin.users.spotFilterLabel') }}: {{ t('admin.users.spotFilterAll') }}</option>
      <option value="permanent">{{ t('admin.users.spotFilterLabel') }}: {{ t('admin.users.spotPermanent') }}</option>
      <option value="seasonal">{{ t('admin.users.spotFilterLabel') }}: {{ t('admin.users.spotSeasonal') }}</option>
      <option value="none">{{ t('admin.users.spotFilterLabel') }}: {{ t('admin.users.spotNone') }}</option>
    </select>
  </div>
</template>
