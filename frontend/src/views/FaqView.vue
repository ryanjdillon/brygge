<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { HelpCircle } from 'lucide-vue-next'
import { groupFaqItems, faqGroupLabelKey } from '@/lib/faqData'
import { useFaqs } from '@/composables/useFaqs'
import FaqList from '@/components/FaqList.vue'

const { t } = useI18n()
const { items } = useFaqs()
useFaqs().ensureLoaded()

const groups = computed(() => groupFaqItems(items.value))
</script>

<template>
  <div class="mx-auto max-w-3xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-slate-900">{{ t('faq.title') }}</h1>
    <p class="mt-2 text-slate-600">{{ t('faq.intro') }}</p>

    <section
      v-for="(group, idx) in groups"
      :key="group.key"
      :class="idx > 0 ? 'mt-12' : 'mt-10'"
    >
      <header class="mb-4 flex items-center gap-3">
        <span class="h-px w-8 bg-slate-400" aria-hidden="true" />
        <h2 class="text-xs font-semibold uppercase tracking-[0.18em] text-slate-500">
          {{ t(faqGroupLabelKey(group.key)) }}
        </h2>
      </header>
      <FaqList :items="group.items" />
    </section>

    <!-- Fallback for anything not covered: point at the board rather than
         guess. Also the empty state when a club has no published FAQ yet. -->
    <div class="mt-12 rounded-2xl border border-slate-200 bg-slate-50 p-6 text-center">
      <HelpCircle class="mx-auto h-7 w-7 text-slate-400" aria-hidden="true" />
      <p class="mt-3 font-semibold text-slate-900">{{ t('faq.notFound') }}</p>
      <RouterLink
        to="/contact"
        class="mt-3 inline-flex items-center rounded-full bg-teal-600 px-5 py-2 text-sm font-semibold text-white transition hover:bg-teal-700"
      >
        {{ t('faq.contactCta') }}
      </RouterLink>
    </div>
  </div>
</template>
