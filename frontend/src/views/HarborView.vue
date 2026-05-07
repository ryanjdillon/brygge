<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { Anchor, Radio, Sailboat, HandCoins, Download } from 'lucide-vue-next'
import { useClubCoordinates, useMapMarkers } from '@/composables/useMap'
import { usePricing } from '@/composables/usePricing'
import { useTodayAvailability } from '@/composables/useBookings'
import { useFeatures } from '@/composables/useFeatures'
import { useClubStore } from '@/stores/club'
import SeaChart from '@/components/map/SeaChart.vue'

const { t } = useI18n()
const { isEnabled } = useFeatures()
const bookingsEnabled = computed(() => isEnabled('bookings'))
const club = useClubStore()
club.ensureLoaded()
const { data: clubCoords } = useClubCoordinates()
const { data: markers } = useMapMarkers()
const { categories, isLoading: pricingLoading, unitLabel } = usePricing()
const { data: todayAvail, isLoading: resourcesLoading } = useTodayAvailability(
  'guest_slip',
  bookingsEnabled,
)

const hasCoordinates = computed(
  () => clubCoords.value?.latitude != null && clubCoords.value?.longitude != null,
)

const harborCategoryOrder = ['guest', 'seasonal_rental', 'harbor_membership', 'slip_fee']
const harborCategories = computed(() =>
  categories.value
    .filter((c) => harborCategoryOrder.includes(c.key))
    .sort((a, b) => harborCategoryOrder.indexOf(a.key) - harborCategoryOrder.indexOf(b.key)),
)

// Each card prefers DB content; falls back to the bundled i18n string
// so the page still reads sensibly before any admin has visited
// settings.
const approachText = computed(() => club.harborApproach || t('directions.approachNotes'))
const depthText = computed(() => club.harborDepth || t('directions.depthInfo'))
const vhfText = computed(() => club.harborVhf || 'Ch 16 / Ch 73')
const ctaTitle = computed(() => club.harborCtaTitle || t('harbor.ctaTitle'))
const ctaDesc = computed(() => club.harborCtaDescription || t('harbor.ctaDescription'))

function formatAmount(amount: number): string {
  return amount.toLocaleString('nb-NO')
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <div class="flex flex-wrap items-center justify-between gap-4">
      <div>
        <h1 class="text-3xl font-bold tracking-tight text-slate-900">{{ t('harbor.title') }}</h1>
        <p class="mt-1 text-slate-600">{{ t('harbor.subtitle') }}</p>
      </div>
      <div
        v-if="bookingsEnabled && !resourcesLoading && todayAvail"
        class="flex items-center gap-2 rounded-full bg-emerald-50 px-4 py-2 text-sm font-medium text-emerald-700 ring-1 ring-emerald-200"
      >
        <span class="inline-block h-2 w-2 rounded-full bg-emerald-500" />
        {{ t('booking.availableToday', { available: todayAvail.available, total: todayAvail.total }) }}
      </div>
    </div>

    <div class="mt-8 h-96 overflow-hidden rounded-2xl border border-slate-200">
      <SeaChart
        v-if="hasCoordinates"
        :lat="clubCoords!.latitude!"
        :lng="clubCoords!.longitude!"
        :markers="markers ?? []"
        :club-name="clubCoords!.name"
      >
        <template #overlay>
          <a
            href="/api/v1/map/export/gpx"
            download
            class="absolute bottom-3 left-3 z-10 flex items-center gap-2 rounded-full bg-white px-4 py-2.5 text-sm font-medium text-slate-900 shadow-lg transition hover:bg-slate-50"
          >
            <Download class="h-4 w-4" aria-hidden="true" />
            {{ t('directions.downloadGPX') }}
          </a>
        </template>
      </SeaChart>
      <div v-else class="flex h-full items-center justify-center bg-slate-50 text-slate-400">
        {{ t('common.loading') }}
      </div>
    </div>

    <div class="mt-10 grid gap-10 lg:grid-cols-2">
      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-slate-900">
          <Sailboat class="h-5 w-5 text-slate-500" aria-hidden="true" />
          {{ t('harbor.navigation') }}
        </h2>

        <dl class="mt-4 space-y-3">
          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-slate-500">
              <Anchor class="h-4 w-4" aria-hidden="true" />
              {{ t('directions.coordinates') }}
            </dt>
            <dd class="mt-1 font-mono text-slate-900">
              <span v-if="hasCoordinates">
                {{ clubCoords!.latitude!.toFixed(4) }}&deg;N {{ clubCoords!.longitude!.toFixed(4) }}&deg;E
              </span>
              <span v-else class="text-slate-400">&mdash;</span>
            </dd>
          </div>

          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-slate-500">
              <Radio class="h-4 w-4" aria-hidden="true" />
              {{ t('directions.vhf') }}
            </dt>
            <dd class="mt-1 font-mono text-slate-900">{{ vhfText }}</dd>
          </div>

          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <dt class="text-sm font-medium text-slate-500">{{ t('directions.approach') }}</dt>
            <dd class="mt-1 whitespace-pre-line text-sm text-slate-700">{{ approachText }}</dd>
          </div>

          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <dt class="text-sm font-medium text-slate-500">{{ t('directions.depth') }}</dt>
            <dd class="mt-1 whitespace-pre-line text-sm text-slate-700">{{ depthText }}</dd>
          </div>
        </dl>

        <RouterLink
          to="/directions"
          class="mt-4 inline-block text-sm font-medium text-slate-600 hover:text-slate-900"
        >
          {{ t('harbor.fullDirections') }} &rarr;
        </RouterLink>
      </section>

      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-slate-900">
          <HandCoins class="h-5 w-5 text-slate-500" aria-hidden="true" />
          {{ t('harbor.pricing') }}
        </h2>

        <div v-if="pricingLoading" class="mt-4 space-y-3">
          <div v-for="i in 2" :key="i" class="animate-pulse rounded-2xl border border-slate-200 p-5">
            <div class="h-5 w-32 rounded bg-slate-200" />
            <div class="mt-3 h-4 w-full rounded bg-slate-200" />
          </div>
        </div>

        <div v-else-if="harborCategories.length" class="mt-4 space-y-3">
          <div
            v-for="cat in harborCategories"
            :key="cat.key"
            class="rounded-2xl border border-slate-200 bg-white p-5"
          >
            <div class="flex items-center justify-between">
              <h3 class="font-semibold text-slate-900">
                {{ cat.label }}
                <span v-if="cat.key === 'slip_fee'" class="text-sm font-normal text-slate-500">
                  ({{ t('harbor.slipFeeNote') }})
                </span>
              </h3>
            </div>
            <ul class="mt-3 space-y-2">
              <li
                v-for="item in cat.items"
                :key="item.id"
                class="flex items-baseline justify-between border-b border-slate-100 pb-2 last:border-0 last:pb-0"
              >
                <span class="text-sm text-slate-700">{{ item.name }}</span>
                <span class="text-sm font-semibold text-slate-900">
                  {{ formatAmount(item.amount) }} kr
                  <span class="font-normal text-slate-500">{{ unitLabel(item.unit) }}</span>
                </span>
              </li>
            </ul>
          </div>
        </div>

        <p v-else class="mt-4 text-sm text-slate-500">{{ t('pricing.noPricing') }}</p>

        <RouterLink
          to="/pricing"
          class="mt-4 inline-block text-sm font-medium text-slate-600 hover:text-slate-900"
        >
          {{ t('harbor.allPrices') }} &rarr;
        </RouterLink>
      </section>
    </div>

    <div v-if="bookingsEnabled" class="mt-12 rounded-2xl bg-slate-900 p-8 text-center text-white sm:p-10">
      <h2 class="text-xl font-semibold">{{ ctaTitle }}</h2>
      <p class="mt-2 text-slate-200">{{ ctaDesc }}</p>
      <div class="mt-5 flex items-center justify-center gap-3">
        <RouterLink
          to="/book?type=guest_slip"
          class="inline-flex items-center gap-2 rounded-full bg-white px-6 py-3 text-sm font-semibold text-slate-900 shadow hover:bg-slate-50"
        >
          {{ t('booking.bookNow') }}
        </RouterLink>
        <RouterLink
          to="/contact"
          class="inline-flex items-center gap-2 rounded-full px-6 py-3 text-sm font-semibold text-white ring-1 ring-white/40 hover:bg-white/10"
        >
          {{ t('harbor.ctaButton') }}
        </RouterLink>
      </div>
    </div>
  </div>
</template>
