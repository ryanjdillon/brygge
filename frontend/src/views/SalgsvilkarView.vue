<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClubStore } from '@/stores/club'

const { t } = useI18n()
const club = useClubStore()
club.ensureLoaded()

const lastUpdated = '2026-06-09'

// Each section maps to one of the seven mandatory headings Vipps
// requires (Parter, Betaling, Levering, Angrerett, Retur,
// Reklamasjonshåndtering, Konfliktløsning). The body copy is the
// seed default; per-club overrides come in a follow-up admin view.
const sections = computed(() => [
  { id: 'parter', title: t('salgsvilkar.parter.title'), body: t('salgsvilkar.parter.body', { club: club.name || 'Klubben', orgNumber: club.orgNumber || '—' }) },
  { id: 'betaling', title: t('salgsvilkar.betaling.title'), body: t('salgsvilkar.betaling.body') },
  { id: 'levering', title: t('salgsvilkar.levering.title'), body: t('salgsvilkar.levering.body') },
  { id: 'angrerett', title: t('salgsvilkar.angrerett.title'), body: t('salgsvilkar.angrerett.body') },
  { id: 'retur', title: t('salgsvilkar.retur.title'), body: t('salgsvilkar.retur.body') },
  { id: 'reklamasjon', title: t('salgsvilkar.reklamasjon.title'), body: t('salgsvilkar.reklamasjon.body') },
  { id: 'konflikt', title: t('salgsvilkar.konflikt.title'), body: t('salgsvilkar.konflikt.body') },
])
</script>

<template>
  <main class="mx-auto max-w-3xl px-4 py-10 sm:px-6 lg:px-8">
    <header class="border-b border-gray-200 pb-6">
      <p class="text-xs font-medium uppercase tracking-wide text-gray-500">{{ t('salgsvilkar.eyebrow') }}</p>
      <h1 class="mt-2 text-3xl font-bold text-gray-900">{{ t('salgsvilkar.title') }}</h1>
      <p class="mt-3 text-sm text-gray-600">{{ t('salgsvilkar.intro', { club: club.name || 'klubben' }) }}</p>
      <p class="mt-2 text-xs text-gray-400">{{ t('salgsvilkar.lastUpdated', { date: lastUpdated }) }}</p>
    </header>

    <nav class="mt-6 rounded-md border border-gray-200 bg-gray-50 p-4 text-sm" aria-label="Table of contents">
      <p class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">{{ t('salgsvilkar.toc') }}</p>
      <ol class="grid grid-cols-1 gap-y-1 text-blue-700 sm:grid-cols-2">
        <li v-for="(s, idx) in sections" :key="s.id">
          <a :href="`#${s.id}`" class="hover:underline">{{ idx + 1 }}. {{ s.title }}</a>
        </li>
      </ol>
    </nav>

    <article class="mt-8 space-y-10 text-gray-800 leading-relaxed">
      <section v-for="(s, idx) in sections" :id="s.id" :key="s.id" class="scroll-mt-24">
        <h2 class="text-xl font-semibold text-gray-900">{{ idx + 1 }}. {{ s.title }}</h2>
        <div class="prose prose-sm mt-3 max-w-none whitespace-pre-line">{{ s.body }}</div>
      </section>
    </article>

    <footer class="mt-12 rounded-md border border-gray-200 bg-gray-50 p-5 text-sm text-gray-700">
      <p class="font-medium text-gray-900">{{ t('salgsvilkar.contactHeading') }}</p>
      <dl class="mt-3 grid grid-cols-1 gap-y-1 sm:grid-cols-2 sm:gap-x-6">
        <div class="flex gap-2"><dt class="text-gray-500 w-24 shrink-0">{{ t('salgsvilkar.fields.name') }}</dt><dd class="font-medium">{{ club.name || '—' }}</dd></div>
        <div class="flex gap-2"><dt class="text-gray-500 w-24 shrink-0">{{ t('salgsvilkar.fields.orgNumber') }}</dt><dd>{{ club.orgNumber || '—' }}</dd></div>
        <div v-if="club.address" class="flex gap-2 sm:col-span-2"><dt class="text-gray-500 w-24 shrink-0">{{ t('salgsvilkar.fields.address') }}</dt><dd class="whitespace-pre-line">{{ club.address }}</dd></div>
        <div v-if="club.phone" class="flex gap-2"><dt class="text-gray-500 w-24 shrink-0">{{ t('salgsvilkar.fields.phone') }}</dt><dd>{{ club.phone }}</dd></div>
        <div v-if="club.treasurerEmail || club.chairmanEmail" class="flex gap-2"><dt class="text-gray-500 w-24 shrink-0">{{ t('salgsvilkar.fields.email') }}</dt><dd>{{ club.treasurerEmail || club.chairmanEmail }}</dd></div>
      </dl>
    </footer>
  </main>
</template>
