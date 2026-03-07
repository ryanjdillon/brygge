<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useEvents, type CalendarEvent } from '@/composables/useEvents'
import { Download, Plus, X } from 'lucide-vue-next'

const { t } = useI18n()
const auth = useAuthStore()

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

const { data: events, isLoading } = useEvents()

const filteredEvents = computed(() => {
  if (!events.value) return []
  return events.value.filter((e) => activeTags.value.has(e.tag) || !tags.some((t) => t.key === e.tag))
})

const showProposeModal = ref(false)
const proposeForm = ref({
  title: '',
  description: '',
  location: '',
  start_time: '',
  end_time: '',
  tag: 'social',
})

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('nb-NO', {
    weekday: 'long',
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function tagColor(tag: string): string {
  switch (tag) {
    case 'regatta': return 'bg-blue-100 text-blue-800'
    case 'dugnad': return 'bg-orange-100 text-orange-800'
    case 'social': return 'bg-green-100 text-green-800'
    case 'agm': return 'bg-purple-100 text-purple-800'
    default: return 'bg-gray-100 text-gray-800'
  }
}

function tagLabel(tag: string): string {
  const map: Record<string, string> = {
    regatta: t('calendar.filterRegatta'),
    dugnad: t('calendar.filterDugnad'),
    social: t('calendar.filterSocial'),
    agm: t('calendar.filterAgm'),
  }
  return map[tag] ?? tag
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <h1 class="text-3xl font-bold text-gray-900">{{ t('calendar.title') }}</h1>
        <p class="mt-1 text-gray-600">{{ t('calendar.description') }}</p>
      </div>
      <div class="flex gap-2">
        <button
          v-if="auth.isAuthenticated"
          class="inline-flex items-center gap-2 rounded-md border border-blue-600 px-4 py-2 text-sm font-medium text-blue-600 hover:bg-blue-50"
          @click="showProposeModal = true"
        >
          <Plus class="h-4 w-4" />
          {{ t('calendar.proposeEvent') }}
        </button>
        <a
          href="/api/v1/calendar/public.ics"
          class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        >
          <Download class="h-4 w-4" />
          {{ t('calendar.export') }}
        </a>
      </div>
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

    <div v-if="isLoading" class="mt-8 text-center text-gray-500">
      {{ t('common.loading') }}...
    </div>

    <div v-else-if="filteredEvents.length === 0" class="mt-8 text-center text-gray-400">
      {{ t('common.noResults') }}
    </div>

    <div v-else class="mt-8 space-y-4">
      <div
        v-for="event in filteredEvents"
        :key="event.id"
        class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm"
      >
        <div class="flex items-start justify-between">
          <div>
            <div class="flex items-center gap-2">
              <h3 class="text-lg font-semibold text-gray-900">{{ event.title }}</h3>
              <span
                :class="['inline-flex rounded-full px-2 py-0.5 text-xs font-medium', tagColor(event.tag)]"
              >
                {{ tagLabel(event.tag) }}
              </span>
            </div>
            <p class="mt-1 text-sm text-gray-500">{{ formatDate(event.start_time) }}</p>
            <p v-if="event.location" class="mt-1 text-sm text-gray-500">{{ event.location }}</p>
          </div>
        </div>
        <p v-if="event.description" class="mt-2 text-sm text-gray-600">{{ event.description }}</p>
      </div>
    </div>

    <Teleport to="body">
      <div
        v-if="showProposeModal"
        role="dialog" aria-modal="true" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
        @click.self="showProposeModal = false"
      >
        <div class="w-full max-w-lg rounded-lg bg-white p-6 shadow-xl">
          <div class="flex items-center justify-between">
            <h2 class="text-lg font-semibold text-gray-900">{{ t('calendar.proposeEvent') }}</h2>
            <button class="text-gray-400 hover:text-gray-600" @click="showProposeModal = false">
              <X class="h-5 w-5" />
            </button>
          </div>
          <p class="mt-1 text-sm text-gray-500">{{ t('calendar.proposeDescription') }}</p>

          <form class="mt-4 space-y-4" @submit.prevent="showProposeModal = false">
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.eventTitle') }}</label>
              <input
                v-model="proposeForm.title"
                type="text"
                required
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.description') }}</label>
              <textarea
                v-model="proposeForm.description"
                rows="3"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.startTime') }}</label>
                <input
                  v-model="proposeForm.start_time"
                  type="datetime-local"
                  required
                  class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.endTime') }}</label>
                <input
                  v-model="proposeForm.end_time"
                  type="datetime-local"
                  required
                  class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </div>
            <div class="flex justify-end gap-3 pt-2">
              <button
                type="button"
                class="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
                @click="showProposeModal = false"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                type="submit"
                class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
              >
                {{ t('common.submit') }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Teleport>
  </div>
</template>
