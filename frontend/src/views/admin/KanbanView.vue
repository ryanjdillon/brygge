<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  useProject,
  useProjectTasks,
  useCreateTask,
  useUpdateTask,
  useDeleteTask,
  type Task,
} from '@/composables/useProjects'
import { ArrowLeft, ArrowRight, Plus, X, Trash2, Pencil } from 'lucide-vue-next'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const projectId = computed(() => route.params.projectId as string)
const { data: project } = useProject(() => projectId.value)
const { data: tasks, isLoading, isError } = useProjectTasks(() => projectId.value)
const createTask = useCreateTask(() => projectId.value)
const updateTask = useUpdateTask()
const deleteTask = useDeleteTask()

const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)
const showCreateModal = ref(false)
const createForStatus = ref<'todo' | 'in_progress' | 'done'>('todo')
const showDetailModal = ref(false)
const selectedTask = ref<Task | null>(null)

const newTitle = ref('')
const newDescription = ref('')
const newPriority = ref('medium')
const newAssigneeId = ref('')
const newDueDate = ref('')

const editTitle = ref('')
const editDescription = ref('')
const editPriority = ref('')
const editAssigneeId = ref('')
const editDueDate = ref('')

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const columns = computed(() => [
  { key: 'todo' as const, label: t('projects.statusTodo'), tasks: tasks.value?.todo ?? [], color: 'gray' },
  { key: 'in_progress' as const, label: t('projects.statusInProgress'), tasks: tasks.value?.in_progress ?? [], color: 'blue' },
  { key: 'done' as const, label: t('projects.statusDone'), tasks: tasks.value?.done ?? [], color: 'green' },
])

const priorityClasses: Record<string, string> = {
  low: 'bg-gray-100 text-gray-600',
  medium: 'bg-yellow-100 text-yellow-800',
  high: 'bg-red-100 text-red-800',
}

const columnHeaderClasses: Record<string, string> = {
  gray: 'bg-gray-100 text-gray-700',
  blue: 'bg-blue-100 text-blue-700',
  green: 'bg-green-100 text-green-700',
}

const statusOrder: ('todo' | 'in_progress' | 'done')[] = ['todo', 'in_progress', 'done']

function openCreateModal(status: 'todo' | 'in_progress' | 'done') {
  createForStatus.value = status
  newTitle.value = ''
  newDescription.value = ''
  newPriority.value = 'medium'
  newAssigneeId.value = ''
  newDueDate.value = ''
  showCreateModal.value = true
}

function handleCreate() {
  if (!newTitle.value.trim()) return
  createTask.mutate(
    {
      title: newTitle.value.trim(),
      description: newDescription.value.trim(),
      priority: newPriority.value,
      assignee_id: newAssigneeId.value || undefined,
      due_date: newDueDate.value || undefined,
    },
    {
      onSuccess: (task) => {
        if (createForStatus.value !== 'todo') {
          updateTask.mutate({
            taskId: task.id,
            input: { status: createForStatus.value },
          })
        }
        showCreateModal.value = false
        showToast('success', t('projects.taskCreated'))
      },
      onError: () => {
        showToast('error', t('projects.taskCreateError'))
      },
    },
  )
}

function openDetail(task: Task) {
  selectedTask.value = task
  editTitle.value = task.title
  editDescription.value = task.description
  editPriority.value = task.priority
  editAssigneeId.value = task.assignee_id ?? ''
  editDueDate.value = task.due_date ?? ''
  showDetailModal.value = true
}

function handleSaveDetail() {
  if (!selectedTask.value || !editTitle.value.trim()) return
  updateTask.mutate(
    {
      taskId: selectedTask.value.id,
      input: {
        title: editTitle.value.trim(),
        description: editDescription.value.trim(),
        priority: editPriority.value,
        assignee_id: editAssigneeId.value || '',
        due_date: editDueDate.value || '',
      },
    },
    {
      onSuccess: () => {
        showDetailModal.value = false
        showToast('success', t('projects.taskUpdated'))
      },
      onError: () => {
        showToast('error', t('projects.taskUpdateError'))
      },
    },
  )
}

function handleDelete(taskId: string) {
  if (!confirm(t('projects.taskDeleteConfirm'))) return
  deleteTask.mutate(taskId, {
    onSuccess: () => {
      showDetailModal.value = false
      showToast('success', t('projects.taskDeleted'))
    },
    onError: () => {
      showToast('error', t('projects.taskDeleteError'))
    },
  })
}

function moveTask(task: Task, direction: 'prev' | 'next') {
  const currentIdx = statusOrder.indexOf(task.status)
  const newIdx = direction === 'next' ? currentIdx + 1 : currentIdx - 1
  if (newIdx < 0 || newIdx >= statusOrder.length) return
  updateTask.mutate({
    taskId: task.id,
    input: { status: statusOrder[newIdx] },
  })
}
</script>

<template>
  <div>
    <div class="flex items-center gap-3">
      <button
        class="rounded-md p-1.5 text-gray-400 hover:bg-gray-100 hover:text-gray-600"
        @click="router.push('/admin/projects')"
      >
        <ArrowLeft class="h-5 w-5" />
      </button>
      <h1 class="text-2xl font-bold text-gray-900">
        {{ project?.name ?? t('common.loading') }}
      </h1>
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

    <div v-else class="mt-6 grid grid-cols-1 gap-4 lg:grid-cols-3">
      <div
        v-for="col in columns"
        :key="col.key"
        class="flex flex-col rounded-lg border border-gray-200 bg-gray-50"
      >
        <div :class="['flex items-center justify-between rounded-t-lg px-4 py-3 text-sm font-semibold', columnHeaderClasses[col.color]]">
          <span>{{ col.label }} ({{ col.tasks.length }})</span>
          <button
            class="rounded p-1 hover:bg-white/50"
            @click="openCreateModal(col.key)"
          >
            <Plus class="h-4 w-4" />
          </button>
        </div>

        <div class="flex-1 space-y-2 p-3">
          <div
            v-for="task in col.tasks"
            :key="task.id"
            class="cursor-pointer rounded-md border border-gray-200 bg-white p-3 shadow-sm transition hover:shadow-md"
            @click="openDetail(task)"
          >
            <div class="flex items-start justify-between gap-2">
              <span class="text-sm font-medium text-gray-900">{{ task.title }}</span>
              <span :class="['shrink-0 rounded-full px-2 py-0.5 text-xs font-medium', priorityClasses[task.priority]]">
                {{ t(`projects.priority${task.priority.charAt(0).toUpperCase() + task.priority.slice(1)}`) }}
              </span>
            </div>
            <div v-if="task.due_date" class="mt-1.5 text-xs text-gray-500">
              {{ task.due_date }}
            </div>
            <div class="mt-2 flex gap-1">
              <button
                v-if="statusOrder.indexOf(task.status) > 0"
                class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600"
                :title="t('projects.movePrev')"
                @click.stop="moveTask(task, 'prev')"
              >
                <ArrowLeft class="h-3.5 w-3.5" />
              </button>
              <button
                v-if="statusOrder.indexOf(task.status) < statusOrder.length - 1"
                class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600"
                :title="t('projects.moveNext')"
                @click.stop="moveTask(task, 'next')"
              >
                <ArrowRight class="h-3.5 w-3.5" />
              </button>
            </div>
          </div>

          <div v-if="!col.tasks.length" class="py-8 text-center text-sm text-gray-400">
            {{ t('projects.noTasks') }}
          </div>
        </div>
      </div>
    </div>

    <!-- Create task modal -->
    <div
      v-if="showCreateModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      @click.self="showCreateModal = false"
    >
      <div class="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">{{ t('projects.createTask') }}</h2>
          <button class="text-gray-400 hover:text-gray-600" @click="showCreateModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>
        <form class="mt-4 space-y-4" @submit.prevent="handleCreate">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('projects.taskTitle') }}</label>
            <input
              v-model="newTitle"
              type="text"
              required
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('projects.taskDescription') }}</label>
            <textarea
              v-model="newDescription"
              rows="3"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('projects.taskPriority') }}</label>
              <select
                v-model="newPriority"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="low">{{ t('projects.priorityLow') }}</option>
                <option value="medium">{{ t('projects.priorityMedium') }}</option>
                <option value="high">{{ t('projects.priorityHigh') }}</option>
              </select>
            </div>
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('projects.taskDueDate') }}</label>
              <input
                v-model="newDueDate"
                type="date"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>
          <div class="flex justify-end gap-3">
            <button
              type="button"
              class="rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              @click="showCreateModal = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              :disabled="createTask.isPending.value"
              class="rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
            >
              {{ t('common.save') }}
            </button>
          </div>
        </form>
      </div>
    </div>

    <!-- Task detail modal -->
    <div
      v-if="showDetailModal && selectedTask"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      @click.self="showDetailModal = false"
    >
      <div class="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">{{ t('projects.editTask') }}</h2>
          <div class="flex items-center gap-2">
            <button
              class="text-red-400 hover:text-red-600"
              @click="handleDelete(selectedTask!.id)"
            >
              <Trash2 class="h-5 w-5" />
            </button>
            <button class="text-gray-400 hover:text-gray-600" @click="showDetailModal = false">
              <X class="h-5 w-5" />
            </button>
          </div>
        </div>
        <form class="mt-4 space-y-4" @submit.prevent="handleSaveDetail">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('projects.taskTitle') }}</label>
            <input
              v-model="editTitle"
              type="text"
              required
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('projects.taskDescription') }}</label>
            <textarea
              v-model="editDescription"
              rows="3"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('projects.taskPriority') }}</label>
              <select
                v-model="editPriority"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="low">{{ t('projects.priorityLow') }}</option>
                <option value="medium">{{ t('projects.priorityMedium') }}</option>
                <option value="high">{{ t('projects.priorityHigh') }}</option>
              </select>
            </div>
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('projects.taskDueDate') }}</label>
              <input
                v-model="editDueDate"
                type="date"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>
          <div class="flex justify-end gap-3">
            <button
              type="button"
              class="rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              @click="showDetailModal = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              :disabled="updateTask.isPending.value"
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
