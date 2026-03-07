<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { usePricing } from '@/composables/usePricing'

const { t } = useI18n()
const { categories, isLoading, isError, unitLabel } = usePricing()

function formatAmount(amount: number): string {
  return amount.toLocaleString('nb-NO')
}

function seasonLabel(metadata: Record<string, string>): string | null {
  if (!metadata.period_start || !metadata.period_end) return null
  return `${metadata.period_start} – ${metadata.period_end}`
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-gray-900">{{ t('pricing.title') }}</h1>

    <div v-if="isLoading" class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="i in 6"
        :key="i"
        class="animate-pulse rounded-lg border border-gray-200 p-6"
      >
        <div class="h-5 w-32 rounded bg-gray-200" />
        <div class="mt-4 space-y-2">
          <div class="h-4 w-full rounded bg-gray-200" />
          <div class="h-4 w-3/4 rounded bg-gray-200" />
        </div>
      </div>
    </div>

    <div v-else-if="isError" class="mt-8 rounded-lg border border-red-200 bg-red-50 p-6 text-red-700">
      {{ t('pricing.error') }}
    </div>

    <div v-else-if="!categories.length" class="mt-8 text-center text-gray-500">
      {{ t('pricing.noPricing') }}
    </div>

    <div v-else class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="cat in categories"
        :key="cat.key"
        class="rounded-lg border border-gray-200 bg-white p-6"
      >
        <h2 class="text-lg font-semibold text-gray-900">{{ cat.label }}</h2>
        <ul class="mt-4 space-y-3">
          <li
            v-for="item in cat.items"
            :key="item.id"
            class="border-b border-gray-100 pb-3 last:border-0 last:pb-0"
          >
            <div class="flex items-baseline justify-between">
              <span class="text-gray-700">{{ item.name }}</span>
              <span class="font-semibold text-gray-900">
                {{ formatAmount(item.amount) }} kr
                <span class="text-sm font-normal text-gray-500">{{ unitLabel(item.unit) }}</span>
              </span>
            </div>
            <p v-if="item.description" class="mt-0.5 text-sm text-gray-500">
              {{ item.description }}
            </p>
            <p v-if="seasonLabel(item.metadata)" class="mt-0.5 text-xs text-gray-400">
              {{ t('pricing.period') }}: {{ seasonLabel(item.metadata) }}
            </p>
            <p v-if="item.installments_allowed" class="mt-0.5 text-xs text-blue-600">
              {{ t('pricing.installments', { max: item.max_installments }) }}
            </p>
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>
