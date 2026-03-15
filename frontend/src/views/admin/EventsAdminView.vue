<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, Pencil, Trash2, Check, X } from 'lucide-vue-next'
import {
  useEvents,
  useCreateEvent,
  useUpdateEvent,
  useDeleteEvent,
  type CalendarEvent,
  type CreateEventPayload,
} from '@/composables/useEvents'

const { t } = useI18n()

const { data: events, isLoading, error } = useEvents()
const createMutation = useCreateEvent()
const updateMutation = useUpdateEvent()
const deleteMutation = useDeleteEvent()

const showModal = ref(false)
const editingEvent = ref<CalendarEvent | null>(null)

const tagOptions = ['regatta', 'volunteer', 'social', 'agm', 'other'] as const

const form = ref<CreateEventPayload>({
  title: '',
  description: '',
  location: '',
  start_time: '',
  end_time: '',
  tag: 'other',
  is_public: true,
})

function openCreateModal() {
  editingEvent.value = null
  form.value = {
    title: '',
    description: '',
    location: '',
    start_time: '',
    end_time: '',
    tag: 'other',
    is_public: true,
  }
  showModal.value = true
}

function openEditModal(event: CalendarEvent) {
  editingEvent.value = event
  form.value = {
    title: event.title,
    description: event.description,
    location: event.location,
    start_time: event.start_time.slice(0, 16),
    end_time: event.end_time.slice(0, 16),
    tag: event.tag,
    is_public: event.is_public,
  }
  showModal.value = true
}

function closeModal() {
  showModal.value = false
  editingEvent.value = null
}

const isSubmitting = computed(
  () => createMutation.isPending.value || updateMutation.isPending.value,
)

async function handleSubmit() {
  const payload = {
    ...form.value,
    start_time: new Date(form.value.start_time).toISOString(),
    end_time: new Date(form.value.end_time).toISOString(),
  }

  if (editingEvent.value) {
    await updateMutation.mutateAsync({ id: editingEvent.value.id, ...payload })
  } else {
    await createMutation.mutateAsync(payload)
  }
  closeModal()
}

async function handleDelete(id: string) {
  if (!confirm(t('common.confirm'))) return
  await deleteMutation.mutateAsync(id)
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('nb-NO', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function tagLabel(tag: string): string {
  const map: Record<string, string> = {
    regatta: t('calendar.filterRegatta'),
    volunteer: t('calendar.filterVolunteer'),
    social: t('calendar.filterSocial'),
    agm: t('calendar.filterAgm'),
    other: t('admin.events.tagOther'),
  }
  return map[tag] ?? tag
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.events.title') }}</h1>
      <button
        class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        @click="openCreateModal"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.events.create') }}
      </button>
    </div>

    <div v-if="isLoading" class="mt-8 text-center text-gray-500">
      {{ t('common.loading') }}...
    </div>

    <div v-else-if="error" class="mt-8 text-center text-red-600">
      {{ t('common.error') }}
    </div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.events.eventTitle') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.events.date') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.events.tag') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.events.visibility') }}
            </th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('common.actions') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-if="!events?.length">
            <td colspan="5" class="px-4 py-8 text-center text-gray-500">
              {{ t('common.noResults') }}
            </td>
          </tr>
          <tr v-for="event in events" :key="event.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">
              {{ event.title }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ formatDate(event.start_time) }}
            </td>
            <td class="px-4 py-3">
              <span class="inline-flex rounded-full bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-800">
                {{ tagLabel(event.tag) }}
              </span>
            </td>
            <td class="px-4 py-3 text-sm text-gray-600">
              {{ event.is_public ? t('admin.events.public') : t('admin.events.private') }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right">
              <button
                class="mr-2 text-gray-500 hover:text-blue-600"
                :title="t('common.edit')"
                @click="openEditModal(event)"
              >
                <Pencil class="h-4 w-4" />
              </button>
              <button
                class="text-gray-500 hover:text-red-600"
                :title="t('common.delete')"
                @click="handleDelete(event.id)"
              >
                <Trash2 class="h-4 w-4" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Teleport to="body">
      <div
        v-if="showModal"
        role="dialog" aria-modal="true" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
        @click.self="closeModal"
      >
        <div class="w-full max-w-lg rounded-lg bg-white p-6 shadow-xl">
          <div class="flex items-center justify-between">
            <h2 class="text-lg font-semibold text-gray-900">
              {{ editingEvent ? t('admin.events.edit') : t('admin.events.create') }}
            </h2>
            <button class="text-gray-400 hover:text-gray-600" @click="closeModal">
              <X class="h-5 w-5" />
            </button>
          </div>

          <form class="mt-4 space-y-4" @submit.prevent="handleSubmit">
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.eventTitle') }}</label>
              <input
                v-model="form.title"
                type="text"
                required
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.description') }}</label>
              <textarea
                v-model="form.description"
                rows="3"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.location') }}</label>
              <input
                v-model="form.location"
                type="text"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>

            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.startTime') }}</label>
                <input
                  v-model="form.start_time"
                  type="datetime-local"
                  required
                  class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.endTime') }}</label>
                <input
                  v-model="form.end_time"
                  type="datetime-local"
                  required
                  class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </div>

            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="block text-sm font-medium text-gray-700">{{ t('admin.events.tag') }}</label>
                <select
                  v-model="form.tag"
                  class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                >
                  <option v-for="tag in tagOptions" :key="tag" :value="tag">
                    {{ tagLabel(tag) }}
                  </option>
                </select>
              </div>
              <div class="flex items-end">
                <label class="flex items-center gap-2 text-sm font-medium text-gray-700">
                  <input
                    v-model="form.is_public"
                    type="checkbox"
                    class="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  />
                  {{ t('admin.events.isPublic') }}
                </label>
              </div>
            </div>

            <div class="flex justify-end gap-3 pt-2">
              <button
                type="button"
                class="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
                @click="closeModal"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                type="submit"
                :disabled="isSubmitting"
                class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
              >
                {{ isSubmitting ? t('common.loading') + '...' : t('common.save') }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Teleport>
  </div>
</template>
