<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useProjects, useCreateProject } from '@/composables/useProjects'
import { Plus, FolderKanban, X } from 'lucide-vue-next'

const { t } = useI18n()

const { data: projects, isLoading, isError } = useProjects()
const createProject = useCreateProject()

const showModal = ref(false)
const newName = ref('')
const newDescription = ref('')

const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

function openModal() {
  newName.value = ''
  newDescription.value = ''
  showModal.value = true
}

function handleCreate() {
  if (!newName.value.trim()) return
  createProject.mutate(
    { name: newName.value.trim(), description: newDescription.value.trim() },
    {
      onSuccess: () => {
        showModal.value = false
        showToast('success', t('projects.createSuccess'))
      },
      onError: () => {
        showToast('error', t('projects.createError'))
      },
    },
  )
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('projects.title') }}</h1>
      <button
        class="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
        @click="openModal"
      >
        <Plus class="h-4 w-4" />
        {{ t('projects.createProject') }}
      </button>
    </div>

    <div
      v-if="toast"
      :class="[
        'mt-4 rounded-md p-3 text-sm',
        toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800',
      ]"
    >
      {{ toast.message }}
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">
      {{ t('projects.loadError') }}
    </div>

    <div v-else-if="!projects?.length" class="mt-6 text-gray-500">
      {{ t('projects.noProjects') }}
    </div>

    <div v-else class="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <RouterLink
        v-for="project in projects"
        :key="project.id"
        :to="`/admin/projects/${project.id}`"
        class="block rounded-lg border border-gray-200 bg-white p-5 shadow-sm transition hover:shadow-md"
      >
        <div class="flex items-start gap-3">
          <FolderKanban class="mt-0.5 h-5 w-5 text-blue-600" />
          <div class="flex-1">
            <h3 class="font-semibold text-gray-900">{{ project.name }}</h3>
            <p v-if="project.description" class="mt-1 text-sm text-gray-500 line-clamp-2">
              {{ project.description }}
            </p>
          </div>
        </div>
        <div class="mt-4 flex gap-3 text-xs">
          <span class="rounded-full bg-gray-100 px-2 py-0.5 text-gray-600">
            {{ t('projects.statusTodo') }}: {{ project.todo_count }}
          </span>
          <span class="rounded-full bg-blue-100 px-2 py-0.5 text-blue-700">
            {{ t('projects.statusInProgress') }}: {{ project.in_progress_count }}
          </span>
          <span class="rounded-full bg-green-100 px-2 py-0.5 text-green-700">
            {{ t('projects.statusDone') }}: {{ project.done_count }}
          </span>
        </div>
      </RouterLink>
    </div>

    <div
      v-if="showModal"
      role="dialog" aria-modal="true" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      @click.self="showModal = false"
    >
      <div class="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">{{ t('projects.createProject') }}</h2>
          <button class="text-gray-400 hover:text-gray-600" @click="showModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>
        <form class="mt-4 space-y-4" @submit.prevent="handleCreate">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('projects.name') }}</label>
            <input
              v-model="newName"
              type="text"
              required
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('projects.description') }}</label>
            <textarea
              v-model="newDescription"
              rows="3"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div class="flex justify-end gap-3">
            <button
              type="button"
              class="rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              @click="showModal = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              :disabled="createProject.isPending.value"
              class="rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
            >
              {{ t('common.save') }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>
