<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { AlertTriangle } from 'lucide-vue-next'

const model = defineModel<{ length: number | null; beam: number | null; draft: number | null }>({
  required: true,
})

const { t } = useI18n()
</script>

<template>
  <div class="space-y-4">
    <div class="grid grid-cols-3 gap-4">
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('booking.boatLength') }}</label>
        <div class="mt-1 flex rounded-md shadow-sm">
          <input
            v-model.number="model.length"
            type="number"
            step="0.1"
            min="0"
            class="block w-full rounded-md border-gray-300 text-sm focus:border-blue-500 focus:ring-blue-500"
            placeholder="8.5"
          />
          <span class="ml-1 inline-flex items-center text-sm text-gray-500">m</span>
        </div>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('booking.boatBeam') }}</label>
        <div class="mt-1 flex rounded-md shadow-sm">
          <input
            v-model.number="model.beam"
            type="number"
            step="0.1"
            min="0"
            class="block w-full rounded-md border-gray-300 text-sm focus:border-blue-500 focus:ring-blue-500"
            placeholder="2.9"
          />
          <span class="ml-1 inline-flex items-center text-sm text-gray-500">m</span>
        </div>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('booking.boatDraft') }}</label>
        <div class="mt-1 flex rounded-md shadow-sm">
          <input
            v-model.number="model.draft"
            type="number"
            step="0.1"
            min="0"
            class="block w-full rounded-md border-gray-300 text-sm focus:border-blue-500 focus:ring-blue-500"
            placeholder="1.4"
          />
          <span class="ml-1 inline-flex items-center text-sm text-gray-500">m</span>
        </div>
      </div>
    </div>
    <div class="flex items-start gap-2 rounded-md bg-amber-50 p-3 text-sm text-amber-800">
      <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <p>{{ t('booking.dimensionDisclaimer') }}</p>
    </div>
  </div>
</template>
