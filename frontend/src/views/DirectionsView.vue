<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Car, Ship, Radio, Anchor, Download, Info } from 'lucide-vue-next'
import { useClubCoordinates, useMapMarkers } from '@/composables/useMap'
import SeaChart from '@/components/map/SeaChart.vue'
import LandMap from '@/components/map/LandMap.vue'

const { t } = useI18n()
const { data: club } = useClubCoordinates()
const { data: markers } = useMapMarkers()

const hasCoordinates = computed(
  () => club.value?.latitude != null && club.value?.longitude != null
)
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-gray-900">{{ t('directions.title') }}</h1>

    <div class="mt-10 grid gap-10 lg:grid-cols-2">
      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-gray-900">
          <Car class="h-5 w-5 text-blue-600" aria-hidden="true" />
          {{ t('directions.land') }}
        </h2>
        <div class="mt-4 h-72 overflow-hidden rounded-lg border border-gray-200">
          <LandMap
            v-if="hasCoordinates"
            :lat="club!.latitude!"
            :lng="club!.longitude!"
            :club-name="club!.name"
          />
          <div
            v-else
            class="flex h-full items-center justify-center bg-gray-50 text-gray-400"
          >
            {{ t('common.loading') }}
          </div>
        </div>
        <div class="mt-4 rounded-lg border border-gray-200 p-4">
          <dt class="flex items-center gap-2 text-sm font-medium text-gray-500">
            <Info class="h-4 w-4" aria-hidden="true" />
            {{ t('directions.landInstructionsTitle') }}
          </dt>
          <dd class="mt-1 text-gray-600">{{ t('directions.landInstructions') }}</dd>
        </div>
      </section>

      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-gray-900">
          <Ship class="h-5 w-5 text-blue-600" aria-hidden="true" />
          {{ t('directions.sea') }}
        </h2>

        <div class="mt-4 h-72 overflow-hidden rounded-lg border border-gray-200">
          <SeaChart
            v-if="hasCoordinates"
            :lat="club!.latitude!"
            :lng="club!.longitude!"
            :markers="markers ?? []"
            :club-name="club!.name"
          >
            <template #overlay>
              <a
                href="/api/v1/map/export/gpx"
                download
                class="absolute bottom-3 left-3 z-10 flex items-center gap-2 rounded-lg bg-white px-4 py-2.5 text-sm font-medium text-gray-900 shadow-lg transition hover:bg-gray-50"
              >
                <Download class="h-4 w-4" aria-hidden="true" />
                {{ t('directions.downloadGPX') }}
              </a>
            </template>
          </SeaChart>
          <div
            v-else
            class="flex h-full items-center justify-center bg-gray-50 text-gray-400"
          >
            {{ t('common.loading') }}
          </div>
        </div>

        <dl class="mt-4 space-y-4">
          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-gray-500">
              <Anchor class="h-4 w-4" aria-hidden="true" />
              {{ t('directions.coordinates') }}
            </dt>
            <dd class="mt-1 font-mono text-gray-900">
              <span v-if="hasCoordinates">
                {{ club!.latitude!.toFixed(4) }}&deg;N {{ club!.longitude!.toFixed(4) }}&deg;E
              </span>
              <span v-else>—</span>
            </dd>
          </div>

          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-gray-500">
              <Radio class="h-4 w-4" aria-hidden="true" />
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
      </section>
    </div>
  </div>
</template>
