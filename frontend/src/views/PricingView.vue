<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { usePricing } from '@/composables/usePricing'

const { t } = useI18n()
const { data: categories, isLoading, isError } = usePricing()

const categoryLabels: Record<string, string> = {
  yearlyDues: 'pricing.yearlyDues',
  andel: 'pricing.andel',
  slipFees: 'pricing.slipFees',
  guestSlip: 'pricing.guestSlip',
  bobilParking: 'pricing.bobilParking',
  roomHire: 'pricing.roomHire',
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

    <div v-else-if="!categories?.length" class="mt-8 text-center text-gray-500">
      {{ t('pricing.noPricing') }}
    </div>

    <div v-else class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="category in categories"
        :key="category.key"
        class="rounded-lg border border-gray-200 p-6"
      >
        <h2 class="text-lg font-semibold text-gray-900">
          {{ categoryLabels[category.key] ? t(categoryLabels[category.key]) : category.label }}
        </h2>
        <ul class="mt-4 space-y-3">
          <li
            v-for="item in category.items"
            :key="item.name"
            class="flex items-baseline justify-between"
          >
            <span class="text-gray-600">{{ item.name }}</span>
            <span class="font-semibold text-gray-900">
              {{ item.price }} kr
              <span class="text-sm font-normal text-gray-500">{{ item.unit }}</span>
            </span>
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>
