<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  Anchor,
  Caravan,
  CloudSun,
  Calendar,
  Mail,
  Tag,
  ShoppingBag,
  ArrowRight,
  LogIn,
} from 'lucide-vue-next'
import { useFeatures } from '@/composables/useFeatures'
import { useClubStore } from '@/stores/club'
import heroImage from '@/assets/hero.jpg'

const { t } = useI18n()
const { isEnabled } = useFeatures()
const club = useClubStore()
club.ensureLoaded()

const clubName = computed(() => club.name || 'Brygge')
const postEmail = computed(() => (club.domain ? `post@${club.domain}` : 'post@klokkarvikbaatlag.no'))

type FeatureFlag = 'bookings' | 'projects' | 'calendar' | 'commerce' | 'accounting'

interface Tile {
  icon: typeof Anchor
  title: string
  desc: string
  to: string
  feature?: FeatureFlag
}

interface Section {
  titleKey: string
  tiles: Tile[]
}

const sections = computed<Section[]>(() => {
  const all: Section[] = [
    {
      titleKey: 'home.sectionVisit',
      tiles: [
        { icon: Anchor, title: 'home.featureHarbor', desc: 'home.featureHarborDesc', to: '/harbor' },
        { icon: Caravan, title: 'home.featureMotorhome', desc: 'home.featureMotorhomeDesc', to: '/motorhome' },
        { icon: CloudSun, title: 'home.featureWeather', desc: 'home.featureWeatherDesc', to: '/weather' },
      ],
    },
    {
      titleKey: 'home.sectionClub',
      tiles: [
        { icon: Tag, title: 'home.featurePricing', desc: 'home.featurePricingDesc', to: '/pricing', feature: 'accounting' },
        { icon: Calendar, title: 'home.featureCalendar', desc: 'home.featureCalendarDesc', to: '/calendar', feature: 'calendar' },
        { icon: Mail, title: 'home.featureContact', desc: 'home.featureContactDesc', to: '/contact' },
        { icon: ShoppingBag, title: 'home.featureMerchandise', desc: 'home.featureMerchandiseDesc', to: '/merchandise', feature: 'commerce' },
      ],
    },
  ]
  return all
    .map((s) => ({ ...s, tiles: s.tiles.filter((tile) => !tile.feature || isEnabled(tile.feature)) }))
    .filter((s) => s.tiles.length > 0)
})
</script>

<template>
  <div>
    <!-- Hero — the photo extends up behind the (transparent) navbar.
         A full-bleed dark gradient overlay lifts contrast on the upper
         half so the welcome text and CTA stay readable regardless of
         where the photo's brighter regions land. -->
    <section class="relative isolate overflow-hidden text-center text-white">
      <img
        :src="heroImage"
        alt=""
        aria-hidden="true"
        class="absolute inset-0 -z-10 h-full w-full object-cover object-center"
      />
      <!-- Dark gradient: heavier near the top so the navbar reads, fades
           toward the bottom so the photo retains presence. The second
           layer is a darker pool centered on the CTA area. -->
      <div
        aria-hidden="true"
        class="absolute inset-0 -z-10 bg-gradient-to-b from-black/70 via-black/45 to-black/55"
      />
      <div
        aria-hidden="true"
        class="absolute inset-0 -z-10 bg-[radial-gradient(ellipse_at_center,rgba(0,0,0,0.45),transparent_70%)]"
      />

      <div class="relative px-4 pb-24 pt-36 sm:pb-32 sm:pt-44">
        <h1 class="text-4xl font-bold tracking-tight drop-shadow-md sm:text-5xl">
          {{ t('home.welcomeWith', { club: clubName }) }}
        </h1>
        <p class="mx-auto mt-4 max-w-xl text-lg text-white/90 drop-shadow">
          {{ t('home.tagline') }}
        </p>
        <RouterLink
          to="/login"
          class="mt-8 inline-flex items-center gap-2 rounded-full bg-white px-7 py-3 text-sm font-semibold text-slate-900 shadow-lg shadow-black/30 transition hover:-translate-y-0.5 hover:bg-slate-50 hover:shadow-xl"
        >
          <LogIn class="h-4 w-4" aria-hidden="true" />
          {{ t('home.ctaLogin') }}
        </RouterLink>
      </div>
    </section>

    <div class="mx-auto max-w-7xl px-4 py-14 sm:px-6 sm:py-20 lg:px-8">
      <section v-for="(section, idx) in sections" :key="section.titleKey" :class="idx > 0 ? 'mt-14 sm:mt-20' : ''">
        <header class="mb-6 flex items-center gap-3">
          <span class="h-px w-8 bg-slate-400" aria-hidden="true" />
          <h2 class="text-xs font-semibold uppercase tracking-[0.18em] text-slate-500">
            {{ t(section.titleKey) }}
          </h2>
        </header>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <RouterLink
            v-for="tile in section.tiles"
            :key="tile.to"
            :to="tile.to"
            class="group relative flex items-start gap-4 rounded-2xl border border-slate-200 bg-white p-5 transition duration-200 hover:-translate-y-0.5 hover:border-slate-400 hover:shadow-lg"
          >
            <div class="flex h-11 w-11 flex-none items-center justify-center rounded-xl bg-slate-100 transition group-hover:bg-slate-200">
              <component :is="tile.icon" class="h-5 w-5 text-slate-600 transition group-hover:text-slate-800" aria-hidden="true" />
            </div>
            <div class="min-w-0 flex-1">
              <h3 class="text-base font-semibold text-slate-900 transition group-hover:text-slate-700">
                {{ t(tile.title) }}
              </h3>
              <p class="mt-1 text-sm text-slate-600">
                {{ t(tile.desc) }}
              </p>
            </div>
            <ArrowRight
              class="mt-1 h-4 w-4 flex-none translate-x-0 text-slate-400 opacity-0 transition group-hover:translate-x-0.5 group-hover:opacity-100"
              aria-hidden="true"
            />
          </RouterLink>
        </div>
      </section>
    </div>

    <!-- Bli medlem: self-service registration isn't wired up yet, so point
         prospective members at the club mailbox. The wave backdrop sets off a
         high-contrast card; the section is sized to its content so the page
         ends naturally with the footer flush below. -->
    <section class="wave-bg px-4 py-14 sm:py-20">
      <div class="mx-auto max-w-2xl rounded-2xl border border-slate-200 bg-white p-8 text-center shadow-sm">
        <Anchor class="mx-auto h-8 w-8 text-slate-400" aria-hidden="true" />
        <h2 class="mt-3 text-2xl font-bold text-slate-900">{{ t('home.join.title') }}</h2>
        <p class="mt-3 text-slate-600">{{ t('home.join.comingSoon') }}</p>
        <p class="mt-2 text-slate-600">
          {{ t('home.join.contactPrefix') }}
          <a :href="`mailto:${postEmail}`" class="font-semibold text-brand-700 underline hover:text-brand-900">{{ postEmail }}</a>
          {{ t('home.join.contactSuffix') }}
        </p>
      </div>
    </section>
  </div>
</template>
