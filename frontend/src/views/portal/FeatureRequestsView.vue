<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  useFeatureRequests,
  useCreateFeatureRequest,
  useVote,
  type FeatureRequest,
} from '@/composables/useFeatureRequests'
import { ThumbsUp, ThumbsDown, Plus, X } from 'lucide-vue-next'

const { t } = useI18n()

const statusFilter = ref('')
const { data: requests, isLoading, isError } = useFeatureRequests(() => statusFilter.value)
const createRequest = useCreateFeatureRequest()
const vote = useVote()

const showModal = ref(false)
const newTitle = ref('')
const newDescription = ref('')

const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const statusOptions = ['', 'proposed', 'reviewing', 'accepted', 'done'] as const

const statusClasses: Record<string, string> = {
  proposed: 'bg-gray-100 text-gray-700',
  reviewing: 'bg-yellow-100 text-yellow-800',
  accepted: 'bg-green-100 text-green-800',
  rejected: 'bg-red-100 text-red-700',
  done: 'bg-blue-100 text-blue-700',
}

function openModal() {
  newTitle.value = ''
  newDescription.value = ''
  showModal.value = true
}

function handleSubmit() {
  if (!newTitle.value.trim()) return
  createRequest.mutate(
    { title: newTitle.value.trim(), description: newDescription.value.trim() },
    {
      onSuccess: () => {
        showModal.value = false
        showToast('success', t('featureRequests.submitSuccess'))
      },
      onError: () => {
        showToast('error', t('featureRequests.submitError'))
      },
    },
  )
}

function handleVote(request: FeatureRequest, value: 1 | -1) {
  vote.mutate({ requestId: request.id, value })
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('featureRequests.title') }}</h1>
      <button
        class="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
        @click="openModal"
      >
        <Plus class="h-4 w-4" />
        {{ t('featureRequests.submit') }}
      </button>
    </div>

    <div class="mt-4 flex gap-2">
      <button
        v-for="status in statusOptions"
        :key="status || 'all'"
        :class="[
          'rounded-full px-3 py-1 text-sm font-medium transition',
          statusFilter === status
            ? 'bg-blue-600 text-white'
            : 'bg-gray-100 text-gray-700 hover:bg-gray-200',
        ]"
        @click="statusFilter = status"
      >
        {{ status ? t(`featureRequests.status${status.charAt(0).toUpperCase() + status.slice(1)}`) : t('featureRequests.filterAll') }}
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
      {{ t('featureRequests.loadError') }}
    </div>

    <div v-else-if="!requests?.length" class="mt-6 text-gray-500">
      {{ t('featureRequests.noRequests') }}
    </div>

    <div v-else class="mt-6 space-y-3">
      <div
        v-for="request in requests"
        :key="request.id"
        class="rounded-lg border border-gray-200 bg-white p-4 shadow-sm"
      >
        <div class="flex items-start gap-4">
          <div class="flex flex-col items-center gap-1">
            <button
              :class="[
                'rounded p-1 transition',
                request.user_vote === 1
                  ? 'text-blue-600 bg-blue-50'
                  : 'text-gray-400 hover:text-blue-600 hover:bg-blue-50',
              ]"
              @click="handleVote(request, 1)"
            >
              <ThumbsUp class="h-4 w-4" />
            </button>
            <span class="text-sm font-semibold" :class="request.vote_count > 0 ? 'text-blue-600' : request.vote_count < 0 ? 'text-red-600' : 'text-gray-500'">
              {{ request.vote_count }}
            </span>
            <button
              :class="[
                'rounded p-1 transition',
                request.user_vote === -1
                  ? 'text-red-600 bg-red-50'
                  : 'text-gray-400 hover:text-red-600 hover:bg-red-50',
              ]"
              @click="handleVote(request, -1)"
            >
              <ThumbsDown class="h-4 w-4" />
            </button>
          </div>

          <div class="flex-1">
            <div class="flex items-center gap-2">
              <h3 class="font-semibold text-gray-900">{{ request.title }}</h3>
              <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', statusClasses[request.status]]">
                {{ t(`featureRequests.status${request.status.charAt(0).toUpperCase() + request.status.slice(1)}`) }}
              </span>
            </div>
            <p v-if="request.description" class="mt-1 text-sm text-gray-600">
              {{ request.description }}
            </p>
            <p class="mt-2 text-xs text-gray-400">
              {{ new Date(request.created_at).toLocaleDateString() }}
            </p>
          </div>
        </div>
      </div>
    </div>

    <!-- Submit new request modal -->
    <div
      v-if="showModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      @click.self="showModal = false"
    >
      <div class="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <div class="flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">{{ t('featureRequests.submitNew') }}</h2>
          <button class="text-gray-400 hover:text-gray-600" @click="showModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>
        <form class="mt-4 space-y-4" @submit.prevent="handleSubmit">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('featureRequests.requestTitle') }}</label>
            <input
              v-model="newTitle"
              type="text"
              required
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('featureRequests.requestDescription') }}</label>
            <textarea
              v-model="newDescription"
              rows="4"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              :placeholder="t('featureRequests.descriptionPlaceholder')"
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
              :disabled="createRequest.isPending.value"
              class="rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
            >
              {{ t('common.submit') }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>
