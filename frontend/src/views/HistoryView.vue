<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { Landmark, Award, Users, Anchor } from 'lucide-vue-next'

const { t } = useI18n()

const milestones = [
  { year: '1952', icon: Anchor, titleKey: 'history.founded', descKey: 'history.foundedDesc' },
  { year: '1968', icon: Landmark, titleKey: 'history.clubhouse', descKey: 'history.clubhouseDesc' },
  { year: '1985', icon: Award, titleKey: 'history.expansion', descKey: 'history.expansionDesc' },
  { year: '2010', icon: Users, titleKey: 'history.modernisation', descKey: 'history.modernisationDesc' },
]
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-gray-900">{{ t('history.title') }}</h1>
    <p class="mt-2 max-w-2xl text-gray-600">{{ t('history.intro') }}</p>

    <!-- Timeline -->
    <div class="relative mt-12">
      <div class="absolute left-8 top-0 hidden h-full w-px bg-gray-200 sm:block" />

      <div class="space-y-12">
        <div v-for="milestone in milestones" :key="milestone.year" class="relative flex gap-6">
          <div class="relative z-10 flex h-16 w-16 shrink-0 items-center justify-center rounded-full border-2 border-blue-600 bg-white">
            <component :is="milestone.icon" class="h-6 w-6 text-blue-600" aria-hidden="true" />
          </div>
          <div class="pt-2">
            <span class="text-sm font-semibold text-blue-600">{{ milestone.year }}</span>
            <h2 class="mt-1 text-lg font-semibold text-gray-900">{{ t(milestone.titleKey) }}</h2>
            <p class="mt-1 text-gray-600">{{ t(milestone.descKey) }}</p>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
