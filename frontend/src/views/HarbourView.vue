<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { Anchor, Radio, Sailboat, HandCoins, Download } from 'lucide-vue-next'
import { useClubCoordinates, useMapMarkers } from '@/composables/useMap'
import { usePricing } from '@/composables/usePricing'
import { useTodayAvailability } from '@/composables/useBookings'
import SeaChart from '@/components/map/SeaChart.vue'

const { t } = useI18n()
const { data: club } = useClubCoordinates()
const { data: markers } = useMapMarkers()
const { categories, isLoading: pricingLoading, unitLabel } = usePricing()
const { data: todayAvail, isLoading: resourcesLoading } = useTodayAvailability('guest_slip')

const totalCapacity = computed(() => todayAvail.value?.available ?? 0)

const hasCoordinates = computed(
  () => club.value?.latitude != null && club.value?.longitude != null,
)

const harbourCategoryOrder = ['guest', 'seasonal_rental', 'moloandel', 'slip_fee']

const harbourCategories = computed(() =>
  categories.value
    .filter((c) => harbourCategoryOrder.includes(c.key))
    .sort((a, b) => harbourCategoryOrder.indexOf(a.key) - harbourCategoryOrder.indexOf(b.key)),
)

function formatAmount(amount: number): string {
  return amount.toLocaleString('nb-NO')
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <div class="flex flex-wrap items-center justify-between gap-4">
      <div>
        <h1 class="text-3xl font-bold text-gray-900">{{ t('harbour.title') }}</h1>
        <p class="mt-1 text-gray-600">{{ t('harbour.subtitle') }}</p>
      </div>
      <div
        v-if="!resourcesLoading && todayAvail"
        class="flex items-center gap-2 rounded-full bg-green-50 px-4 py-2 text-sm font-medium text-green-700"
      >
        <span class="inline-block h-2 w-2 rounded-full bg-green-500" />
        {{ t('booking.availableToday', { available: todayAvail.available, total: todayAvail.total }) }}
      </div>
    </div>

    <!-- Sea chart -->
    <div class="mt-8 h-96 overflow-hidden rounded-lg border border-gray-200">
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

    <div class="mt-10 grid gap-10 lg:grid-cols-2">
      <!-- Navigation info (left / top on mobile) -->
      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-gray-900">
          <Sailboat class="h-5 w-5 text-blue-600" aria-hidden="true" />
          {{ t('harbour.navigation') }}
        </h2>

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
              <span v-else>&mdash;</span>
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
            <dd class="mt-1 text-sm text-gray-600">{{ t('directions.approachNotes') }}</dd>
          </div>

          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="text-sm font-medium text-gray-500">{{ t('directions.depth') }}</dt>
            <dd class="mt-1 text-sm text-gray-600">{{ t('directions.depthInfo') }}</dd>
          </div>
        </dl>

        <RouterLink
          to="/directions"
          class="mt-4 inline-block text-sm font-medium text-blue-600 hover:text-blue-700"
        >
          {{ t('harbour.fullDirections') }} &rarr;
        </RouterLink>
      </section>

      <!-- Pricing (right / below on mobile) -->
      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-gray-900">
          <HandCoins class="h-5 w-5 text-blue-600" aria-hidden="true" />
          {{ t('harbour.pricing') }}
        </h2>

        <div v-if="pricingLoading" class="mt-4 space-y-4">
          <div v-for="i in 2" :key="i" class="animate-pulse rounded-lg border border-gray-200 p-5">
            <div class="h-5 w-32 rounded bg-gray-200" />
            <div class="mt-3 h-4 w-full rounded bg-gray-200" />
          </div>
        </div>

        <div v-else-if="harbourCategories.length" class="mt-4 space-y-4">
          <div
            v-for="cat in harbourCategories"
            :key="cat.key"
            class="rounded-lg border border-gray-200 bg-white p-5"
          >
            <div class="flex items-center justify-between">
              <h3 class="font-semibold text-gray-900">
                {{ cat.label }}
                <span v-if="cat.key === 'slip_fee'" class="text-sm font-normal text-gray-500">
                  ({{ t('harbour.slipFeeNote') }})
                </span>
              </h3>
            </div>
            <ul class="mt-3 space-y-2">
              <li
                v-for="item in cat.items"
                :key="item.id"
                class="flex items-baseline justify-between border-b border-gray-100 pb-2 last:border-0 last:pb-0"
              >
                <span class="text-sm text-gray-700">{{ item.name }}</span>
                <span class="text-sm font-semibold text-gray-900">
                  {{ formatAmount(item.amount) }} kr
                  <span class="font-normal text-gray-500">{{ unitLabel(item.unit) }}</span>
                </span>
              </li>
            </ul>
            <RouterLink
              v-if="cat.key === 'moloandel'"
              to="/join"
              class="mt-3 inline-flex items-center gap-1 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-blue-700"
            >
              {{ t('harbour.joinWaitingList') }}
            </RouterLink>
          </div>
        </div>

        <p v-else class="mt-4 text-sm text-gray-500">{{ t('pricing.noPricing') }}</p>

        <RouterLink
          to="/pricing"
          class="mt-4 inline-block text-sm font-medium text-blue-600 hover:text-blue-700"
        >
          {{ t('harbour.allPrices') }} &rarr;
        </RouterLink>
      </section>
    </div>

    <!-- CTA -->
    <div class="mt-12 rounded-lg bg-blue-50 p-8 text-center">
      <h2 class="text-xl font-semibold text-blue-900">{{ t('harbour.ctaTitle') }}</h2>
      <p class="mt-2 text-blue-700">{{ t('harbour.ctaDescription') }}</p>
      <div class="mt-4 flex items-center justify-center gap-3">
        <RouterLink
          to="/book?type=guest_slip"
          class="inline-block rounded-md bg-blue-600 px-6 py-3 text-sm font-semibold text-white shadow hover:bg-blue-700"
        >
          {{ t('booking.bookNow') }}
        </RouterLink>
        <RouterLink
          to="/contact"
          class="inline-block rounded-md border border-blue-600 px-6 py-3 text-sm font-semibold text-blue-600 hover:bg-blue-50"
        >
          {{ t('harbour.ctaButton') }}
        </RouterLink>
      </div>
    </div>
  </div>
</template>
