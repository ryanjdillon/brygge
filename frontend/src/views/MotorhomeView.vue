<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { Car, Plug, Droplets, Info } from 'lucide-vue-next'
import { useClubCoordinates } from '@/composables/useMap'
import { usePricing } from '@/composables/usePricing'
import { useTodayAvailability } from '@/composables/useBookings'
import { useFeatures } from '@/composables/useFeatures'
import { useClubStore } from '@/stores/club'
import LandMap from '@/components/map/LandMap.vue'

const { t } = useI18n()
const { isEnabled } = useFeatures()
const bookingsEnabled = computed(() => isEnabled('bookings'))
const club = useClubStore()
club.ensureLoaded()
const { data: clubCoords } = useClubCoordinates()
const { categories, isLoading: pricingLoading, unitLabel } = usePricing()
const { data: todayAvail, isLoading: resourcesLoading } = useTodayAvailability(
  'motorhome_spot',
  bookingsEnabled,
)

const hasCoordinates = computed(
  () => clubCoords.value?.latitude != null && clubCoords.value?.longitude != null,
)

const motorhomeCategories = computed(() =>
  categories.value.filter((c) => c.key === 'motorhome'),
)

const powerText = computed(() => club.motorhomePower || t('motorhome.powerInfo'))
const facilitiesText = computed(() => club.motorhomeFacilities || t('motorhome.facilitiesInfo'))
const checkinText = computed(() => club.motorhomeCheckin || t('motorhome.checkinInfo'))
const rulesText = computed(() => club.motorhomeRules || t('motorhome.rulesInfo'))
const ctaTitle = computed(() => club.motorhomeCtaTitle || t('motorhome.ctaTitle'))
const ctaDesc = computed(() => club.motorhomeCtaDescription || t('motorhome.ctaDescription'))

function formatAmount(amount: number): string {
  return amount.toLocaleString('nb-NO')
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <div class="flex flex-wrap items-center justify-between gap-4">
      <div>
        <h1 class="text-3xl font-bold tracking-tight text-slate-900">{{ t('motorhome.title') }}</h1>
        <p class="mt-1 text-slate-600">{{ t('motorhome.subtitle') }}</p>
      </div>
      <div
        v-if="bookingsEnabled && !resourcesLoading && todayAvail"
        class="flex items-center gap-2 rounded-full bg-emerald-50 px-4 py-2 text-sm font-medium text-emerald-700 ring-1 ring-emerald-200"
      >
        <span class="inline-block h-2 w-2 rounded-full bg-emerald-500" />
        {{ t('booking.availableToday', { available: todayAvail.available, total: todayAvail.total }) }}
      </div>
    </div>

    <div class="mt-8 h-80 overflow-hidden rounded-2xl border border-slate-200">
      <LandMap
        v-if="hasCoordinates"
        :lat="clubCoords!.latitude!"
        :lng="clubCoords!.longitude!"
        :club-name="clubCoords!.name"
      />
      <div v-else class="flex h-full items-center justify-center bg-slate-50 text-slate-400">
        {{ t('common.loading') }}
      </div>
    </div>

    <div class="mt-10 grid gap-10 lg:grid-cols-2">
      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-slate-900">
          <Info class="h-5 w-5 text-slate-500" aria-hidden="true" />
          {{ t('motorhome.practicalInfo') }}
        </h2>

        <dl class="mt-4 space-y-3">
          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-slate-500">
              <Plug class="h-4 w-4" aria-hidden="true" />
              {{ t('motorhome.power') }}
            </dt>
            <dd class="mt-1 whitespace-pre-line text-sm text-slate-700">{{ powerText }}</dd>
          </div>

          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-slate-500">
              <Droplets class="h-4 w-4" aria-hidden="true" />
              {{ t('motorhome.facilities') }}
            </dt>
            <dd class="mt-1 whitespace-pre-line text-sm text-slate-700">{{ facilitiesText }}</dd>
          </div>

          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <dt class="text-sm font-medium text-slate-500">{{ t('motorhome.checkin') }}</dt>
            <dd class="mt-1 whitespace-pre-line text-sm text-slate-700">{{ checkinText }}</dd>
          </div>

          <div class="rounded-2xl border border-slate-200 bg-white p-4">
            <dt class="text-sm font-medium text-slate-500">{{ t('motorhome.rules') }}</dt>
            <dd class="mt-1 whitespace-pre-line text-sm text-slate-700">{{ rulesText }}</dd>
          </div>
        </dl>

        <RouterLink
          to="/directions"
          class="mt-4 inline-block text-sm font-medium text-slate-600 hover:text-slate-900"
        >
          {{ t('motorhome.fullDirections') }} &rarr;
        </RouterLink>
      </section>

      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-slate-900">
          <Car class="h-5 w-5 text-slate-500" aria-hidden="true" />
          {{ t('motorhome.pricing') }}
        </h2>

        <div v-if="pricingLoading" class="mt-4 animate-pulse rounded-2xl border border-slate-200 p-5">
          <div class="h-5 w-32 rounded bg-slate-200" />
          <div class="mt-3 h-4 w-full rounded bg-slate-200" />
        </div>

        <div v-else-if="motorhomeCategories.length" class="mt-4 space-y-3">
          <div
            v-for="cat in motorhomeCategories"
            :key="cat.key"
            class="rounded-2xl border border-slate-200 bg-white p-5"
          >
            <h3 class="font-semibold text-slate-900">{{ cat.label }}</h3>
            <ul class="mt-3 space-y-2">
              <li
                v-for="item in cat.rows"
                :key="item.id"
                class="flex items-baseline justify-between border-b border-slate-100 pb-2 last:border-0 last:pb-0"
              >
                <span class="text-sm text-slate-700">{{ item.name }}</span>
                <span class="text-sm font-semibold text-slate-900">
                  {{ formatAmount(item.allAmount ?? item.memberAmount ?? 0) }} kr
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
          {{ t('motorhome.allPrices') }} &rarr;
        </RouterLink>
      </section>
    </div>

    <div v-if="bookingsEnabled" class="mt-12 rounded-2xl bg-slate-900 p-8 text-center text-white sm:p-10">
      <h2 class="text-xl font-semibold">{{ ctaTitle }}</h2>
      <p class="mt-2 text-slate-200">{{ ctaDesc }}</p>
      <div class="mt-5 flex items-center justify-center gap-3">
        <RouterLink
          to="/book?type=motorhome_spot"
          class="inline-flex items-center gap-2 rounded-full bg-white px-6 py-3 text-sm font-semibold text-slate-900 shadow hover:bg-slate-50"
        >
          {{ t('booking.bookNow') }}
        </RouterLink>
        <RouterLink
          to="/contact"
          class="inline-flex items-center gap-2 rounded-full px-6 py-3 text-sm font-semibold text-white ring-1 ring-white/40 hover:bg-white/10"
        >
          {{ t('motorhome.ctaButton') }}
        </RouterLink>
      </div>
    </div>
  </div>
</template>
