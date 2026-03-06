<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { MapPin, Navigation, Radio, Anchor } from 'lucide-vue-next'

const { t } = useI18n()
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-gray-900">{{ t('directions.title') }}</h1>

    <div class="mt-10 grid gap-10 lg:grid-cols-2">
      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-gray-900">
          <MapPin class="h-5 w-5 text-blue-600" />
          {{ t('directions.land') }}
        </h2>
        <div
          class="mt-4 flex h-64 items-center justify-center rounded-lg border-2 border-dashed border-gray-300 bg-gray-50 text-gray-400"
        >
          Embedded map placeholder
        </div>
        <p class="mt-4 text-gray-600">{{ t('directions.landInstructions') }}</p>
      </section>

      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-gray-900">
          <Navigation class="h-5 w-5 text-blue-600" />
          {{ t('directions.sea') }}
        </h2>

        <dl class="mt-4 space-y-4">
          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-gray-500">
              <Anchor class="h-4 w-4" />
              {{ t('directions.coordinates') }}
            </dt>
            <dd class="mt-1 font-mono text-gray-900">59&deg;54.3'N 010&deg;44.1'E</dd>
          </div>

          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-gray-500">
              <Radio class="h-4 w-4" />
              {{ t('directions.vhf') }}
            </dt>
            <dd class="mt-1 font-mono text-gray-900">Ch 16 / Ch 73</dd>
          </div>

          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="text-sm font-medium text-gray-500">{{ t('directions.approach') }}</dt>
            <dd class="mt-1 text-gray-600">{{ t('directions.approachNotes') }}</dd>
          </div>

          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="text-sm font-medium text-gray-500">{{ t('directions.depth') }}</dt>
            <dd class="mt-1 text-gray-600">{{ t('directions.depthInfo') }}</dd>
          </div>
        </dl>

        <div class="mt-6">
          <h3 class="text-sm font-medium text-gray-500">{{ t('directions.harbourChart') }}</h3>
          <div
            class="mt-2 flex h-48 items-center justify-center rounded-lg border-2 border-dashed border-gray-300 bg-gray-50 text-gray-400"
          >
            Harbour chart image placeholder
          </div>
        </div>
      </section>
    </div>
  </div>
</template>
