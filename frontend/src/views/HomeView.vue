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
  HelpCircle,
} from 'lucide-vue-next'
import { useFeatures } from '@/composables/useFeatures'
import { useClubStore } from '@/stores/club'
import { useFaqs } from '@/composables/useFaqs'
import { useJoinStatus } from '@/composables/useJoinStatus'
import heroImage from '@/assets/hero.jpg'

const { t } = useI18n()
const { isEnabled } = useFeatures()
const club = useClubStore()
club.ensureLoaded()

// The FAQ tile shows only when the club has published questions.
const faqs = useFaqs()
faqs.ensureLoaded()
const faqItems = faqs.items
const hasFaq = computed(() => faqItems.value.length > 0)

const clubName = computed(() => club.name || 'Brygge')
// The club's own short description (Site settings), else the default tagline.
// Shown once the club load has settled (a failed load falls back to the
// tagline), so a custom description never flashes in over the default.
const tagline = computed(() => club.publicDescription.trim() || t('home.tagline'))
const { open: joinOpen } = useJoinStatus()

// Shared by the tiles and the join card: one lift-and-shadow hover, and a
// focus ring that follows the rounded corners (the global outline does not).
const linkCardClass =
  'group rounded-2xl border border-slate-200 bg-white transition duration-200 hover:-translate-y-0.5 hover:border-slate-400 hover:shadow-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-2'

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
        <!-- min-h-7 holds one line (text-lg) while the club loads, so the
             buttons below do not jump when the text arrives. -->
        <p class="mx-auto mt-4 min-h-7 max-w-xl text-lg text-balance text-white/90 drop-shadow" data-testid="hero-tagline">
          <template v-if="club.settled">{{ tagline }}</template>
        </p>
        <div class="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
          <RouterLink
            to="/login"
            class="inline-flex min-h-11 items-center justify-center rounded-full bg-white px-7 py-3 text-sm font-semibold text-slate-900 shadow-lg shadow-black/30 transition hover:-translate-y-0.5 hover:bg-slate-50 hover:shadow-xl"
          >
            {{ t('home.ctaLogin') }}
          </RouterLink>
          <RouterLink
            v-if="joinOpen"
            to="/join"
            class="inline-flex min-h-11 items-center justify-center rounded-full px-7 py-3 text-sm font-semibold text-white ring-1 ring-white/60 backdrop-blur-sm transition hover:-translate-y-0.5 hover:bg-white/10"
            data-testid="hero-join"
          >
            {{ t('home.ctaJoin') }}
          </RouterLink>
        </div>
      </div>
    </section>

    <div class="mx-auto max-w-7xl px-4 py-14 sm:px-6 sm:py-20 lg:px-8">
      <section v-for="(section, idx) in sections" :key="section.titleKey" :class="idx > 0 ? 'mt-12 sm:mt-16' : ''">
        <h2 class="mb-5 text-xl font-semibold text-slate-900 sm:mb-6 sm:text-2xl">
          {{ t(section.titleKey) }}
        </h2>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <RouterLink
            v-for="tile in section.tiles"
            :key="tile.to"
            :to="tile.to"
            :class="[linkCardClass, 'relative flex items-start gap-4 p-5']"
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

    <!-- Bli medlem: the whole card is one link, to the join form while the
         club takes joins, otherwise to the contact page. Its accessible name
         is its own content (heading, sentence, cue), read once. The wave backdrop
         sets off the card; the section is sized to its content so the footer
         sits flush below. -->
    <section class="wave-bg px-4 py-14 sm:px-6 sm:py-20 lg:px-8">
      <RouterLink
        :to="joinOpen ? '/join' : '/contact'"
        :class="[linkCardClass, 'mx-auto block max-w-2xl p-6 text-center shadow-sm sm:p-8']"
        data-testid="home-join"
      >
        <Anchor class="mx-auto h-8 w-8 text-slate-400 transition group-hover:text-slate-500" aria-hidden="true" />
        <h2 class="mt-3 text-xl font-semibold text-slate-900 sm:text-2xl">{{ t('join.title') }}</h2>
        <p class="mx-auto mt-3 max-w-prose text-pretty text-slate-600">
          {{ joinOpen ? t('home.joinCardBody') : t('join.closed.body') }}
        </p>
        <span
          class="mt-5 inline-flex items-center gap-1.5 text-sm font-semibold text-brand-700 transition group-hover:text-brand-800"
        >
          {{ joinOpen ? t('home.joinCardCue') : t('join.closed.contact') }}
          <ArrowRight class="h-4 w-4 transition group-hover:translate-x-0.5" aria-hidden="true" />
        </span>
      </RouterLink>
    </section>
  </div>
</template>
