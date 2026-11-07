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
  HelpCircle,
  UserPlus,
} from 'lucide-vue-next'
import { useFeatures } from '@/composables/useFeatures'
import { useClubStore } from '@/stores/club'
import { useFaqs } from '@/composables/useFaqs'
import { useJoinStatus } from '@/composables/useJoinStatus'
import { formatNOK } from '@/lib/format'
import FaqList from '@/components/FaqList.vue'
import heroImage from '@/assets/hero.jpg'

const { t } = useI18n()
const { isEnabled } = useFeatures()
const club = useClubStore()
club.ensureLoaded()

const { items: faqItems } = useFaqs()
useFaqs().ensureLoaded()
const hasFaq = computed(() => faqItems.value.length > 0)
// A short, broadly useful subset for the teaser.
const featuredFaqs = computed(() => faqItems.value.slice(0, 5))

const clubName = computed(() => club.name || 'Brygge')
const { open: joinOpen, price: joinPrice, year: joinYear } = useJoinStatus()

type FeatureFlag = 'bookings' | 'projects' | 'calendar' | 'commerce' | 'accounting'

interface Tile {
  icon: typeof Anchor
  title: string
  desc: string
  to: string
  feature?: FeatureFlag
  requiresFaq?: boolean
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
        { icon: HelpCircle, title: 'home.featureFaq', desc: 'home.featureFaqDesc', to: '/faq', requiresFaq: true },
        { icon: ShoppingBag, title: 'home.featureMerchandise', desc: 'home.featureMerchandiseDesc', to: '/merchandise', feature: 'commerce' },
      ],
    },
  ]
  return all
    .map((s) => ({
      ...s,
      tiles: s.tiles.filter(
        (tile) => (!tile.feature || isEnabled(tile.feature)) && (!tile.requiresFaq || hasFaq.value),
      ),
    }))
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
        <div class="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
          <RouterLink
            to="/login"
            class="inline-flex min-h-11 items-center gap-2 rounded-full bg-white px-7 py-3 text-sm font-semibold text-slate-900 shadow-lg shadow-black/30 transition hover:-translate-y-0.5 hover:bg-slate-50 hover:shadow-xl"
          >
            <LogIn class="h-4 w-4" aria-hidden="true" />
            {{ t('home.ctaLogin') }}
          </RouterLink>
          <RouterLink
            v-if="joinOpen"
            to="/join"
            class="inline-flex min-h-11 items-center gap-2 rounded-full px-7 py-3 text-sm font-semibold text-white ring-1 ring-white/60 backdrop-blur-sm transition hover:-translate-y-0.5 hover:bg-white/10"
            data-testid="hero-join"
          >
            <UserPlus class="h-4 w-4" aria-hidden="true" />
            {{ t('home.ctaJoin') }}
          </RouterLink>
        </div>
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

    <!-- FAQ teaser: a short, broadly useful subset with a link through to
         the full page. Keeps the common questions one click from the home
         page without lengthening it with the whole list. -->
    <section v-if="hasFaq" class="mx-auto max-w-3xl px-4 pb-4 sm:px-6 lg:px-8">
      <header class="mb-6 flex items-center justify-between gap-4">
        <div class="flex items-center gap-3">
          <span class="h-px w-8 bg-slate-400" aria-hidden="true" />
          <h2 class="text-xs font-semibold uppercase tracking-[0.18em] text-slate-500">
            {{ t('faq.home.title') }}
          </h2>
        </div>
        <RouterLink
          to="/faq"
          class="inline-flex items-center gap-1 text-sm font-semibold text-teal-700 hover:text-teal-800 hover:underline"
        >
          {{ t('faq.seeAll') }}
          <ArrowRight class="h-4 w-4" aria-hidden="true" />
        </RouterLink>
      </header>
      <FaqList :items="featuredFaqs" />
    </section>

    <!-- Bli medlem: the join form while the club takes joins, otherwise the
         contact page. The wave backdrop sets off a high-contrast card; the
         section is sized to its content so the footer sits flush below. -->
    <section class="wave-bg px-4 py-14 sm:py-20">
      <div class="mx-auto max-w-2xl rounded-2xl border border-slate-200 bg-white p-6 text-center shadow-sm sm:p-8" data-testid="home-join">
        <Anchor class="mx-auto h-8 w-8 text-slate-400" aria-hidden="true" />
        <h2 class="mt-3 text-2xl font-bold text-slate-900">{{ t('join.title') }}</h2>
        <template v-if="joinOpen">
          <p v-if="joinPrice" class="mt-3 text-slate-600" data-testid="home-join-price">
            {{ t('join.form.price', { year: joinYear, amount: formatNOK(joinPrice.amount) }) }}
          </p>
          <RouterLink
            to="/join"
            class="mt-5 inline-flex min-h-11 w-full items-center justify-center gap-2 rounded-md bg-brand-600 px-5 py-2 text-sm font-semibold text-white hover:bg-brand-700 sm:w-auto"
          >
            <UserPlus class="h-4 w-4" aria-hidden="true" />
            {{ t('nav.join') }}
          </RouterLink>
        </template>
        <template v-else>
          <p class="mt-3 text-slate-600">{{ t('join.closed.body') }}</p>
          <RouterLink
            to="/contact"
            class="mt-5 inline-flex min-h-11 w-full items-center justify-center rounded-md border border-slate-300 px-5 py-2 text-sm font-semibold text-slate-700 hover:bg-slate-50 sm:w-auto"
          >
            {{ t('join.closed.contact') }}
          </RouterLink>
        </template>
      </div>
    </section>
  </div>
</template>
