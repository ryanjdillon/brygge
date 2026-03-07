<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { Car, Plug, Droplets, Info } from 'lucide-vue-next'
import { useClubCoordinates } from '@/composables/useMap'
import { usePricing, unitLabel } from '@/composables/usePricing'
import { useTodayAvailability } from '@/composables/useBookings'
import LandMap from '@/components/map/LandMap.vue'

const { t } = useI18n()
const { data: club } = useClubCoordinates()
const { categories, isLoading: pricingLoading } = usePricing()
const { data: todayAvail, isLoading: resourcesLoading } = useTodayAvailability('bobil_spot')

const totalCapacity = computed(() => todayAvail.value?.available ?? 0)

const hasCoordinates = computed(
  () => club.value?.latitude != null && club.value?.longitude != null,
)

const bobilCategories = computed(() =>
  categories.value.filter((c) => c.key === 'bobil'),
)

function formatAmount(amount: number): string {
  return amount.toLocaleString('nb-NO')
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <div class="flex flex-wrap items-center justify-between gap-4">
      <div>
        <h1 class="text-3xl font-bold text-gray-900">{{ t('bobil.title') }}</h1>
        <p class="mt-1 text-gray-600">{{ t('bobil.subtitle') }}</p>
      </div>
      <div
        v-if="!resourcesLoading && todayAvail"
        class="flex items-center gap-2 rounded-full bg-green-50 px-4 py-2 text-sm font-medium text-green-700"
      >
        <span class="inline-block h-2 w-2 rounded-full bg-green-500" />
        {{ t('booking.availableToday', { available: todayAvail.available, total: todayAvail.total }) }}
      </div>
    </div>

    <!-- Land map -->
    <div class="mt-8 h-80 overflow-hidden rounded-lg border border-gray-200">
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

    <div class="mt-10 grid gap-10 lg:grid-cols-2">
      <!-- Practical info -->
      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-gray-900">
          <Info class="h-5 w-5 text-blue-600" aria-hidden="true" />
          {{ t('bobil.practicalInfo') }}
        </h2>

        <dl class="mt-4 space-y-4">
          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-gray-500">
              <Plug class="h-4 w-4" aria-hidden="true" />
              {{ t('bobil.power') }}
            </dt>
            <dd class="mt-1 text-sm text-gray-600">{{ t('bobil.powerInfo') }}</dd>
          </div>

          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="flex items-center gap-2 text-sm font-medium text-gray-500">
              <Droplets class="h-4 w-4" aria-hidden="true" />
              {{ t('bobil.facilities') }}
            </dt>
            <dd class="mt-1 text-sm text-gray-600">{{ t('bobil.facilitiesInfo') }}</dd>
          </div>

          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="text-sm font-medium text-gray-500">{{ t('bobil.checkin') }}</dt>
            <dd class="mt-1 text-sm text-gray-600">{{ t('bobil.checkinInfo') }}</dd>
          </div>

          <div class="rounded-lg border border-gray-200 p-4">
            <dt class="text-sm font-medium text-gray-500">{{ t('bobil.rules') }}</dt>
            <dd class="mt-1 text-sm text-gray-600">{{ t('bobil.rulesInfo') }}</dd>
          </div>
        </dl>

        <RouterLink
          to="/directions"
          class="mt-4 inline-block text-sm font-medium text-blue-600 hover:text-blue-700"
        >
          {{ t('bobil.fullDirections') }} &rarr;
        </RouterLink>
      </section>

      <!-- Pricing -->
      <section>
        <h2 class="flex items-center gap-2 text-xl font-semibold text-gray-900">
          <Car class="h-5 w-5 text-blue-600" aria-hidden="true" />
          {{ t('bobil.pricing') }}
        </h2>

        <div v-if="pricingLoading" class="mt-4 animate-pulse rounded-lg border border-gray-200 p-5">
          <div class="h-5 w-32 rounded bg-gray-200" />
          <div class="mt-3 h-4 w-full rounded bg-gray-200" />
        </div>

        <div v-else-if="bobilCategories.length" class="mt-4 space-y-4">
          <div
            v-for="cat in bobilCategories"
            :key="cat.key"
            class="rounded-lg border border-gray-200 bg-white p-5"
          >
            <h3 class="font-semibold text-gray-900">{{ cat.label }}</h3>
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
          </div>
        </div>

        <p v-else class="mt-4 text-sm text-gray-500">{{ t('pricing.noPricing') }}</p>

        <RouterLink
          to="/pricing"
          class="mt-4 inline-block text-sm font-medium text-blue-600 hover:text-blue-700"
        >
          {{ t('bobil.allPrices') }} &rarr;
        </RouterLink>
      </section>
    </div>

    <!-- CTA -->
    <div class="mt-12 rounded-lg bg-blue-50 p-8 text-center">
      <h2 class="text-xl font-semibold text-blue-900">{{ t('bobil.ctaTitle') }}</h2>
      <p class="mt-2 text-blue-700">{{ t('bobil.ctaDescription') }}</p>
      <div class="mt-4 flex items-center justify-center gap-3">
        <RouterLink
          to="/book?type=bobil_spot"
          class="inline-block rounded-md bg-blue-600 px-6 py-3 text-sm font-semibold text-white shadow hover:bg-blue-700"
        >
          {{ t('booking.bookNow') }}
        </RouterLink>
        <RouterLink
          to="/contact"
          class="inline-block rounded-md border border-blue-600 px-6 py-3 text-sm font-semibold text-blue-600 hover:bg-blue-50"
        >
          {{ t('bobil.ctaButton') }}
        </RouterLink>
      </div>
    </div>
  </div>
</template>
