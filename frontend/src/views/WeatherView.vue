<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useWeather } from '@/composables/useWeather'
import { Wind, Thermometer, Droplets } from 'lucide-vue-next'

const { t } = useI18n()
const { data: weather, isLoading, isError } = useWeather()

function windDirectionLabel(degrees: number | null): string {
  if (degrees == null) return '—'
  const dirs = [
    t('weather.dirN'), t('weather.dirNE'), t('weather.dirE'), t('weather.dirSE'),
    t('weather.dirS'), t('weather.dirSW'), t('weather.dirW'), t('weather.dirNW'),
  ]
  return dirs[Math.round(degrees / 45) % 8]
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-gray-900">{{ t('weather.title') }}</h1>

    <div v-if="isLoading" class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="i in 3"
        :key="i"
        class="animate-pulse rounded-lg border border-gray-200 p-6"
      >
        <div class="h-8 w-8 rounded bg-gray-200" />
        <div class="mt-4 h-4 w-24 rounded bg-gray-200" />
        <div class="mt-2 h-8 w-16 rounded bg-gray-200" />
      </div>
    </div>

    <div v-else-if="isError" class="mt-8 rounded-lg border border-red-200 bg-red-50 p-6 text-red-700">
      {{ t('weather.error') }}
    </div>

    <div v-else-if="weather" class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div class="rounded-lg border border-gray-200 p-6">
        <Thermometer class="h-8 w-8 text-blue-600" />
        <p class="mt-4 text-sm text-gray-500">{{ t('weather.temperature') }}</p>
        <p class="mt-1 text-2xl font-bold text-gray-900">{{ weather.temperature ?? '—' }}&deg;C</p>
      </div>

      <div class="rounded-lg border border-gray-200 p-6">
        <Wind class="h-8 w-8 text-blue-600" />
        <p class="mt-4 text-sm text-gray-500">{{ t('weather.wind') }}</p>
        <p class="mt-1 text-2xl font-bold text-gray-900">{{ weather.windSpeed ?? '—' }} m/s</p>
        <p class="text-sm text-gray-500">{{ windDirectionLabel(weather.windDirection) }}</p>
      </div>

      <div class="rounded-lg border border-gray-200 p-6">
        <Droplets class="h-8 w-8 text-blue-600" />
        <p class="mt-4 text-sm text-gray-500">{{ t('weather.humidity') }}</p>
        <p class="mt-1 text-2xl font-bold text-gray-900">{{ weather.humidity ?? '—' }}%</p>
      </div>
    </div>

    <p class="mt-8 text-center text-xs text-gray-400">
      {{ t('weather.attribution') }}
    </p>
  </div>
</template>
