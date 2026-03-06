<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Calendar, CloudSun, Ship, Users } from 'lucide-vue-next'

const { t } = useI18n()

const features = [
  { icon: Calendar, title: 'home.featureCalendar', desc: 'home.featureCalendarDesc', to: '/calendar' },
  { icon: CloudSun, title: 'home.featureWeather', desc: 'home.featureWeatherDesc', to: '/weather' },
  { icon: Ship, title: 'home.featureBookings', desc: 'home.featureBookingsDesc', to: '/pricing' },
  { icon: Users, title: 'home.featureMembers', desc: 'home.featureMembersDesc', to: '/join' },
]
</script>

<template>
  <div>
    <section class="bg-gradient-to-br from-blue-900 to-blue-700 px-4 py-24 text-center text-white sm:py-32">
      <h1 class="text-4xl font-bold tracking-tight sm:text-5xl">
        {{ t('home.welcome') }}
      </h1>
      <p class="mx-auto mt-4 max-w-xl text-lg text-blue-100">
        {{ t('home.tagline') }}
      </p>
      <RouterLink
        to="/join"
        class="mt-8 inline-block rounded-md bg-white px-6 py-3 text-sm font-semibold text-blue-900 shadow hover:bg-blue-50"
      >
        {{ t('home.ctaJoin') }}
      </RouterLink>
    </section>

    <section class="mx-auto max-w-7xl px-4 py-16 sm:px-6 lg:px-8">
      <div class="grid gap-8 sm:grid-cols-2 lg:grid-cols-4">
        <RouterLink
          v-for="feature in features"
          :key="feature.to"
          :to="feature.to"
          class="group rounded-lg border border-gray-200 p-6 transition hover:border-blue-300 hover:shadow-md"
        >
          <component :is="feature.icon" class="h-8 w-8 text-blue-600 group-hover:text-blue-700" />
          <h3 class="mt-4 text-lg font-semibold text-gray-900">
            {{ t(feature.title) }}
          </h3>
          <p class="mt-2 text-sm text-gray-600">
            {{ t(feature.desc) }}
          </p>
        </RouterLink>
      </div>
    </section>
  </div>
</template>
