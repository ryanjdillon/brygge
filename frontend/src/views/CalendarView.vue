<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Download } from 'lucide-vue-next'

const { t } = useI18n()

const tags = [
  { key: 'regatta', label: 'calendar.filterRegatta' },
  { key: 'dugnad', label: 'calendar.filterDugnad' },
  { key: 'social', label: 'calendar.filterSocial' },
  { key: 'agm', label: 'calendar.filterAgm' },
]

const activeTags = ref<Set<string>>(new Set(tags.map((tag) => tag.key)))

function toggleTag(key: string) {
  if (activeTags.value.has(key)) {
    activeTags.value.delete(key)
  } else {
    activeTags.value.add(key)
  }
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <h1 class="text-3xl font-bold text-gray-900">{{ t('calendar.title') }}</h1>
        <p class="mt-1 text-gray-600">{{ t('calendar.description') }}</p>
      </div>
      <a
        href="/api/v1/calendar/public.ics"
        class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
      >
        <Download class="h-4 w-4" />
        {{ t('calendar.export') }}
      </a>
    </div>

    <div class="mt-6 flex flex-wrap gap-2">
      <button
        v-for="tag in tags"
        :key="tag.key"
        :class="[
          'rounded-full px-3 py-1 text-sm font-medium transition',
          activeTags.has(tag.key)
            ? 'bg-blue-600 text-white'
            : 'bg-gray-100 text-gray-600 hover:bg-gray-200',
        ]"
        @click="toggleTag(tag.key)"
      >
        {{ t(tag.label) }}
      </button>
    </div>

    <div
      id="calendar"
      class="mt-8 flex min-h-[400px] items-center justify-center rounded-lg border-2 border-dashed border-gray-300 bg-gray-50 text-gray-400"
    >
      FullCalendar integration placeholder
    </div>
  </div>
</template>
