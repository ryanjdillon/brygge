<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { useMyDugnadHours } from '@/composables/useDugnad'
import { useProjects } from '@/composables/useProjects'
import { FolderKanban, TrendingUp, TrendingDown } from 'lucide-vue-next'

const { t } = useI18n()
const { data: hours, isLoading: hoursLoading } = useMyDugnadHours()
const { data: projects, isLoading: projectsLoading } = useProjects()
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('dugnad.title') }}</h1>

    <div v-if="hoursLoading" class="mt-4 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else-if="hours" class="mt-6 grid gap-4 sm:grid-cols-4">
      <div class="rounded-lg border border-gray-200 bg-white p-4">
        <div class="text-sm text-gray-500">{{ t('dugnad.signedUpHours') }}</div>
        <div class="mt-1 text-2xl font-bold text-blue-600">{{ hours.signed_up_hours }}{{ t('dugnad.hoursUnit') }}</div>
      </div>
      <div class="rounded-lg border border-gray-200 bg-white p-4">
        <div class="text-sm text-gray-500">{{ t('dugnad.completedHours') }}</div>
        <div class="mt-1 text-2xl font-bold text-green-600">{{ hours.completed_hours }}{{ t('dugnad.hoursUnit') }}</div>
      </div>
      <div class="rounded-lg border border-gray-200 bg-white p-4">
        <div class="text-sm text-gray-500">{{ t('dugnad.requiredHours') }}</div>
        <div class="mt-1 text-2xl font-bold text-gray-900">{{ hours.required_hours }}{{ t('dugnad.hoursUnit') }}</div>
      </div>
      <div class="rounded-lg border border-gray-200 bg-white p-4">
        <div class="text-sm text-gray-500">{{ hours.remaining > 0 ? t('dugnad.remaining') : t('dugnad.surplus') }}</div>
        <div :class="['mt-1 flex items-center gap-1 text-2xl font-bold', hours.remaining > 0 ? 'text-orange-600' : 'text-green-600']">
          <TrendingDown v-if="hours.remaining > 0" class="h-5 w-5" />
          <TrendingUp v-else class="h-5 w-5" />
          {{ Math.abs(hours.remaining) }}{{ t('dugnad.hoursUnit') }}
        </div>
      </div>
    </div>

    <h2 class="mt-8 text-lg font-semibold text-gray-900">{{ t('projects.title') }}</h2>

    <div v-if="projectsLoading" class="mt-4 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else-if="!projects?.length" class="mt-4 text-gray-500">{{ t('projects.noProjects') }}</div>

    <div v-else class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
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
        <div class="mt-3 flex gap-3 text-xs">
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
  </div>
</template>
